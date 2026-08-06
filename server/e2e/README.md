# Docs plugin — RBAC end-to-end suite

This is the **official** end-to-end suite for the docs plugin's space-permission RBAC model
(MM-69269). It is Go + [Testcontainers](https://golang.testcontainers.org/), and boots a real
Mattermost server with the plugin installed — no mocks.

It covers the seven canonical Confluence permission scenarios plus their named parity gaps, and is
the authoritative behavioral spec for them.

## Why a locally built core image

The core changes this epic depends on (the `Space` channel type, the atomic per-page capability
roles, the seeded preset schemes) live on an **unmerged** core branch, so no published Mattermost
image carries them yet. `build/build-core-image.sh` cross-compiles `./cmd/mattermost` from that
branch and packages it into a minimal Docker image (`$CORE_IMAGE`, default `mm-docs-rbac-core:dev`).

Building locally is only needed for a branch that has not been pushed. Core's own CI publishes a
server image for every commit as `mattermostdevelopment/mattermost-team-edition:<7-char-sha>`, so
once the paired core change is up as a PR, `CORE_IMAGE` can point at that tag and no local image
build is required — that is exactly how the `e2e` job in `.github/workflows/ci.yml` runs, reading
the tag from the `CORE_IMAGE` repository variable. Once those core changes merge and ship in a
release, point `CORE_IMAGE` at the released image instead and this suite runs unchanged.

Which core commit that variable is allowed to name is recorded in `build/core-commit.txt`, and CI
fails the suite when the two disagree. The pin lives in a file of its own rather than being read
off the `go.mod` `server/public` version, because most of the RBAC implementation sits outside the
public module: a core commit can change the scheme guards and role composition these scenarios
assert on without moving that version at all. Bump the pin in the same PR that depends on the newer
core behaviour.

The image this script builds is **API-only**: server binary plus i18n, templates and fonts, and an
empty `client/`. That is all the suites here need — every assertion is an HTTP call.

The browser suite does not use this image, or Testcontainers, at all. The System Console spec in
core's Playwright tests (`./scripts/run-tests.sh core-ui-e2e`) runs in Playwright's default
*external* mode against the docs-core server `start-docs-core-server.sh` already runs, so a run
costs seconds rather than a container boot. It needs that server to serve the webapp, which a
core checkout does not do out of the box:

```sh
(cd ../MM-69269-core/webapp && npm run build)              # once
cp -R ../MM-69269-core/webapp/channels/dist/. ../MM-69269-core/server/client/
./scripts/stop-docs-core-server.sh && ./scripts/start-docs-core-server.sh
```

Two details that are easy to lose an afternoon to: a **symlink** into `server/client` serves
`root.html` but 404s every asset, so copy rather than link; and the client directory is resolved
**once, at boot** (`fileutils.FindDir` in `web/static.go`), so populating it under a running server
registers no static handler — restart afterwards. Without the webapp the server answers `/api/v4`
normally and returns 500 for every page.

A bare tag (no `/`) is treated as locally built: if it is absent the suite fails immediately and
names the build script, rather than letting Testcontainers fail mid-boot. A namespaced tag is
assumed pullable and is left to Testcontainers to fetch.

## Running it

Requires Docker.

```sh
make test-e2e
```

This ensures the plugin bundle exists (`make dist` if it is missing or carries no linux binary for
the Docker daemon's architecture), builds the core image with `build/build-core-image.sh` unless
`CORE_IMAGE` is namespaced, then runs:

```sh
go test -tags e2e -count=1 -v ./server/e2e/...
```

The container boots once for the whole suite (container startup is slow) and is torn down after
all tests finish. Every scenario creates its own space and it is deleted at the end of the run.

## Known environment gap

Scenario 6 (read-only guest reviewer) drives the real `POST /users/{id}/demote` core endpoint.
That endpoint requires an Enterprise license (`api.team.demote_user_to_guest.license.error`), and
the core image built by `build/build-core-image.sh` has none. In that case the subtest skips
without asserting the guest-specific behavior, so the run reports it as SKIP rather than as a pass
that covered nothing. It does not fail the suite. This is the one place the repo's no-skip rule is
waived, and the waiver is marked `//nolint:forbidigo` at the call: the rule exists to stop a
missing prerequisite from going unreported, which a silent early return does more thoroughly than
a skip. Supplying a real EE license to the container (extend `startEnv` in `container_test.go`
with `mmcontainer.WithLicense`) would let this subtest run to completion.
