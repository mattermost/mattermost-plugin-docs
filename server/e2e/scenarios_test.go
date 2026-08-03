//go:build e2e

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	pluginmodel "github.com/mattermost/mattermost-plugin-docs/server/model"
)

// TestScenarios runs the seven canonical Confluence space-permission scenarios plus the named
// parity gaps against a real Mattermost server (built from the paired core branch) with the
// plugin installed, via Testcontainers. scripts/smoke-scenarios.sh is the authoritative
// behavioral spec and runs the same scenarios as a bash suite; the two are kept in lockstep, not
// merged.
func TestScenarios(t *testing.T) {
	env := getEnv(t)
	ctx := context.Background()

	teamName := fmt.Sprintf("e2e-scenario-%d", time.Now().UnixNano())
	team, _, err := env.adminClient.CreateTeam(ctx, &mmmodel.Team{
		Name:        teamName,
		DisplayName: "E2E Scenario Smoke",
		Type:        mmmodel.TeamOpen,
	})
	require.NoError(t, err, "failed to create scenario team")

	ts := time.Now().UnixNano()
	spaceAdmin := createActor(t, ctx, env, team.Id, fmt.Sprintf("scn-admin-%d", ts))
	contrib := createActor(t, ctx, env, team.Id, fmt.Sprintf("scn-contrib-%d", ts))
	member := createActor(t, ctx, env, team.Id, fmt.Sprintf("scn-member-%d", ts))
	outsider := createActor(t, ctx, env, team.Id, fmt.Sprintf("scn-outsider-%d", ts))
	guestCandidate := createActor(t, ctx, env, team.Id, fmt.Sprintf("scn-guest-%d", ts))

	// spacesToClean accumulates every space id created below; deleted once at the very end (after
	// the gap subtests, which continue off scenario 1's space) rather than per-subtest, since a
	// t.Cleanup registered on a subtest's own *testing.T fires when that subtest finishes — too
	// early for a space a later gap subtest still needs.
	var spacesToClean []string
	t.Cleanup(func() {
		for _, id := range spacesToClean {
			deleteSpace(t, ctx, spaceAdmin, id)
		}
	})

	var s1ID string // captured for the gap subtests, which continue off scenario 1's space.

	t.Run("scenario1_open_wiki", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Open Wiki"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		require.Regexp(t, "^[a-z0-9]{26}$", space.Id)
		require.Equal(t, pluginmodel.ViewAccessOpen, space.ViewAccess, "expected create-time default view_access=open")
		s1ID = space.Id
		spacesToClean = append(spacesToClean, s1ID)

		addSpaceMember(t, ctx, spaceAdmin, s1ID, member.id)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodGet, "/spaces/"+s1ID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "existing member read: %s", body)

		var page pluginmodel.Page
		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+s1ID+"/pages",
			createPageReq("Open Wiki Page"), &page)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "member create page failed: %s", body)
		require.Regexp(t, "^[a-z0-9]{26}$", page.Id)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+s1ID+"/pages/"+page.Id,
			editPageReq(page.EditAt, "edited by an existing member"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "existing member edit: %s", body)

		// Headline assertion: a team non-member's first default-granted write auto-joins them.
		status, body, err = doPluginRequest(ctx, outsider.client, http.MethodPost, "/spaces/"+s1ID+"/pages",
			createPageReq("Drive-by page"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "non-member create: %s", body)
		require.True(t, spaceHasMember(t, ctx, spaceAdmin, s1ID, outsider.id),
			"non-member's drive-by write did not auto-join them (real GetChannelMember/AddChannelMember round-trip)")
	})

	t.Run("scenario2_knowledge_base", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Knowledge Base"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)
		addSpaceMember(t, ctx, spaceAdmin, space.Id, contrib.id)

		var roResp pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/default-capabilities",
			map[string][]string{"default_capabilities": {}}, &roResp)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "setting read-only default failed: %s", body)
		require.Empty(t, roResp.DefaultCapabilities)

		var seed pluginmodel.Page
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("KB Seed Page"), &seed)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin seed page failed: %s", body)

		status, _, err = doPluginRequest(ctx, member.client, http.MethodGet, "/spaces/"+space.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "MEMBER read")

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Unauthorized"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "ungranted MEMBER create: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+space.Id+"/pages/"+seed.Id,
			editPageReq(seed.EditAt, "nope"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "ungranted MEMBER edit: %s", body)

		// The real grant surface: the space admin assigns create_page + edit_page to CONTRIB.
		var grantResp pluginmodel.SpaceMember
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/members/"+contrib.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityCreatePage, pluginmodel.CapabilityEditPage}}, &grantResp)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting CONTRIB capabilities: %s", body)
		require.ElementsMatch(t, []string{pluginmodel.CapabilityCreatePage, pluginmodel.CapabilityEditPage}, grantResp.GrantedCapabilities,
			"grant round-tripped as %v", grantResp.GrantedCapabilities)

		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Contributed"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "granted CONTRIB create: %s", body)

		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodPatch, "/spaces/"+space.Id+"/pages/"+seed.Id,
			editPageReq(seed.EditAt, "edited by contributor"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granted CONTRIB edit: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Still unauthorized"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "MEMBER still expected 403 after CONTRIB's grant: %s", body)
	})

	t.Run("scenario3_private_team_space", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Private Team Space", "view_access": pluginmodel.ViewAccessPrivate}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)
		// OUTSIDER is deliberately NOT added — it is the non-member control for this scenario.

		status, _, err = doPluginRequest(ctx, member.client, http.MethodGet, "/spaces/"+space.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "member read")

		var strangerBody map[string]any
		status, strangerRaw, err := doPluginRequest(ctx, outsider.client, http.MethodGet, "/spaces/"+space.Id, nil, &strangerBody)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "stranger metadata read: %s", strangerRaw)

		status, _, err = doPluginRequest(ctx, outsider.client, http.MethodGet, "/spaces/"+space.Id+"/pages", nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "stranger page-list read")

		// The identical 403 for a syntactically valid but nonexistent space id — no existence oracle.
		nonexistentID := reverseString(space.Id)
		var nonexistentBody map[string]any
		status, nonexistentRaw, err := doPluginRequest(ctx, outsider.client, http.MethodGet, "/spaces/"+nonexistentID, nil, &nonexistentBody)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "nonexistent-space read: %s", nonexistentRaw)
		require.Equal(t, strangerBody, nonexistentBody,
			"a real private space's 403 body differs from a nonexistent id's 403 body (existence oracle)")
	})

	t.Run("scenario4_announcement_space", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Announcement Space", "default_capabilities": []string{pluginmodel.CapabilityCommentPage}}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		// The create response is the bare Space (no default_capabilities field); GET the space to
		// confirm the comment default actually landed.
		var withAccess pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+space.Id, nil, &withAccess)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "get space: %s", body)
		require.ElementsMatch(t, []string{pluginmodel.CapabilityCommentPage}, withAccess.DefaultCapabilities,
			"comment default not set at create: %s", body)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)
		addSpaceMember(t, ctx, spaceAdmin, space.Id, contrib.id)

		status, _, err = doPluginRequest(ctx, member.client, http.MethodGet, "/spaces/"+space.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "plain member read")

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Unauthorized announcement"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "plain member create (comment default grants no create_page): %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/members/"+contrib.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityCreatePage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting CONTRIB create_page: %s", body)

		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Announcement"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "granted author create: %s", body)

		t.Log("scenario4: the comment halves (a plain member commenting, a granted commenter succeeding) are not asserted — the comments epic has not landed its routes yet, so there is nothing to drive them through.")
	})

	t.Run("scenario5_mixed_matrix", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Mixed Matrix", "default_capabilities": []string{}}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, contrib.id)
		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)

		var seed pluginmodel.Page
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Matrix Seed Page"), &seed)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin seed page failed: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/members/"+contrib.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityCreatePage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting A create_page: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/members/"+member.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityEditPage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting B edit_page: %s", body)

		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("By A"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "A (create_page-only) create: %s", body)

		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodPatch, "/spaces/"+space.Id+"/pages/"+seed.Id,
			editPageReq(seed.EditAt, "a"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "A (create_page-only) edit: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+space.Id+"/pages/"+seed.Id,
			editPageReq(seed.EditAt, "b"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "B (edit_page-only) edit: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("By B"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "B (edit_page-only) create: %s", body)
	})

	t.Run("scenario6_readonly_guest_reviewer", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Guest Reviewer"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, guestCandidate.id)

		cfg, _, err := env.adminClient.GetConfig(ctx)
		require.NoError(t, err, "failed to read server config")
		originalGuestEnable := cfg.GuestAccountsSettings.Enable != nil && *cfg.GuestAccountsSettings.Enable

		demoteResp, demoteErr := env.adminClient.DemoteUserToGuest(ctx, guestCandidate.id)
		if demoteErr != nil {
			var appErr *mmmodel.AppError
			if !errors.As(demoteErr, &appErr) {
				t.Fatalf("scenario6: demote failed with a non-AppError: %v", demoteErr)
			}
			if strings.Contains(appErr.Id, "license") {
				t.Logf("scenario6: guest reviewer flow (real DemoteUserToGuest) is not testable in this environment — %s: %s. "+
					"The core image built by build/build-core-image.sh has no Enterprise license, and guest accounts require one. "+
					"Not asserted here; see README.md.", appErr.Id, appErr.Error())
				return
			}
			if !strings.Contains(appErr.Id, "disabled") {
				t.Fatalf("scenario6: demote failed unexpectedly: %s (%s)", appErr.Id, appErr.Error())
			}

			t.Logf("scenario6: guest accounts disabled (%s) — enabling GuestAccountsSettings.Enable for this run", appErr.Id)
			_, _, patchErr := env.adminClient.PatchConfig(ctx, &mmmodel.Config{GuestAccountsSettings: mmmodel.GuestAccountsSettings{Enable: mmmodel.NewPointer(true)}})
			require.NoError(t, patchErr, "enabling GuestAccountsSettings failed")
			t.Cleanup(func() {
				_, _, restoreErr := env.adminClient.PatchConfig(ctx, &mmmodel.Config{GuestAccountsSettings: mmmodel.GuestAccountsSettings{Enable: mmmodel.NewPointer(originalGuestEnable)}})
				if restoreErr != nil {
					t.Logf("scenario6: failed to restore GuestAccountsSettings.Enable=%v: %v", originalGuestEnable, restoreErr)
				}
			})

			demoteResp, demoteErr = env.adminClient.DemoteUserToGuest(ctx, guestCandidate.id)
			require.NoError(t, demoteErr, "demote still failed after enabling guest accounts")
		}
		require.Equal(t, http.StatusOK, demoteResp.StatusCode, "demote expected 200")

		status, body, err = doPluginRequest(ctx, guestCandidate.client, http.MethodGet, "/spaces/"+space.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "guest read: %s", body)

		status, body, err = doPluginRequest(ctx, guestCandidate.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Not allowed"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "guest create: %s", body)

		var seed pluginmodel.Page
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Guest Seed Page"), &seed)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin seed page failed: %s", body)

		status, body, err = doPluginRequest(ctx, guestCandidate.client, http.MethodPatch, "/spaces/"+space.Id+"/pages/"+seed.Id,
			editPageReq(seed.EditAt, "guest edit"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "guest update: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/members/"+guestCandidate.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityCreatePage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, status, "granting a guest capabilities: %s", body)
		require.Equal(t, "app.space.member.guest_not_assignable.app_error", appErrorID(body))
	})

	var s7ID string
	t.Run("scenario7_delegated_space_admin", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Delegated Admin"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		s7ID = space.Id
		spacesToClean = append(spacesToClean, s7ID)

		addSpaceMember(t, ctx, spaceAdmin, s7ID, member.id) // the plain-member control actor for this scenario
		updateAt := space.UpdateAt

		status, _, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+s7ID+"/members", nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin list members")

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+s7ID+"/members",
			map[string]string{"user_id": contrib.id}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin add member: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+s7ID+"/members/"+contrib.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityEditPage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin set member capabilities: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+s7ID+"/default-capabilities",
			map[string][]string{"default_capabilities": {pluginmodel.CapabilityCommentPage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin set default capabilities: %s", body)

		var patched pluginmodel.Space
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+s7ID,
			map[string]any{"view_access": pluginmodel.ViewAccessPrivate, "expected_update_at": updateAt}, &patched)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin ViewAccess flip failed: %s", body)
		require.Equal(t, pluginmodel.ViewAccessPrivate, patched.ViewAccess)
		updateAt = patched.UpdateAt

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodDelete, "/spaces/"+s7ID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin delete: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+s7ID+"/restore", nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin restore: %s", body)

		// A plain member (no elevation) is denied on each of the same routes.
		status, _, err = doPluginRequest(ctx, member.client, http.MethodGet, "/spaces/"+s7ID+"/members", nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control list members")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+s7ID+"/members",
			map[string]string{"user_id": outsider.id}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control add member")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+s7ID+"/members/"+contrib.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityEditPage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control set member capabilities")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+s7ID+"/default-capabilities",
			map[string][]string{"default_capabilities": {pluginmodel.CapabilityCommentPage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control set default capabilities")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+s7ID,
			map[string]any{"view_access": pluginmodel.ViewAccessOpen, "expected_update_at": updateAt}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control ViewAccess flip")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodDelete, "/spaces/"+s7ID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control delete")
	})

	// scenario8 exercises the space-private CUSTOM scheme path — a default-capability set matching
	// no preset — which the plugin provisions through core's CreateScheme + PatchRole plugin API.
	// It asserts the whole write→read chain end-to-end against real core: the custom set round-trips
	// through GET (proving PatchRole set exactly those role permissions and they project back), a
	// plain member is enforced against it, and switching back to a preset retires the now-unreferenced
	// custom scheme.
	t.Run("scenario8_custom_capability_scheme", func(t *testing.T) {
		customCaps := []string{pluginmodel.CapabilityCreatePage, pluginmodel.CapabilityEditPage}

		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Custom Capability Scheme", "default_capabilities": customCaps}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace with custom default caps failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		// The custom set must round-trip: create provisioned a docs_space_custom_* scheme whose user
		// role PatchRole set to exactly {read_page}+customCaps, and GET projects that back.
		var withAccess pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+space.Id, nil, &withAccess)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "get space: %s", body)
		require.ElementsMatch(t, customCaps, withAccess.DefaultCapabilities,
			"custom default caps did not round-trip (CreateScheme/PatchRole path): %s", body)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)

		// A plain member holds exactly the custom default: create and edit, but not delete-own.
		var page pluginmodel.Page
		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Member custom-create"), &page)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "granted custom create_page: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+space.Id+"/pages/"+page.Id,
			editPageReq(page.EditAt, "member custom-edit"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granted custom edit_page: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodDelete, "/spaces/"+space.Id+"/pages/"+page.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "custom default excludes delete_own_page: %s", body)

		// Switching to the read-only preset repoints the backing channel at the shared preset and
		// retires the now-unreferenced custom scheme (DeleteScheme); the member loses create.
		var roResp pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+space.Id+"/default-capabilities",
			map[string][]string{"default_capabilities": {}}, &roResp)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "switch custom→preset failed: %s", body)
		require.Empty(t, roResp.DefaultCapabilities, "default caps not cleared to read-only: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Should be forbidden now"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "member create after switch to read-only: %s", body)
	})

	t.Run("gap_open_space_removal_not_durable", func(t *testing.T) {
		// Continues from scenario 1's open space, where OUTSIDER is already a member via auto-join.
		require.NotEmpty(t, s1ID, "scenario1 must have run first")
		require.True(t, spaceHasMember(t, ctx, spaceAdmin, s1ID, outsider.id),
			"expected OUTSIDER to already be a member of %s from scenario1", s1ID)

		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodDelete, "/spaces/"+s1ID+"/members/"+outsider.id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "removing OUTSIDER: %s", body)
		require.False(t, spaceHasMember(t, ctx, spaceAdmin, s1ID, outsider.id),
			"OUTSIDER still listed as a member immediately after removal")

		status, body, err = doPluginRequest(ctx, outsider.client, http.MethodPost, "/spaces/"+s1ID+"/pages",
			createPageReq("Regained access"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "OUTSIDER's post-removal write: %s", body)
		require.True(t, spaceHasMember(t, ctx, spaceAdmin, s1ID, outsider.id),
			"OUTSIDER's default-granted write did not re-join them — removal should NOT be durable on an open space")
	})

	t.Run("gap_delete_page_not_grantable", func(t *testing.T) {
		require.NotEmpty(t, s1ID, "scenario1 must have run first")
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPatch, "/spaces/"+s1ID+"/members/"+contrib.id+"/capabilities",
			map[string][]string{"granted_capabilities": {pluginmodel.CapabilityDeletePage}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, status, "granting delete_page: %s", body)
	})

	t.Run("gap_anonymous_access_denied_on_open_space", func(t *testing.T) {
		require.NotEmpty(t, s1ID, "scenario1 must have run first")
		anon := mmmodel.NewAPIv4Client(env.baseURL)
		status, body, err := doPluginRequest(ctx, anon, http.MethodGet, "/spaces/"+s1ID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, status,
			"an unauthenticated read on an open space: %s", body)
	})

	// The two remaining named parity gaps have no API surface to probe: per-group capability
	// grants beyond member/admin (a synced group's GroupSyncable carries only a binary
	// SchemeAdmin, so an arbitrary per-group capability set has no route to request it), and the
	// export_space split (Export is mapped onto admin_space in this epic; no separate export
	// capability exists to grant, deny, or probe).
}

// reverseString reverses s, mirroring smoke-scenarios.sh's `echo "$ID" | rev` — used to derive a
// syntactically valid but nonexistent id from a real one.
func reverseString(s string) string {
	r := []rune(s)
	slices.Reverse(r)
	return string(r)
}
