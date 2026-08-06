//go:build e2e

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package e2e is the official end-to-end suite for the docs plugin's space-permission RBAC model
// (see README.md in this directory). It boots a real Mattermost server (built from the paired core
// branch that carries the space-permission changes) via Testcontainers, installs the plugin into
// it, and drives the seven Confluence permission scenarios plus their named parity gaps through the
// real HTTP API — no mocks. Build with -tags e2e (see `make test-e2e`); it is excluded from
// `go test ./...` and CI's default run by the build tag.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	mmcontainer "github.com/mattermost/testcontainers-mattermost-go"
)

// pluginID is the Docs plugin's manifest id (see plugin.json at the repo root).
const pluginID = "com.mattermost.docs"

// defaultCoreImage matches the CORE_IMAGE default in build/build-core-image.sh — the image it
// produces from the paired core branch until those changes merge and ship in a published release
// image, at which point CORE_IMAGE can point at that instead. Override with the CORE_IMAGE env var.
const defaultCoreImage = "mm-docs-rbac-core:dev"

// testEnv holds the single Mattermost+plugin container shared by every scenario in this package.
type testEnv struct {
	container   *mmcontainer.MattermostContainer
	baseURL     string
	adminClient *mmmodel.Client4
}

var (
	sharedEnv     *testEnv
	sharedEnvErr  error
	sharedEnvOnce sync.Once
)

// TestMain boots the shared container once for the whole package and tears it down after every
// test has run — container startup is slow (a full Mattermost + Postgres + plugin install), so it
// must not happen per scenario.
func TestMain(m *testing.M) {
	code := m.Run()

	if sharedEnv != nil && sharedEnv.container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if err := sharedEnv.container.Terminate(ctx); err != nil {
			fmt.Printf("failed to terminate shared e2e container: %v\n", err)
		}
		cancel()
	}

	os.Exit(code)
}

// getEnv returns the shared test environment, starting the container on first use.
func getEnv(t *testing.T) *testEnv {
	t.Helper()
	sharedEnvOnce.Do(func() {
		sharedEnv, sharedEnvErr = startEnv()
	})
	if sharedEnvErr != nil {
		t.Fatalf("failed to start shared Mattermost+plugin container: %v", sharedEnvErr)
	}
	return sharedEnv
}

// startEnv resolves the plugin bundle and the core image, then boots the container.
func startEnv() (*testEnv, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	bundlePath, err := resolveBundlePath()
	if err != nil {
		return nil, err
	}

	imageTag := os.Getenv("CORE_IMAGE")
	if imageTag == "" {
		imageTag = defaultCoreImage
	}
	if imgErr := checkImageExists(imageTag); imgErr != nil {
		return nil, imgErr
	}

	license, err := resolveLicense()
	if err != nil {
		return nil, err
	}

	container, err := mmcontainer.RunContainer(ctx,
		withImage(imageTag),
		mmcontainer.WithPlugin(bundlePath, pluginID, nil),
		mmcontainer.WithEnv("MM_FEATUREFLAGS_ENABLEDOCS", "true"),
		mmcontainer.WithLicense(license),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start Mattermost container: %w", err)
	}

	// Teardown runs on its own context: the likeliest failure below is ctx's own deadline expiring,
	// and Terminate cannot clean up on an already-expired context — leaving a Mattermost+Postgres
	// pair running on the CI agent or dev machine.
	terminate := func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer termCancel()
		_ = container.Terminate(termCtx)
	}

	baseURL, err := container.URL(ctx)
	if err != nil {
		terminate()
		return nil, fmt.Errorf("failed to resolve container URL: %w", err)
	}

	adminClient, err := container.GetAdminClient(ctx)
	if err != nil {
		terminate()
		return nil, fmt.Errorf("failed to get admin client: %w", err)
	}

	// Core's advanced-permissions phase-2 migration (which seeds the scheme/role state the space
	// admin-role assignment in CreateSpace depends on) runs on an async scheduler job, not
	// synchronously at boot — mmcontainer's wait strategy only waits for the "Server is listening"
	// log line, well before that job's first run. Without this, the very first CreateSpace call
	// fails with app.space.create.admin_role_failed.app_error wrapping
	// app.schemes.is_phase_2_migration_completed.not_completed.app_error.
	if err := waitForPhase2Migration(ctx, adminClient); err != nil {
		terminate()
		return nil, err
	}

	return &testEnv{container: container, baseURL: baseURL, adminClient: adminClient}, nil
}

