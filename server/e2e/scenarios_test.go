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
// plugin installed, via Testcontainers. This suite is the authoritative behavioral spec for those
// scenarios.
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

		// Headline assertion: a team member who is not yet a space member is auto-joined by their
		// first default-granted write.
		status, body, err = doPluginRequest(ctx, outsider.client, http.MethodPost, "/spaces/"+s1ID+"/pages",
			createPageReq("Drive-by page"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "space non-member create: %s", body)
		require.True(t, spaceHasMember(t, ctx, spaceAdmin, s1ID, outsider.id),
			"space non-member's drive-by write did not auto-join them (real GetChannelMember/AddChannelMember round-trip)")
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
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/default-permissions",
			map[string][]string{"default_permissions": {}}, &roResp)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "setting read-only default failed: %s", body)
		require.Empty(t, roResp.DefaultPermissions)

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
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+contrib.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionCreatePage.Id, mmmodel.PermissionEditPage.Id}}, &grantResp)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting CONTRIB permissions: %s", body)
		require.ElementsMatch(t, []string{mmmodel.PermissionCreatePage.Id, mmmodel.PermissionEditPage.Id}, grantResp.GrantedPermissions,
			"grant round-tripped as %v", grantResp.GrantedPermissions)

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
			map[string]any{"title": "Scenario Private Team Space", "view_access": pluginmodel.ViewAccessPrivate}, &space)
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
			map[string]any{"title": "Scenario Announcement Space", "default_permissions": []string{mmmodel.PermissionCommentPage.Id}}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		// The create response is the bare Space (no default_permissions field); GET the space to
		// confirm the comment default actually landed.
		var withAccess pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+space.Id, nil, &withAccess)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "get space: %s", body)
		require.ElementsMatch(t, []string{mmmodel.PermissionCommentPage.Id}, withAccess.DefaultPermissions,
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

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+contrib.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionCreatePage.Id}}, nil)
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
			map[string]any{"title": "Scenario Mixed Matrix", "default_permissions": []string{}}, &space)
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

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+contrib.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionCreatePage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting A create_page: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+member.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionEditPage.Id}}, nil)
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

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+guestCandidate.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionCreatePage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, status, "granting a guest permissions: %s", body)
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

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+s7ID+"/members/"+contrib.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionEditPage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin set member permissions: %s", body)

		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+s7ID+"/default-permissions",
			map[string][]string{"default_permissions": {mmmodel.PermissionCommentPage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "admin set default permissions: %s", body)

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

		// A plain member (no elevation) is denied on each of the same write routes. The roster read
		// is the one exception: it serves every reader of the space so a space view can render its
		// membership, and withholds authority by redacting the permission matrix rather than by
		// refusing. CONTRIB holds a real edit_page grant in this space, so there is something for
		// that redaction to hide.
		requireRedactedRoster(t, ctx, member, s7ID, "control list members")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPost, "/spaces/"+s7ID+"/members",
			map[string]string{"user_id": outsider.id}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control add member")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPut, "/spaces/"+s7ID+"/members/"+contrib.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionEditPage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control set member permissions")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPut, "/spaces/"+s7ID+"/default-permissions",
			map[string][]string{"default_permissions": {mmmodel.PermissionCommentPage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control set default permissions")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodPatch, "/spaces/"+s7ID,
			map[string]any{"view_access": pluginmodel.ViewAccessOpen, "expected_update_at": updateAt}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control ViewAccess flip")

		status, _, err = doPluginRequest(ctx, member.client, http.MethodDelete, "/spaces/"+s7ID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "control delete")
	})

	// scenario8 exercises the POOLED scheme path — a default-permission set matching no preset —
	// which the plugin provisions through core's CreateScheme + PatchRole plugin API. It asserts the
	// whole write→read chain end-to-end against real core: the custom set round-trips through GET
	// (proving PatchRole set exactly those role permissions and they project back), a plain member
	// is enforced against it, and switching back to a preset repoints the channel, leaving the
	// superseded scheme in place for any other space using it.
	t.Run("scenario8_custom_permission_scheme", func(t *testing.T) {
		customCaps := []string{mmmodel.PermissionCreatePage.Id, mmmodel.PermissionEditPage.Id}

		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Custom Permission Scheme", "default_permissions": customCaps}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace with custom default caps failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		// The custom set must round-trip: create resolved a pooled docs_space_default_* scheme whose
		// user role PatchRole set to exactly {read_page}+customCaps, and GET projects that back.
		var withAccess pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+space.Id, nil, &withAccess)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "get space: %s", body)
		require.ElementsMatch(t, customCaps, withAccess.DefaultPermissions,
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

		// Switching to the read-only preset repoints the backing channel at that preset's scheme;
		// the pooled scheme it leaves behind stays for the next space to request the same set. The
		// member loses create.
		var roResp pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/default-permissions",
			map[string][]string{"default_permissions": {}}, &roResp)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "switch custom→preset failed: %s", body)
		require.Empty(t, roResp.DefaultPermissions, "default caps not cleared to read-only: %s", body)

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

	// delete_page (delete-any) is grantable to a plain member without making them a space admin,
	// matching Confluence, where Delete is assignable independently of Admin. The grant reaches a
	// page the grantee does not own — that is the whole difference from delete_own_page.
	t.Run("delete_any_grantable_to_non_admin", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Delete Any"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, contrib.id)

		var seed pluginmodel.Page
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Admin-owned Page"), &seed)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin seed page failed: %s", body)

		// Before the grant: the contribute default carries delete_own_page only, so a page the
		// contributor does not own is out of reach.
		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodDelete, "/spaces/"+space.Id+"/pages/"+seed.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "delete of an unowned page before the grant: %s", body)

		var granted pluginmodel.SpaceMember
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+contrib.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionDeletePage.Id}}, &granted)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting delete_page: %s", body)
		require.Contains(t, granted.GrantedPermissions, mmmodel.PermissionDeletePage.Id,
			"delete_page did not round-trip through ExplicitRoles: %s", body)
		require.False(t, granted.IsAdmin, "granting delete_page must not make the member a space admin")

		status, body, err = doPluginRequest(ctx, contrib.client, http.MethodDelete, "/spaces/"+space.Id+"/pages/"+seed.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "delete of an unowned page after the grant: %s", body)
	})

	// The same permission as a space default rather than a per-member grant, so every member holds
	// delete-any. delete_page is in no preset, so this drives the pooled-scheme path end to end:
	// the scheme is minted, the backing channel is attached to it, and only then does the role
	// patch carrying delete_page become admissible to core.
	t.Run("delete_any_as_space_default", func(t *testing.T) {
		defaultCaps := []string{mmmodel.PermissionCreatePage.Id, mmmodel.PermissionDeletePage.Id}

		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Delete Any Default", "default_permissions": defaultCaps}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace with a delete_page default failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		var withAccess pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+space.Id, nil, &withAccess)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "get space: %s", body)
		require.ElementsMatch(t, defaultCaps, withAccess.DefaultPermissions,
			"delete_page did not round-trip as a space default: %s", body)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)

		var seed pluginmodel.Page
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Admin-owned Page"), &seed)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin seed page failed: %s", body)

		status, body, err = doPluginRequest(ctx, member.client, http.MethodDelete, "/spaces/"+space.Id+"/pages/"+seed.Id, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "plain member delete of an unowned page: %s", body)
	})

	// admin_space is the one grantable permission that carries administration itself rather than a
	// page action, so it is asserted as a triad: refused before the grant, admitted after it, and
	// refused again after the revoke. scenario7 drives the same routes as the space's creator, who is
	// an admin by construction — it never grants admin_space to anyone, so the delegation its name
	// describes is exercised only here.
	t.Run("admin_space_grant_delegates_administration", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Delegated Admin Grant"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		spacesToClean = append(spacesToClean, space.Id)

		addSpaceMember(t, ctx, spaceAdmin, space.Id, member.id)

		// Before the grant: a plain member is refused the write route that requires space
		// administration, and reads the roster in its redacted projection, so the grant below is
		// shown to change both.
		requireRedactedRoster(t, ctx, member, space.Id, "roster read before the admin_space grant")

		status, body, err = doPluginRequest(ctx, member.client, http.MethodPut, "/spaces/"+space.Id+"/default-permissions",
			map[string][]string{"default_permissions": {mmmodel.PermissionCommentPage.Id}}, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, "default-permissions write before the admin_space grant: %s", body)

		var granted pluginmodel.SpaceMember
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+member.id+"/permissions",
			map[string][]string{"granted_permissions": {mmmodel.PermissionAdminSpace.Id}}, &granted)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "granting admin_space: %s", body)
		// Exact, not Contains: the request granted admin_space alone, so anything else appearing
		// here is a grant the caller did not ask for. Contains would pass on a union with the
		// space's own defaults, which is the escalation shape worth pinning.
		require.Equal(t, []string{mmmodel.PermissionAdminSpace.Id}, granted.GrantedPermissions,
			"admin_space did not round-trip through the granted set as the only grant: %s", body)
		require.True(t, granted.IsAdmin,
			"granting admin_space must set SchemeAdmin, which is what the admin gates read: %s", body)
		require.ElementsMatch(t, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions), granted.Permissions,
			"the effective set of an admin_space grant must be exactly core's space-admin authority: %s", body)

		// After the grant: the write route answers, and the roster arrives unredacted, against real
		// core's role composition. The status alone would not show the change — the read answered
		// 200 before the grant too — so this asserts the matrix the redaction was withholding.
		afterGrant := listSpaceMembersAs(t, ctx, member, space.Id, "roster read after the admin_space grant")
		var own *spaceMemberJSON
		for _, m := range afterGrant.Items {
			if m.UserId == member.id {
				own = m
			}
		}
		require.NotNil(t, own, "the delegated admin does not appear in the roster it can now read")
		require.Equal(t, []string{mmmodel.PermissionAdminSpace.Id}, own.GrantedPermissions,
			"the unredacted roster does not report the admin_space grant this test just made")
		require.True(t, own.IsAdmin, "the unredacted roster does not report the delegated admin as an admin")

		var afterDefaults pluginmodel.SpaceWithAccess
		status, body, err = doPluginRequest(ctx, member.client, http.MethodPut, "/spaces/"+space.Id+"/default-permissions",
			map[string][]string{"default_permissions": {mmmodel.PermissionCommentPage.Id}}, &afterDefaults)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "default-permissions write after the admin_space grant: %s", body)
		require.ElementsMatch(t, []string{mmmodel.PermissionCommentPage.Id}, afterDefaults.DefaultPermissions,
			"the delegated admin's own default-permissions write did not take effect: %s", body)

		// Revoking is the half a grant-only test cannot cover: the delegated authority must be
		// withdrawable, and SchemeAdmin must come back down with it.
		var revoked pluginmodel.SpaceMember
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPut, "/spaces/"+space.Id+"/members/"+member.id+"/permissions",
			map[string][]string{"granted_permissions": {}}, &revoked)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "revoking admin_space: %s", body)
		require.NotContains(t, revoked.GrantedPermissions, mmmodel.PermissionAdminSpace.Id,
			"admin_space survived the revoke in the granted set: %s", body)
		require.False(t, revoked.IsAdmin, "SchemeAdmin survived the admin_space revoke: %s", body)

		requireRedactedRoster(t, ctx, member, space.Id, "roster read after the admin_space revoke")
	})

	// The auto-join undo path. scenario1 asserts the forward direction (a default-granted write by a
	// space non-member joins them); nothing asserted that a write which passes the permission gate
	// and then fails leaves no membership behind. The positive control at the end is what keeps the
	// negative from being vacuous: without it, an actor who was never auto-joinable at all would
	// satisfy the same assertion.
	t.Run("auto_join_undone_when_the_write_is_rejected", func(t *testing.T) {
		var space pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]string{"title": "Scenario Auto Join Undo"}, &space)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "createSpace failed: %s", body)
		require.Equal(t, pluginmodel.ViewAccessOpen, space.ViewAccess, "auto-join requires an open space")
		spacesToClean = append(spacesToClean, space.Id)

		// A team member who has never touched this space, so the pre-step has something to undo.
		joiner := createActor(t, ctx, env, team.Id, fmt.Sprintf("scn-undo-%d", time.Now().UnixNano()))
		require.False(t, spaceHasMember(t, ctx, spaceAdmin, space.Id, joiner.id),
			"the actor must start as a space non-member")

		// Seed a real page only to derive a syntactically valid id that resolves to nothing, so the
		// request clears the create_page gate (auto-joining) and then fails inside CreatePage's
		// parent resolution.
		var seed pluginmodel.Page
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Undo Anchor"), &seed)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "admin seed page failed: %s", body)

		missingParent := reverseString(seed.Id)
		require.NotEqual(t, seed.Id, missingParent, "the derived parent id must differ from the real one")

		req := createPageReq("Rejected drive-by page")
		req["parent_id"] = missingParent
		status, body, err = doPluginRequest(ctx, joiner.client, http.MethodPost, "/spaces/"+space.Id+"/pages", req, nil)
		require.NoError(t, err)
		require.NotEqual(t, http.StatusCreated, status,
			"a page create naming a nonexistent parent must not succeed: %s", body)

		require.False(t, spaceHasMember(t, ctx, spaceAdmin, space.Id, joiner.id),
			"the auto-join was not undone after the write it admitted was rejected: %s", body)

		// Positive control: the same actor on the same space, with a valid request, is auto-joined —
		// so the assertion above reflects the undo, not an actor who could never have joined.
		status, body, err = doPluginRequest(ctx, joiner.client, http.MethodPost, "/spaces/"+space.Id+"/pages",
			createPageReq("Accepted drive-by page"), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "space non-member create: %s", body)
		require.True(t, spaceHasMember(t, ctx, spaceAdmin, space.Id, joiner.id),
			"the control write did not auto-join the actor, so the undo assertion above proves nothing: %s", body)
	})

	// The pooled-scheme reuse property, against real core. scenario8 provisions a non-preset set but
	// is always its FIRST user, so getOrCreateSharedScheme only ever takes the create branch and
	// adoptableSharedScheme never runs. A second space requesting the same set is what exercises
	// adoption, which is where the plugin compares a role read back from core against the bare
	// permission set the pooled name implies.
	t.Run("pooled_scheme_reused_by_a_second_space", func(t *testing.T) {
		pooledCaps := []string{mmmodel.PermissionEditPage.Id, mmmodel.PermissionDeleteOwnPage.Id}

		var first pluginmodel.Space
		status, body, err := doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Pooled First", "default_permissions": pooledCaps}, &first)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status, "first space with a pooled set failed: %s", body)
		spacesToClean = append(spacesToClean, first.Id)

		var second pluginmodel.Space
		status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodPost, "/teams/"+team.Id+"/spaces",
			map[string]any{"title": "Scenario Pooled Second", "default_permissions": pooledCaps}, &second)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, status,
			"a second space requesting an already-pooled permission set was refused: %s", body)
		spacesToClean = append(spacesToClean, second.Id)

		// Both spaces must report the set they asked for: adoption that silently produced a
		// different effective set would be worse than the refusal.
		for _, s := range []struct {
			id   string
			what string
		}{{first.Id, "first"}, {second.Id, "second"}} {
			var access pluginmodel.SpaceWithAccess
			status, body, err = doPluginRequest(ctx, spaceAdmin.client, http.MethodGet, "/spaces/"+s.id, nil, &access)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status, "%s space read: %s", s.what, body)
			require.ElementsMatch(t, pooledCaps, access.DefaultPermissions,
				"%s space did not round-trip the pooled set: %s", s.what, body)
		}
	})

	t.Run("gap_anonymous_access_denied_on_open_space", func(t *testing.T) {
		require.NotEmpty(t, s1ID, "scenario1 must have run first")
		anon := mmmodel.NewAPIv4Client(env.baseURL)
		status, body, err := doPluginRequest(ctx, anon, http.MethodGet, "/spaces/"+s1ID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, status,
			"an unauthenticated read on an open space: %s", body)
	})

	// The two remaining named parity gaps have no API surface to probe: per-group permission
	// grants beyond member/admin (a synced group's GroupSyncable carries only a binary
	// SchemeAdmin, so an arbitrary per-group permission set has no route to request it), and the
	// export_space split (Export is mapped onto admin_space in this epic; no separate export
	// permission exists to grant, deny, or probe).
}

// reverseString reverses s — used to derive a syntactically valid but nonexistent id from a real
// one.
func reverseString(s string) string {
	r := []rune(s)
	slices.Reverse(r)
	return string(r)
}