// waitForPhase2Migration polls a Phase2-migration-gated endpoint until it stops reporting
// "not completed", the deadline passes, or it fails for any other reason.
func waitForPhase2Migration(ctx context.Context, adminClient *mmmodel.Client4) error {
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		_, _, err := adminClient.GetSchemes(ctx, "", 0, 1)
		if err == nil {
			return nil
		}
		// Only the pending migration is worth waiting out. A bad token, a 500 or a dropped
		// connection will not resolve on its own, so it is surfaced now rather than after two
		// minutes of polling that would report it as a migration that never finished.
		var appErr *mmmodel.AppError
		if !errors.As(err, &appErr) || !strings.Contains(appErr.Id, "is_phase_2_migration_completed") {
			return fmt.Errorf("polling for the advanced-permissions phase-2 migration failed: %w", err)
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for advanced-permissions phase-2 migration: %w (last error: %v)", ctx.Err(), lastErr)
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("advanced-permissions phase-2 migration did not complete within 2 minutes: %w", lastErr)
}

// resolveBundlePath globs dist/ (relative to this package's directory) for the built plugin
// bundle, failing with a clear pointer to `make dist` when it is absent. Several bundles can sit
// there (a version bump, or a stale host-only bundle from `make server`), so the one with the
// newest modification time is selected — the same one `make test-e2e` inspects before deciding
// to rebuild.
func resolveBundlePath() (string, error) {
	matches, err := filepath.Glob("../../dist/" + pluginID + "-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to glob plugin bundle: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no plugin bundle found at dist/%s-*.tar.gz — run `make dist` first", pluginID)
	}
	newest, newestMod := "", time.Time{}
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest, newestMod = match, info.ModTime()
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no readable plugin bundle at dist/%s-*.tar.gz", pluginID)
	}
	return newest, nil
}

// checkImageExists fails clearly (naming the build script) rather than letting Testcontainers
// surface an opaque "no such image" error mid-boot.
//
// A locally built image has a bare tag and can only come from the build script, so its absence is
// a hard error. A namespaced tag (registry/repo:tag) is one CI publishes per core commit, so
// Testcontainers can pull it and a local miss is expected rather than a failure.
func checkImageExists(imageTag string) error {
	cmd := exec.Command("docker", "image", "inspect", imageTag) // #nosec -- imageTag is test config (CORE_IMAGE env var), not untrusted input
	if err := cmd.Run(); err == nil {
		return nil
	}
	if strings.Contains(imageTag, "/") {
		return nil
	}
	return fmt.Errorf("core image %q not found — build it with ./build/build-core-image.sh (CORE_IMAGE=%s)", imageTag, imageTag)
}

// resolveLicense returns the Enterprise license the server boots with. The pooled custom-scheme
// scenarios drive core's CreateScheme, which is gated on the CustomPermissionsSchemes feature, so
// an unlicensed server answers them with a 501 no assertion can make sense of.
//
// The license is never committed: MM_LICENSE carries it directly (how CI supplies it from a
// secret), MM_LICENSE_FILE names a file holding it (how a developer points at a local copy).
// Absence is a hard error naming both, in keeping with this suite's rule that a missing
// prerequisite fails loudly rather than quietly narrowing what the run proves.
func resolveLicense() (string, error) {
	if license := strings.TrimSpace(os.Getenv("MM_LICENSE")); license != "" {
		return license, nil
	}

	path := strings.TrimSpace(os.Getenv("MM_LICENSE_FILE"))
	if path == "" {
		return "", errors.New("no Enterprise license configured — set MM_LICENSE to the license itself, " +
			"or MM_LICENSE_FILE to a path holding it. The scheme-backed scenarios need one (see README.md)")
	}

	raw, err := os.ReadFile(path) // #nosec -- path is test config (MM_LICENSE_FILE env var), not untrusted input
	if err != nil {
		return "", fmt.Errorf("reading MM_LICENSE_FILE %q: %w", path, err)
	}
	license := strings.TrimSpace(string(raw))
	if license == "" {
		return "", fmt.Errorf("MM_LICENSE_FILE %q is empty", path)
	}
	return license, nil
}

// withImage overrides the image RunContainer would otherwise use (a stock Mattermost release,
// which does not carry this epic's unmerged core changes) with the locally built core image.
func withImage(img string) mmcontainer.MattermostCustomizeRequestOption {
	return func(req *mmcontainer.MattermostContainerRequest) {
		req.Image = img
	}
}
