// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Overview-page seeding on space creation is deferred to the editor/scaffolding epics, so it is not
// asserted here; the store-level DeleteSpace cascade is exercised by the store tests.

// openTestServiceWithAPI builds the standard DB-backed harness but wires the service to a
// pluginapi client backed by a plugintest.API mock, so channel operations can be exercised.
// pluginapi.Channel.Create polls for replica-DSN config internally before returning; stub
// GetConfig with an empty config so it returns immediately instead of hanging/erroring.
func openTestServiceWithAPI(t *testing.T, mockAPI *plugintest.API, options ...app.Option) *testHarness {
	t.Helper()
	h := openTestService(t)
	testutil.StubDefaultSpacePermissions(mockAPI)
	testutil.StubPresetSchemes(mockAPI)
	mockAPI.On("GetConfig").Return(&mmmodel.Config{}).Maybe()
	// Mutations publish best-effort WS events through the client; tests that assert event
	// content override this with exact-argument expectations.
	mockAPI.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// DeleteSpace/RestoreSpace log a debug line ("Deleting space"/"Restoring space", "space_id",
	// spaceID) before touching the store, mirroring CreatePage/DeletePage/RestorePage's convention.
	// CreateSpace logs a wider line (message plus "team_id" and "user_id" pairs); plugintest
	// flattens the variadic pairs, so cover both the three- and five-argument shapes.
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// MovePage logs message plus three pairs; MovePageToSpace logs message plus four.
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// The page-write gate reads the acting user to hold guests to read_page. Defaults to an
	// ordinary (non-guest) user; a test exercising the guest refusal registers its own stub first.
	mockAPI.On("GetUser", mock.Anything).Return(&mmmodel.User{}, nil).Maybe()
	// plugintest flattens a log call's variadic pairs into the mock's argument list, so a stub only
	// matches calls with exactly that many arguments. Cover each shape the service emits: LogWarn
	// with message plus two key/value pairs (ResolveSpaceRead/GetSpacesForTeam client-not-wired
	// denials), LogWarn with message plus three pairs (DeleteSpace, restoreSpaceChannel), and
	// LogError with message plus four (archiveOrphanChannel). A LogWarn with message plus five pairs
	// is also stubbed.
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// LogError shapes: message plus three pairs (the scheme-conflict log in schemeAppError,
	// UpdateSpace's channel-metadata sync failure, and a failed self-joined member removal) and
	// plus four (archiveOrphanChannel). A LogError with message plus two pairs is also stubbed.
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// CreateSpace assigns the creator SchemeAdmin via the scheme's resolved role-name string; no
	// test asserts the exact roles argument, so a wildcard catch-all covers every create.
	mockAPI.On("UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	h.svc = app.New(h.store, &client.Log, client, options...)
	return h
}

// TestServiceCreateSpace_ExplicitDefaultOverridesSiteTemplate verifies the site setting is a
// creation template, not an enforced policy: a caller that supplies a per-space default keeps it.
func TestServiceCreateSpace_ExplicitDefaultOverridesSiteTemplate(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI, app.WithNewSpaceDefaultPermissions(func() []string {
		permissions, _ := model.DefaultPermissionsForSchemeName(mmmodel.SchemeNameSpaceReadOnly)
		return permissions
	}))

	backingChannelID := mmmodel.NewId()
	commentSchemeID := testutil.PresetSchemeID(mmmodel.SchemeNameSpaceComment)
	mockAPI.On("CreateChannel", mock.MatchedBy(func(channel *mmmodel.Channel) bool {
		return channel.SchemeId != nil && *channel.SchemeId == commentSchemeID
	})).Return(&mmmodel.Channel{Id: backingChannelID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)

	permissions, ok := model.DefaultPermissionsForSchemeName(mmmodel.SchemeNameSpaceComment)
	require.True(t, ok)
	created, appErr := h.svc.CreateSpace(
		&model.Space{TeamId: mmmodel.NewId(), Title: "Discussion"},
		mmmodel.NewId(),
		&permissions,
		nil,
	)
	require.Nil(t, appErr)
	require.Equal(t, permissions, created.DefaultPermissions)
}

// TestServiceCreateSpace_BackingChannel verifies CreateSpace creates a ChannelTypeSpace
// backing channel, adds the creator as a member, and persists the channel id on the space.
func TestServiceCreateSpace_BackingChannel(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.MatchedBy(func(ch *mmmodel.Channel) bool {
		return ch.Type == mmmodel.ChannelTypeSpace && ch.TeamId == teamID
	})).Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space := &model.Space{TeamId: teamID, Title: "Test Space"}

	saved, appErr := h.svc.CreateSpace(space, userID, nil, nil)
	require.Nil(t, appErr)
	require.Equal(t, backingChannelID, saved.ChannelId)
	require.Equal(t, userID, saved.CreatorId)

	got, appErr := h.svc.GetSpace(saved.Id)
	require.Nil(t, appErr)
	require.Equal(t, backingChannelID, got.ChannelId)

	mockAPI.AssertExpectations(t)
}

// TestServiceCreateSpace_PresetSchemeMissing verifies an unseeded server is reported as a server
// fault rather than as a missing space. Core seeds the preset schemes; a space create that cannot
// find one says nothing about what the caller asked for, so it must not share the not-found key
// every ordinary row lookup returns.
func TestServiceCreateSpace_PresetSchemeMissing(t *testing.T) {
	mockAPI := &plugintest.API{}
	// Registered ahead of the harness, whose StubPresetSchemes would otherwise match first.
	mockAPI.On("GetSchemeByName", mmmodel.SchemeNameSpaceContribute).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{StatusCode: http.StatusNotFound})
	h := openTestServiceWithAPI(t, mockAPI)

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Unseeded"}, mmmodel.NewId(), nil, nil)

	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.preset_scheme_missing.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

// TestServiceCreateSpace_SchemeDenialKeepsStatus verifies a refusal core issues against the scheme
// API — a license gate, or the permissions migration still running — reaches the caller with the
// status core chose. Reporting it as a 500 would hide a condition the operator can act on.
func TestServiceCreateSpace_SchemeDenialKeepsStatus(t *testing.T) {
	mockAPI := &plugintest.API{}
	// The status and id core's own gate returns: creating a scheme without the license for it is
	// reported as not-implemented, not as a permission denial.
	mockAPI.On("GetOrCreatePluginChannelScheme", mock.Anything, mock.Anything, mock.Anything).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{StatusCode: http.StatusNotImplemented, Id: "app.scheme.plugin_scheme.scheme_license.app_error"})
	h := openTestServiceWithAPI(t, mockAPI)

	// A single permission matches no preset, so the create resolves through the shared pool.
	permissions := []string{mmmodel.PermissionCreatePage.Id}
	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Unlicensed"}, mmmodel.NewId(), &permissions, nil)

	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotImplemented, appErr.StatusCode)
	require.Equal(t, "app.scheme.plugin_scheme.scheme_license.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

// TestServiceCreateSpace_GuestLicenseDenialKeepsStatus is the other half of the pooled-scheme
// license gate: Docs always sends a non-empty guest role (read_page), so a server with custom
// schemes but without GuestAccountsPermissions refuses a non-preset default with the guest
// entitlement error rather than a generic 500.
func TestServiceCreateSpace_GuestLicenseDenialKeepsStatus(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("GetOrCreatePluginChannelScheme", mock.Anything, mock.Anything, mock.Anything).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{StatusCode: http.StatusNotImplemented, Id: "app.scheme.plugin_scheme.guest_license.app_error"})
	h := openTestServiceWithAPI(t, mockAPI)

	permissions := []string{mmmodel.PermissionCreatePage.Id}
	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Unlicensed Guest"}, mmmodel.NewId(), &permissions, nil)

	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotImplemented, appErr.StatusCode)
	require.Equal(t, "app.scheme.plugin_scheme.guest_license.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

// TestServiceCreateSpace_SchemeConflictKeepsRepairGuidance verifies store-direct corruption of a
// pooled scheme remains a server fault, reaches the caller with core's exact row-level guidance,
// and is also emitted to the server log for the operator who can perform the repair.
func TestServiceCreateSpace_SchemeConflictKeepsRepairGuidance(t *testing.T) {
	mockAPI := &plugintest.API{}
	schemeName := "plugin_channel_scheme_docs_deadbeef"
	coreMessage := "The pooled plugin scheme row with Schemes.Name=\"" + schemeName + "\" does not match its generated Roles rows; repair or permanently remove it with administrative database tooling, then retry."
	coreErr := &mmmodel.AppError{
		Id:            "app.scheme.plugin_scheme.conflict.app_error",
		Message:       coreMessage,
		DetailedError: "generated user role permissions do not match",
		StatusCode:    http.StatusInternalServerError,
	}
	mockAPI.On("GetOrCreatePluginChannelScheme", mock.Anything, mock.Anything, mock.Anything).
		Return((*mmmodel.Scheme)(nil), coreErr)
	mockAPI.On(
		"LogError",
		"pooled space scheme is inconsistent; inspect and repair the Schemes and Roles rows named by the core error, then retry",
		"error_id", coreErr.Id,
		"core_message", coreMessage,
		"core_details", coreErr.DetailedError,
	).Return().Once()
	h := openTestServiceWithAPI(t, mockAPI)

	permissions := []string{mmmodel.PermissionCreatePage.Id}
	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Blocked"}, mmmodel.NewId(), &permissions, nil)

	require.Same(t, coreErr, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Contains(t, appErr.Message, schemeName)
	require.Contains(t, appErr.Message, "administrative database tooling")
	mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
	mockAPI.AssertExpectations(t)
}

// TestServiceCreateSpace_ChannelIdRejected verifies a caller-supplied ChannelId is rejected
// before any backing-channel side effect.
func TestServiceCreateSpace_ChannelIdRejected(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	space := &model.Space{ChannelId: mmmodel.NewId(), TeamId: mmmodel.NewId(), Title: "Bad Space"}

	_, appErr := h.svc.CreateSpace(space, mmmodel.NewId(), nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

// TestServiceCreateSpace_ChannelCreationFails verifies that when the backing-channel create fails,
// CreateSpace returns the wrapped error and performs no follow-up side effect (no member add, no
// compensating delete — the channel was never created).
func TestServiceCreateSpace_ChannelCreationFails(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(nil, &mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Doomed"}, mmmodel.NewId(), nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.create.backing_channel_failed.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
	mockAPI.AssertNotCalled(t, "DeleteChannel", mock.Anything)
}

// TestServiceCreateSpace_ReplicaConfiguredSucceeds exercises space creation on a host with SQL
// replicas configured. Channel.Create skips its replica poll for space channels — the generic
// channel lookup it polls cannot see a space channel — so creation must succeed without polling
// GetChannel or archiving the channel.
func TestServiceCreateSpace_ReplicaConfiguredSucceeds(t *testing.T) {
	// The harness helper stubs GetConfig with an empty config, and identical no-argument
	// expectations cannot be overridden, so this test wires its mocks from scratch.
	mockAPI := &plugintest.API{}
	h := openTestService(t)
	testutil.StubDefaultSpacePermissions(mockAPI)
	testutil.StubPresetSchemes(mockAPI)
	mockAPI.On("GetConfig").
		Return(&mmmodel.Config{SqlSettings: mmmodel.SqlSettings{DataSourceReplicas: []string{"replica"}}}).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("UpdateChannelMemberRoles", channelID, userID, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)

	client := pluginapi.NewClient(mockAPI, nil)
	h.svc = app.New(h.store, &client.Log, client)

	saved, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Replicated"}, userID, nil, nil)
	require.Nil(t, appErr)
	require.Equal(t, channelID, saved.ChannelId)
	mockAPI.AssertNotCalled(t, "GetChannel", channelID)
	mockAPI.AssertNotCalled(t, "DeleteChannel", channelID)
	mockAPI.AssertExpectations(t)
}

// TestServiceCreateSpace_InvalidInput verifies the up-front validations reject before any
// backing-channel side effect.
func TestServiceCreateSpace_InvalidInput(t *testing.T) {
	cases := map[string]*model.Space{
		"empty team id": {TeamId: "", Title: "T"},
		"bad team id":   {TeamId: "not-an-id", Title: "T"},
		"missing title": {TeamId: mmmodel.NewId(), Title: "   "},
	}
	for name, space := range cases {
		t.Run(name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			h := openTestServiceWithAPI(t, mockAPI)

			_, appErr := h.svc.CreateSpace(space, mmmodel.NewId(), nil, nil)
			require.NotNil(t, appErr)
			require.Equal(t, 400, appErr.StatusCode)
			mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
		})
	}

	t.Run("nil space", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)

		_, appErr := h.svc.CreateSpace(nil, mmmodel.NewId(), nil, nil)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
	})
}

// TestServiceCreateSpace_CreateSpaceGate pins the create_space authorization gate, which team
// membership alone does not satisfy. Both directions are covered because they fail to different
// mutants: the denial catches the gate being dropped, and the sysadmin override catches the two
// conjuncts being swapped for a disjunction — under which a sysadmin lacking create_space would be
// wrongly refused.
func TestServiceCreateSpace_CreateSpaceGate(t *testing.T) {
	t.Run("active team member without create_space is refused", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		teamID := mmmodel.NewId()
		userID := mmmodel.NewId()
		// Registered before the harness, whose catch-all grants create_space to every non-guest:
		// mock.Mock matches expectations in registration order.
		mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionCreateSpace).Return(false).Maybe()
		h := openTestServiceWithAPI(t, mockAPI)

		_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Not Allowed"}, userID, nil, nil)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusForbidden, appErr.StatusCode)
		require.Equal(t, "app.space.create.forbidden.app_error", appErr.Id)
		// Refused before a real, visible channel is stood up in the target team.
		mockAPI.AssertNotCalled(t, "CreateChannel", mock.Anything)
	})

	t.Run("sysadmin without create_space is allowed", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		teamID := mmmodel.NewId()
		userID := mmmodel.NewId()
		backingChannelID := mmmodel.NewId()
		mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionCreateSpace).Return(false).Maybe()
		mockAPI.On("HasPermissionTo", userID, mmmodel.PermissionManageSystem).Return(true).Maybe()
		h := openTestServiceWithAPI(t, mockAPI)

		testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
		mockAPI.On("CreateChannel", mock.MatchedBy(func(ch *mmmodel.Channel) bool {
			return ch.Type == mmmodel.ChannelTypeSpace && ch.TeamId == teamID
		})).Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
		mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)

		saved, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Allowed"}, userID, nil, nil)
		require.Nil(t, appErr)
		require.Equal(t, backingChannelID, saved.ChannelId)
	})
}

// TestResolveSpaceRead_FormerTeamMemberDenied verifies that access to a team's space ends with
// team membership. The test simulates a backing-channel ChannelMember row that remained after the
// user's team departure: core resolves team read_space from the active membership, so the
// departed user no longer holds it and the team gate blocks them.
func TestResolveSpaceRead_FormerTeamMemberDenied(t *testing.T) {
	mockAPI := &plugintest.API{}
	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	// Registered before the harness, whose read_space catch-all grants it to everyone: mock.Mock
	// matches expectations in registration order.
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionReadSpace).Return(false)
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, userID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution)
	// The team gate blocks before any channel-scoped permission is consulted, so the lingering
	// ChannelMember row is irrelevant.
	mockAPI.AssertNotCalled(t, "HasPermissionToChannel", mock.Anything, mock.Anything, mock.Anything)
}

// TestServiceDeleteSpace_ArchivesBackingChannel verifies DeleteSpace archives the space's
// backing channel after the soft-delete.
func TestServiceDeleteSpace_ArchivesBackingChannel(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)
	mockAPI.On("GetChannelMembers", backingChannelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed"}, userID, nil, nil)
	require.Nil(t, appErr)

	require.Nil(t, h.svc.DeleteSpace(&space.Space))
	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)

	// The space is soft-deleted: a live read no longer finds it.
	_, appErr = h.svc.GetSpace(space.Id)
	require.NotNil(t, appErr)
	require.Equal(t, 404, appErr.StatusCode)
}

// TestServiceDeleteSpace_ArchiveFailureTolerated verifies that when the backing-channel archive
// fails during DeleteSpace, the space is still soft-deleted: the channel-archive call is
// best-effort, so its failure must not roll back or fail the operation.
func TestServiceDeleteSpace_ArchiveFailureTolerated(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	mockAPI.On("GetChannelMembers", backingChannelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed"}, userID, nil, nil)
	require.Nil(t, appErr)

	require.Nil(t, h.svc.DeleteSpace(&space.Space), "DeleteSpace must succeed even though the channel archive fails")
	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)

	// The space is soft-deleted regardless of the channel-archive failure.
	_, appErr = h.svc.GetSpace(space.Id)
	require.NotNil(t, appErr)
	require.Equal(t, 404, appErr.StatusCode)
}

// TestServiceRestoreSpace_ChannelRestoreFailurePropagates verifies that when un-archiving the
// backing channel fails during RestoreSpace, the operation reports the failure rather than
// reporting success while the channel stays archived: a restored-looking space whose channel is
// still archived is more visibly broken than DeleteSpace's best-effort archive leaving a channel
// live, so the two are not symmetric.
func TestServiceRestoreSpace_ChannelRestoreFailurePropagates(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	channel := testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	// The channel is still archived, so the un-archive genuinely failed and must propagate.
	// Set on the shared channel MustSeedChannelScheme's GetChannelOfType stub returns, rather than
	// a second competing stub: DeleteAt is irrelevant to CreateSpace's own scheme-resolution read
	// of the same channel, so it can be set from the start.
	channel.DeleteAt = 100
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)
	mockAPI.On("GetChannelMembers", backingChannelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)
	mockAPI.On("RestoreChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Round Trip"}, userID, nil, nil)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(&space.Space))

	_, appErr = h.svc.RestoreSpace(space.Id)
	require.NotNil(t, appErr, "RestoreSpace must fail when the channel un-archive fails")
	require.Equal(t, "app.space.restore.channel_restore_failed.app_error", appErr.Id)
	mockAPI.AssertCalled(t, "RestoreChannel", backingChannelID)

	// The space row itself is left restored even though the channel-restore call failed.
	got, appErr := h.svc.GetSpace(space.Id)
	require.Nil(t, appErr)
	require.Zero(t, got.DeleteAt)
}

// TestServiceRestoreSpace_RetriesStuckChannelRestore verifies that a space left in the partial state
// created by TestServiceRestoreSpace_ChannelRestoreFailurePropagates — row live, backing channel
// still archived — is not permanently stuck. The second RestoreSpace sees the row is already live,
// finds the channel still archived, and finishes the un-archive instead of rejecting the caller with
// not_deleted.
func TestServiceRestoreSpace_RetriesStuckChannelRestore(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	channel := testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	// The channel stays archived throughout, so both the failure check and the retry see it as
	// genuinely needing an un-archive. Set on the shared channel MustSeedChannelScheme's
	// GetChannelOfType stub returns, rather than a second competing stub.
	channel.DeleteAt = 100
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)
	mockAPI.On("GetChannelMembers", backingChannelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)
	// The first un-archive fails, stranding the restore half-done; the retry succeeds.
	mockAPI.On("RestoreChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError}).Once()
	mockAPI.On("RestoreChannel", backingChannelID).Return(nil).Once()

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Stuck Restore"}, userID, nil, nil)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(&space.Space))

	_, appErr = h.svc.RestoreSpace(space.Id)
	require.NotNil(t, appErr, "the first RestoreSpace must fail on the channel un-archive")
	require.Equal(t, "app.space.restore.channel_restore_failed.app_error", appErr.Id)

	restored, appErr := h.svc.RestoreSpace(space.Id)
	require.Nil(t, appErr, "the retry must finish the channel un-archive, not reject with not_deleted")
	require.Equal(t, space.Id, restored.Id)
	require.Zero(t, restored.DeleteAt)

	mockAPI.AssertNumberOfCalls(t, "RestoreChannel", 2)
}

// TestServiceRestoreSpace_ChannelNeverArchived verifies a space whose backing channel failed to
// archive during DeleteSpace can still be restored. DeleteSpace archives best-effort and swallows
// the failure, so the space row is soft-deleted while the channel stays live; core then rejects
// un-archiving that live channel. Restore must treat "already live" as nothing to do rather than
// leaving the space permanently un-restorable.
func TestServiceRestoreSpace_ChannelNeverArchived(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	// MustSeedChannelScheme's shared channel defaults to DeleteAt 0 (live), matching this test's
	// "the channel stays live" intent — no separate GetChannelOfType stub needed.
	testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	// The archive fails transiently: the space row is soft-deleted, the channel stays live.
	mockAPI.On("DeleteChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "transient", StatusCode: http.StatusInternalServerError})
	mockAPI.On("GetChannelMembers", backingChannelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)
	// Core rejects un-archiving a channel that was never archived.
	mockAPI.On("RestoreChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "channel is not archived", StatusCode: http.StatusBadRequest})

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Never Archived"}, userID, nil, nil)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(&space.Space))

	restored, appErr := h.svc.RestoreSpace(space.Id)
	require.Nil(t, appErr, "RestoreSpace must succeed when the backing channel was never archived")
	require.Zero(t, restored.DeleteAt)

	got, appErr := h.svc.GetSpace(space.Id)
	require.Nil(t, appErr)
	require.Zero(t, got.DeleteAt)
}

// TestServiceCreateSpace_CompensatingDelete verifies that when the space row save fails
// after the backing channel is created, the orphan channel is deleted.
func TestServiceCreateSpace_CompensatingDelete(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	collisionChannelID := mmmodel.NewId()

	// Pre-seed a live space that already owns collisionChannelID, so the second insert
	// trips the unique channel-id constraint and the row save fails.
	mustCreateSpace(t, h.store, collisionChannelID)

	testutil.MustSeedChannelScheme(t, mockAPI, collisionChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: collisionChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", collisionChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", collisionChannelID).Return(nil)

	space := &model.Space{TeamId: teamID, Title: "Doomed Space"}

	_, appErr := h.svc.CreateSpace(space, userID, nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, 409, appErr.StatusCode)
	mockAPI.AssertCalled(t, "DeleteChannel", collisionChannelID)
}

// TestServiceCreateSpace_CompensatingDeleteAlsoFails verifies that when the row save fails AND the
// compensating channel archive also fails, CreateSpace still returns the original row-save error
// (the archive failure is logged, not surfaced) rather than masking it.
func TestServiceCreateSpace_CompensatingDeleteAlsoFails(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	collisionChannelID := mmmodel.NewId()

	// Pre-seed a live space that already owns collisionChannelID, so the second insert
	// trips the unique channel-id constraint and the row save fails.
	mustCreateSpace(t, h.store, collisionChannelID)

	testutil.MustSeedChannelScheme(t, mockAPI, collisionChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: collisionChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", collisionChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", collisionChannelID).
		Return(&mmmodel.AppError{Message: "archive also failed", StatusCode: http.StatusInternalServerError})

	space := &model.Space{TeamId: teamID, Title: "Doomed Space"}

	_, appErr := h.svc.CreateSpace(space, userID, nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, 409, appErr.StatusCode, "the original row-save conflict must surface even though the compensating archive also failed")
	mockAPI.AssertCalled(t, "DeleteChannel", collisionChannelID)
}

// TestServiceCreateSpace_AddMemberFailedCompensates verifies that when adding the creator to the
// backing channel fails, CreateSpace deletes the orphan channel and returns the add-member error.
func TestServiceCreateSpace_AddMemberFailedCompensates(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).
		Return(nil, &mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed"}, userID, nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.create.add_member_failed.app_error", appErr.Id)
	// The orphan channel is removed; no space row is persisted for the team.
	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)

	// Make the caller a member of the orphan channel so the emptiness check below proves no
	// space row was persisted, not merely that the caller has no visible channels.
	testutil.MustAddChannelMember(t, h.db, backingChannelID, userID)
	spaces, _, listErr := h.svc.GetSpacesForTeam(teamID, userID, 0, 60)
	require.Nil(t, listErr)
	require.Empty(t, spaces)
}

// TestServiceCreateSpace_PooledSchemeFailureArchivesChannel covers the compensating path after a
// non-preset permission set resolves to a pooled scheme: when a later create step fails, the
// backing channel is archived.
func TestServiceCreateSpace_PooledSchemeFailureArchivesChannel(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	channel := testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	pool := testutil.StubSchemePool(mockAPI)
	// CreateChannel attaches the pooled scheme, which is what lets core admit the role writes.
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Run(func(args mock.Arguments) {
			created, ok := args.Get(0).(*mmmodel.Channel)
			require.True(t, ok)
			require.NotNil(t, created.SchemeId)
			channel.SchemeId = created.SchemeId
		}).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).
		Return(nil, &mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)

	_, appErr := h.svc.CreateSpace(
		&model.Space{TeamId: teamID, Title: "Doomed Pooled"}, userID, &[]string{"create_page"}, nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.create.add_member_failed.app_error", appErr.Id)

	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)
	require.NotNil(t, channel.SchemeId, "the doomed channel must have carried the pooled scheme")
	createdScheme, ok := pool.Last()
	require.True(t, ok, "the non-preset set must have resolved through the pool")
	require.Equal(t, createdScheme.SchemeID, *channel.SchemeId)
}

// TestServiceBuildSpaceWithAccess_TeamManagerGetsManageTier covers the caller whose authority over a
// space arrives from outside it.
//
// The caller holds team manage_space but is not a member of the space's backing channel, so the read
// resolves through the open-space fall-through and their page authority is read_page alone. The
// routes that read and write the member roster admit exactly this caller, so an effective set
// reporting page permissions only would hide the roster from someone the server would serve —
// manage_space in that set is what carries the tier across.
func TestServiceBuildSpaceWithAccess_TeamManagerGetsManageTier(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Registered before the harness so these beat StubDefaultSpacePermissions' catch-all: no
	// backing-channel page permission at all, which is what forces the fall-through rather than a
	// member read.
	for _, p := range []*mmmodel.Permission{
		mmmodel.PermissionReadPage, mmmodel.PermissionCreatePage, mmmodel.PermissionCommentPage,
		mmmodel.PermissionEditPage, mmmodel.PermissionDeleteOwnPage, mmmodel.PermissionDeletePage,
		mmmodel.PermissionAdminSpace,
	} {
		mockAPI.On("HasPermissionToChannel", userID, mock.Anything, p).Return(false).Maybe()
	}
	teamID := mmmodel.NewId()
	// Also registered ahead of the harness: StubDefaultSpacePermissions denies team manage_space to
	// everyone, and mock.Mock answers with the first matching expectation.
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionReadPublicChannel).Return(true).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionManageSpace).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := testutil.MustCreateSpace(t, h.store, channelID, teamID)
	open := model.ViewAccessOpen
	space, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &open}, space.UpdateAt, false)
	require.NoError(t, err)

	wrapper, appErr := h.svc.BuildSpaceWithAccess(space, userID)

	require.Nil(t, appErr)
	require.ElementsMatch(t,
		[]string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionManageSpace.Id},
		wrapper.Permissions,
		"the caller effectively holds read_page on pages and the manage tier over the space, and the "+
			"effective set must state both — page authority alone would lock them out of the roster")
	require.NotContains(t, wrapper.Permissions, mmmodel.PermissionAdminSpace.Id,
		"the manage tier must not imply the administer tier: the space-wide knobs stay refused")
}

// TestServiceBuildSpaceWithAccess_TeamDeleterGetsDeleteTierOnly covers the caller the archive route
// admits and the roster routes refuse.
//
// The two team permissions behind these tiers are independent, so an effective set that reported
// only the manage tier would leave a client with no way to tell whether archive is offered: it
// would hide it from this caller, whom requireSpaceDelete admits, and offer it to a manage_space
// holder it refuses. Both tiers therefore appear as themselves.
func TestServiceBuildSpaceWithAccess_TeamDeleterGetsDeleteTierOnly(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	for _, p := range []*mmmodel.Permission{
		mmmodel.PermissionReadPage, mmmodel.PermissionCreatePage, mmmodel.PermissionCommentPage,
		mmmodel.PermissionEditPage, mmmodel.PermissionDeleteOwnPage, mmmodel.PermissionDeletePage,
		mmmodel.PermissionAdminSpace,
	} {
		mockAPI.On("HasPermissionToChannel", userID, mock.Anything, p).Return(false).Maybe()
	}
	teamID := mmmodel.NewId()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionReadPublicChannel).Return(true).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionManageSpace).Return(false).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionDeleteSpace).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := testutil.MustCreateSpace(t, h.store, channelID, teamID)
	open := model.ViewAccessOpen
	space, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &open}, space.UpdateAt, false)
	require.NoError(t, err)

	wrapper, appErr := h.svc.BuildSpaceWithAccess(space, userID)

	require.Nil(t, appErr)
	require.ElementsMatch(t,
		[]string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionDeleteSpace.Id},
		wrapper.Permissions,
		"the archive route admits this caller, so the effective set must say so without also "+
			"claiming a manage tier they do not hold")
}

// TestServiceBuildSpaceWithAccess_TeamManagerHasNoDeleteTier is the other half of the same
// independence: holding team manage_space says nothing about archiving, which requireSpaceDelete
// gates on a different team permission.
func TestServiceBuildSpaceWithAccess_TeamManagerHasNoDeleteTier(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionAdminSpace).Return(false).Maybe()
	teamID := mmmodel.NewId()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionManageSpace).Return(true).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionDeleteSpace).Return(false).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := testutil.MustCreateSpace(t, h.store, channelID, teamID)

	mockAPI.On("HasPermissionTo", userID, mmmodel.PermissionManageSystem).Return(false).Maybe()
	mockAPI.On("GetChannelMember", channelID, userID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: userID, SchemeUser: true}, nil).Maybe()

	wrapper, appErr := h.svc.BuildSpaceWithAccess(space, userID)

	require.Nil(t, appErr)
	require.Contains(t, wrapper.Permissions, mmmodel.PermissionManageSpace.Id)
	require.NotContains(t, wrapper.Permissions, mmmodel.PermissionDeleteSpace.Id,
		"the manage tier must not carry archive across; the delete route would refuse this caller")
}

// TestServiceBuildSpaceWithAccess_PlainMemberHasNoManageTier is the negative counterpart: an
// ordinary member with neither space-admin nor team manage_space must not be told they can manage
// members, or the client would offer a roster the server refuses. Membership alone is not the
// manage tier.
func TestServiceBuildSpaceWithAccess_PlainMemberHasNoManageTier(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionAdminSpace).Return(false).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := testutil.MustCreateSpace(t, h.store, channelID, teamID)

	mockAPI.On("HasPermissionTo", userID, mmmodel.PermissionManageSystem).Return(false).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionManageSpace).Return(false).Maybe()
	mockAPI.On("GetChannelMember", channelID, userID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: userID, SchemeUser: true}, nil).Maybe()

	wrapper, appErr := h.svc.BuildSpaceWithAccess(space, userID)

	require.Nil(t, appErr)
	require.NotContains(t, wrapper.Permissions, mmmodel.PermissionManageSpace.Id,
		"an ordinary member holds no manage tier; being in the space is not authority over it")
}

// TestServiceSetSpaceDefaultPermissions_ResponseReflectsRequestedNotStaleReadback covers the
// projection guard: the response is built from the permission set written under the lock, not
// from a fresh read of the roles that write just committed. The response must report the requested
// set even though the channel-scheme fixture still carries the pre-update permissions.
func TestServiceSetSpaceDefaultPermissions_ResponseReflectsRequestedNotStaleReadback(t *testing.T) {
	mockAPI := &plugintest.API{}
	sysadminID := mmmodel.NewId()
	// Registered before the harness so the response's own-permission projection resolves via
	// sysadmin (AdminEffectivePermissions), sidestepping the ReadViaMember channel-member lookup
	// this test does not otherwise stub. mock.Mock matches expectations in registration order.
	mockAPI.On("HasPermissionTo", sysadminID, mmmodel.PermissionManageSystem).Return(true)
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)

	testutil.StubSchemePool(mockAPI)

	space := mustCreateSpace(t, h.store, channelID)

	updated, appErr := h.svc.SetSpaceDefaultPermissions(space, []string{"create_page"}, sysadminID)
	require.Nil(t, appErr)
	require.NotNil(t, updated)
	require.ElementsMatch(t, []string{"create_page"}, updated.DefaultPermissions,
		"the response must report the requested set, not a stale role read-back")
}

// TestServiceSetSpaceDefaultPermissions_PublishesSpaceUpdated pins the event a default-permission
// change emits: exactly one space_updated carrying the space id, broadcast to the backing channel,
// and none when the requested set already matches the scheme the channel carries.
func TestServiceSetSpaceDefaultPermissions_PublishesSpaceUpdated(t *testing.T) {
	mockAPI := &plugintest.API{}
	sysadminID := mmmodel.NewId()
	// Registered before the harness so the response's own-permission projection resolves via
	// sysadmin, sidestepping the channel-member lookup this test does not otherwise stub.
	mockAPI.On("HasPermissionTo", sysadminID, mmmodel.PermissionManageSystem).Return(true)
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := mustCreateSpace(t, h.store, channelID)
	contribute, ok := model.DefaultPermissionsForSchemeName(mmmodel.SchemeNameSpaceContribute)
	require.True(t, ok)

	// The channel is seeded at the contribute preset, so resubmitting that set is a no-op.
	_, appErr := h.svc.SetSpaceDefaultPermissions(space, contribute, sysadminID)
	require.Nil(t, appErr)
	mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "space_updated", mock.Anything, mock.Anything)

	_, appErr = h.svc.SetSpaceDefaultPermissions(space, []string{mmmodel.PermissionCommentPage.Id}, sysadminID)
	require.Nil(t, appErr)
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_updated",
		map[string]any{"space_id": space.Id}, &mmmodel.WebsocketBroadcast{ChannelId: channelID})
	mockAPI.AssertNumberOfCalls(t, "PublishWebSocketEvent", 1)
}

// TestServiceSetSpaceDefaultPermissions_GuestLicenseDenialKeepsStatus pins the same guest
// entitlement refusal SetSpaceDefaultPermissions must surface when a non-preset set would
// create a pooled scheme with Docs' non-empty guest role.
func TestServiceSetSpaceDefaultPermissions_GuestLicenseDenialKeepsStatus(t *testing.T) {
	mockAPI := &plugintest.API{}
	sysadminID := mmmodel.NewId()
	mockAPI.On("HasPermissionTo", sysadminID, mmmodel.PermissionManageSystem).Return(true)
	mockAPI.On("GetOrCreatePluginChannelScheme", mock.Anything, mock.Anything, mock.Anything).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{StatusCode: http.StatusNotImplemented, Id: "app.scheme.plugin_scheme.guest_license.app_error"})
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := mustCreateSpace(t, h.store, channelID)

	_, appErr := h.svc.SetSpaceDefaultPermissions(space, []string{mmmodel.PermissionCreatePage.Id}, sysadminID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotImplemented, appErr.StatusCode)
	require.Equal(t, "app.scheme.plugin_scheme.guest_license.app_error", appErr.Id)
}

// TestServiceRestoreSpace_UnarchivesBackingChannel verifies a create→delete→restore round trip
// un-archives the backing channel and brings the space back live.
func TestServiceRestoreSpace_UnarchivesBackingChannel(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)
	mockAPI.On("GetChannelMembers", backingChannelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)
	mockAPI.On("RestoreChannel", backingChannelID).Return(nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Round Trip"}, userID, nil, nil)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(&space.Space))
	got, appErr := h.svc.RestoreSpace(space.Id)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "RestoreChannel", backingChannelID)
	require.Equal(t, space.Id, got.Id)
}

// TestServiceUpdateSpace verifies UpdateSpace applies only the supplied (non-nil) fields, preserves
// unspecified ones, can clear a field with an explicit empty string, and enforces the
// optimistic-lock baseline.
func TestServiceUpdateSpace(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	patched, appErr := h.svc.UpdateSpace(space, &model.SpacePatch{Title: mmmodel.NewPointer("New Title"), Description: mmmodel.NewPointer("New Desc")}, new(space.UpdateAt), false, "")
	require.Nil(t, appErr)
	require.Equal(t, "New Title", patched.Title)
	require.Equal(t, "New Desc", patched.Description)

	// A patch with only Icon leaves the previously-set Title/Description intact. The caller
	// passes its latest fetched record, mirroring the handler flow (membership gate re-fetches
	// the space on every request).
	patched2, appErr := h.svc.UpdateSpace(patched, &model.SpacePatch{Icon: mmmodel.NewPointer("icon-data")}, new(patched.UpdateAt), false, "")
	require.Nil(t, appErr)
	require.Equal(t, "New Title", patched2.Title, "unspecified fields are preserved")
	require.Equal(t, "New Desc", patched2.Description)
	require.Equal(t, "icon-data", patched2.Icon)

	// An explicit empty string clears a field (a nil field would leave it unchanged).
	patched3, appErr := h.svc.UpdateSpace(patched2, &model.SpacePatch{Description: mmmodel.NewPointer("")}, new(patched2.UpdateAt), false, "")
	require.Nil(t, appErr)
	require.Equal(t, "", patched3.Description, "an explicit empty string clears the field")
	require.Equal(t, "New Title", patched3.Title)

	// A stale baseline is rejected as a conflict unless force is set.
	_, appErr = h.svc.UpdateSpace(patched3, &model.SpacePatch{Title: mmmodel.NewPointer("Stale")}, new(space.UpdateAt), false, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	forced, appErr := h.svc.UpdateSpace(patched3, &model.SpacePatch{Title: mmmodel.NewPointer("Forced")}, new(space.UpdateAt), true, "")
	require.Nil(t, appErr)
	require.Equal(t, "Forced", forced.Title)

	// A whitespace-only title is rejected.
	_, appErr = h.svc.UpdateSpace(space, &model.SpacePatch{Title: mmmodel.NewPointer("   ")}, new(int64(0)), true, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.title_required.app_error", appErr.Id)

	// A title that exceeds SpaceTitleMaxRunes is rejected.
	longTitle := strings.Repeat("x", model.SpaceTitleMaxRunes+1)
	_, appErr = h.svc.UpdateSpace(space, &model.SpacePatch{Title: mmmodel.NewPointer(longTitle)}, new(int64(0)), true, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.title_too_long.app_error", appErr.Id)

	// A description that exceeds SpaceDescriptionMaxRunes is rejected with the documented error ID.
	longDesc := strings.Repeat("x", model.SpaceDescriptionMaxRunes+1)
	_, appErr = h.svc.UpdateSpace(space, &model.SpacePatch{Description: mmmodel.NewPointer(longDesc)}, new(int64(0)), true, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.description_too_long.app_error", appErr.Id)

	// An icon that exceeds SpaceIconMaxBytes is rejected with the documented error ID.
	largeIcon := strings.Repeat("i", model.SpaceIconMaxBytes+1)
	_, appErr = h.svc.UpdateSpace(space, &model.SpacePatch{Icon: mmmodel.NewPointer(largeIcon)}, new(int64(0)), true, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.icon_too_large.app_error", appErr.Id)
}

// TestServiceUpdateSpace_NoChangesRejected verifies an all-nil patch is rejected as a 400 rather
// than silently bumping UpdateAt (mirroring PagePatch's nothing-to-update guard).
func TestServiceUpdateSpace_NoChangesRejected(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.UpdateSpace(space, &model.SpacePatch{}, new(space.UpdateAt), false, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "model.space.patch.nothing_to_update.app_error", appErr.Id)

	// The space row is untouched: its UpdateAt did not advance.
	got, getErr := h.svc.GetSpace(space.Id)
	require.Nil(t, getErr)
	require.Equal(t, space.UpdateAt, got.UpdateAt)
}

// TestServiceUpdateSpace_ViewAccessGuards pins the two guards a ViewAccess change carries, neither
// of which the other UpdateSpace tests reach — they all patch through the store directly or omit
// ViewAccess entirely. A change is admin-only (holding manage on the space is not enough to flip it
// private), and it cannot ride a forced update, whose purpose is to override a stale optimistic-lock
// baseline rather than an authorization check.
func TestServiceUpdateSpace_ViewAccessGuards(t *testing.T) {
	privatePatch := func() *model.SpacePatch {
		return &model.SpacePatch{ViewAccess: mmmodel.NewPointer(model.ViewAccessPrivate)}
	}

	t.Run("force is rejected", func(t *testing.T) {
		h := openTestService(t)
		space := mustCreateSpace(t, h.store, mmmodel.NewId())

		_, appErr := h.svc.UpdateSpace(space, privatePatch(), nil, true, mmmodel.NewId())
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
		require.Equal(t, "app.space.update.view_access_force.app_error", appErr.Id)
	})

	t.Run("a non-admin member is refused", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		space := mustCreateSpace(t, h.store, mmmodel.NewId())

		// The harness grants an ordinary member's permissions and withholds admin_space.
		_, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, mmmodel.NewId())
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusForbidden, appErr.StatusCode)

		// The row is untouched: the escalation check runs before the write.
		got, getErr := h.svc.GetSpace(space.Id)
		require.Nil(t, getErr)
		require.Equal(t, model.ViewAccessOpen, got.ViewAccess)
	})

	t.Run("a space admin succeeds", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		userID := mmmodel.NewId()
		channelID := mmmodel.NewId()
		mockAPI.On("HasPermissionToChannel", userID, channelID, mmmodel.PermissionAdminSpace).Return(true).Maybe()
		h := openTestServiceWithAPI(t, mockAPI)
		// The ViewAccess gate reads SchemeAdmin from the master, so the grant above is not enough.
		testutil.MustAddChannelAdmin(t, h.db, channelID, userID)
		// A nil channel makes the backing-channel metadata sync a no-op.
		mockAPI.On("GetChannelOfType", mock.Anything, mock.Anything).Return((*mmmodel.Channel)(nil), nil).Maybe()
		// The flip prunes the members who joined through the open access; this space has none.
		mockAPI.On("DeleteChannelMember", channelID, mock.Anything).Return(nil).Maybe()
		space := mustCreateSpace(t, h.store, channelID)

		updated, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, userID)
		require.Nil(t, appErr)
		require.Equal(t, model.ViewAccessPrivate, updated.ViewAccess)
	})
}

// TestServiceUpdateSpace_PrivateFlipPrunesSelfJoined pins the open-to-private ordering and failure
// contract. A refused gate and a stale baseline both remove nobody; the auto-joined backing
// memberships are removed while the space is still open, and the privacy change commits only once
// the last of them is gone — so a removal failure leaves the space open and fails the request
// rather than committing a private space the failed member is still in.
func TestServiceUpdateSpace_PrivateFlipPrunesSelfJoined(t *testing.T) {
	privatePatch := func() *model.SpacePatch {
		return &model.SpacePatch{ViewAccess: mmmodel.NewPointer(model.ViewAccessPrivate)}
	}
	setup := func(t *testing.T, mockAPI *plugintest.API) (*testHarness, *model.Space, string, string) {
		t.Helper()
		adminID := mmmodel.NewId()
		memberID := mmmodel.NewId()
		channelID := mmmodel.NewId()
		h := openTestServiceWithAPI(t, mockAPI)
		testutil.MustAddChannelAdmin(t, h.db, channelID, adminID)
		space := mustCreateSpace(t, h.store, channelID)
		require.NoError(t, h.store.MarkAutoJoined(space.Id, memberID))
		return h, space, adminID, memberID
	}

	t.Run("stays open until the removal pass has finished", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h, space, adminID, memberID := setup(t, mockAPI)
		mockAPI.On("DeleteChannelMember", space.ChannelId, memberID).
			Run(func(mock.Arguments) {
				live, err := h.store.GetSpace(space.Id, false)
				require.NoError(t, err)
				require.Equal(t, model.ViewAccessOpen, live.ViewAccess,
					"view_access must still be open while the removal pass runs")
			}).
			Return(nil).
			Once()
		mockAPI.On("GetChannelOfType", space.ChannelId, mmmodel.ChannelTypeSpace).
			Return((*mmmodel.Channel)(nil), nil).
			Once()

		updated, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, adminID)
		require.Nil(t, appErr)
		require.Equal(t, model.ViewAccessPrivate, updated.ViewAccess)
		marked, err := h.store.GetAutoJoinedIDs(space.Id)
		require.NoError(t, err)
		require.Empty(t, marked)
	})

	t.Run("a deletion failure leaves the space open and fails the request", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h, space, adminID, memberID := setup(t, mockAPI)
		deleteErr := mmmodel.NewAppError("DeleteChannelMember", "test.delete.failed", nil, "", http.StatusInternalServerError)
		mockAPI.On("DeleteChannelMember", space.ChannelId, memberID).Return(deleteErr).Once()

		updated, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, adminID)
		require.Nil(t, updated)
		require.NotNil(t, appErr, "a member the pass could not remove must not be left inside a private space")
		require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)

		live, err := h.store.GetSpace(space.Id, false)
		require.NoError(t, err)
		require.Equal(t, model.ViewAccessOpen, live.ViewAccess)
		marked, err := h.store.GetAutoJoinedIDs(space.Id)
		require.NoError(t, err)
		require.Equal(t, []string{memberID}, marked,
			"the failed member stays marked so a later removal pass selects them again")
		mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "space_updated",
			map[string]any{"space_id": space.Id},
			&mmmodel.WebsocketBroadcast{ChannelId: space.ChannelId})
		mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "space_member_removed",
			map[string]any{"space_id": space.Id, "user_id": memberID},
			&mmmodel.WebsocketBroadcast{UserId: memberID})
	})

	t.Run("a refused gate writes and prunes nothing", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h, space, _, memberID := setup(t, mockAPI)

		// The harness grants an ordinary member's permissions and withholds admin_space, and the
		// stand-in tables carry no SchemeAdmin row for this acting user.
		_, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, mmmodel.NewId())
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusForbidden, appErr.StatusCode)

		live, err := h.store.GetSpace(space.Id, false)
		require.NoError(t, err)
		require.Equal(t, model.ViewAccessOpen, live.ViewAccess)
		mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, memberID)
		marked, err := h.store.GetAutoJoinedIDs(space.Id)
		require.NoError(t, err)
		require.Equal(t, []string{memberID}, marked)
	})

	t.Run("an already absent membership does not block the flip", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h, space, adminID, memberID := setup(t, mockAPI)
		notFound := mmmodel.NewAppError("DeleteChannelMember", "test.member.not_found", nil, "", http.StatusNotFound).
			Wrap(pluginapi.ErrNotFound)
		mockAPI.On("DeleteChannelMember", space.ChannelId, memberID).Return(notFound).Once()
		mockAPI.On("GetChannelOfType", space.ChannelId, mmmodel.ChannelTypeSpace).
			Return((*mmmodel.Channel)(nil), nil).
			Once()

		updated, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, adminID)
		require.Nil(t, appErr)
		require.Equal(t, model.ViewAccessPrivate, updated.ViewAccess)
		marked, err := h.store.GetAutoJoinedIDs(space.Id)
		require.NoError(t, err)
		require.Empty(t, marked, "a stale marker is cleared when core reports the membership absent")
	})

	t.Run("a join that races the removal pass yields a conflict and does not flip", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h, space, adminID, memberID := setup(t, mockAPI)
		latecomerID := mmmodel.NewId()
		mockAPI.On("DeleteChannelMember", space.ChannelId, memberID).
			Run(func(mock.Arguments) {
				// Stands in for a JoinOpenSpace that commits while the space is still open, which
				// is the whole window the second lock acquisition exists to close.
				require.NoError(t, h.store.MarkAutoJoined(space.Id, latecomerID))
			}).
			Return(nil).
			Once()

		updated, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, adminID)
		require.Nil(t, updated)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusConflict, appErr.StatusCode)

		live, err := h.store.GetSpace(space.Id, false)
		require.NoError(t, err)
		require.Equal(t, model.ViewAccessOpen, live.ViewAccess,
			"the flip must not commit while a self-joined member remains")
		marked, err := h.store.GetAutoJoinedIDs(space.Id)
		require.NoError(t, err)
		require.Equal(t, []string{latecomerID}, marked)
	})

	t.Run("a stale baseline prunes nobody", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h, space, adminID, memberID := setup(t, mockAPI)
		fresh, err := h.store.UpdateSpace(space.Id,
			&model.SpacePatch{Title: mmmodel.NewPointer("concurrent title")}, space.UpdateAt, false)
		require.NoError(t, err)

		updated, appErr := h.svc.UpdateSpace(space, privatePatch(), new(space.UpdateAt), false, adminID)
		require.Nil(t, updated)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusConflict, appErr.StatusCode)
		mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, memberID)

		live, err := h.store.GetSpace(space.Id, false)
		require.NoError(t, err)
		require.Equal(t, model.ViewAccessOpen, live.ViewAccess)
		require.Equal(t, fresh.UpdateAt, live.UpdateAt)
		marked, err := h.store.GetAutoJoinedIDs(space.Id)
		require.NoError(t, err)
		require.Equal(t, []string{memberID}, marked)
	})
}

// TestGetSpaceWithDeleted verifies that GetSpaceWithDeleted returns both live and soft-deleted
// spaces, unlike GetSpace which filters out deleted rows.
func TestGetSpaceWithDeleted(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", channelID).Return(nil)
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID, nil, nil)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(&space.Space))

	// GetSpace excludes deleted rows.
	_, appErr = h.svc.GetSpace(space.Id)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)

	// GetSpaceWithDeleted still returns the soft-deleted row.
	got, appErr := h.svc.GetSpaceWithDeleted(space.Id)
	require.Nil(t, appErr)
	require.Equal(t, space.Id, got.Id)
	require.NotZero(t, got.DeleteAt)
}

// TestResolveSpaceRead_MemberAdmitted verifies that a user who holds read_page on the space's
// backing channel is admitted as a member rather than via the open-space fall-through.
func TestResolveSpaceRead_MemberAdmitted(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	// The harness grants team read_space to everyone and read_page to true.
	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, userID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadViaMember, resolution)
}

// TestResolveSpaceRead_NonMemberDeniedOnPrivateSpace verifies that an active team member who holds
// no channel-scoped read_page is denied on a private space — the open-space fall-through is the
// only non-member admission, and it does not apply here.
func TestResolveSpaceRead_NonMemberDeniedOnPrivateSpace(t *testing.T) {
	mockAPI := &plugintest.API{}
	strangerID := mmmodel.NewId()
	// Registered before the harness so it takes precedence over StubDefaultSpacePermissions'
	// permissive catch-all: mock.Mock matches expectations in registration order.
	mockAPI.On("HasPermissionToChannel", strangerID, mock.Anything, mmmodel.PermissionReadPage).Return(false)
	h := openTestServiceWithAPI(t, mockAPI)

	// The harness grants team read_space to everyone, so the stranger clears the team gate and is
	// denied purely on the channel-scoped check.
	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), mmmodel.NewId())
	space.ViewAccess = model.ViewAccessPrivate

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, strangerID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution)
}

// createSpaceForMemberTests stands up a space with a mocked backing channel so the member-management
// failure paths can be exercised against a real store row.
func createSpaceForMemberTests(t *testing.T, h *testHarness, mockAPI *plugintest.API) (*model.Space, string) {
	t.Helper()
	teamID := mmmodel.NewId()
	creatorID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, creatorID).Return(&mmmodel.ChannelMember{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, creatorID, nil, nil)
	require.Nil(t, appErr)
	return &space.Space, creatorID
}

// TestServiceGetSpaceMembers_ListFails verifies that a failed member listing on the backing
// channel propagates as a 500 with the get_members error key.
func TestServiceGetSpaceMembers_ListFails(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(nil, &mmmodel.AppError{Id: "app.channel.get_members.app_error", StatusCode: http.StatusInternalServerError})

	_, _, appErr := h.svc.GetSpaceMembers(space, 0, 60, true)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.get_members.failed.app_error", appErr.Id)
}

// TestServiceSpaceDefaultPermissions_ChannelWithoutScheme covers the aggregate API's fail-closed
// contract: a backing channel carrying no direct scheme resolves to not-found rather than falling
// through to a team scheme.
//
// Reached through the roster's permission projection, which resolves the space's default set
// without a read gate in front of it.
func TestServiceSpaceDefaultPermissions_ChannelWithoutScheme(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	mockAPI.On("GetSchemeForChannel", channelID).
		Return(nil, nil, nil, nil, &mmmodel.AppError{StatusCode: http.StatusNotFound})

	_, _, appErr := h.svc.GetSpaceMembers(&model.Space{Id: mmmodel.NewId(), ChannelId: channelID}, 0, 60, true)
	require.NotNil(t, appErr)
}

// TestServiceAddSpaceMember_AddFails verifies that a failed channel-member add propagates as a 500
// with the add_member error key.
func TestServiceAddSpaceMember_AddFails(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	targetID := mmmodel.NewId()
	mockAPI.On("AddChannelMember", space.ChannelId, targetID).
		Return(nil, &mmmodel.AppError{Id: "app.channel.add_member.app_error", StatusCode: http.StatusInternalServerError})

	_, appErr := h.svc.AddSpaceMember(space, targetID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.add_member.failed.app_error", appErr.Id)
}

// TestServiceRemoveSpaceMember_RemoveFails verifies that a failed channel-member removal
// propagates as a 500 with the remove_member error key.
func TestServiceRemoveSpaceMember_RemoveFails(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	targetID := mmmodel.NewId()
	// The target-existence resolve and the last-member guard both read membership from the master
	// DB; seed the target's own row plus another active member so the removal proceeds to the
	// failing DeleteChannelMember call.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, targetID)
	otherID := mmmodel.NewId()
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, otherID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, otherID, 0)
	mockAPI.On("DeleteChannelMember", space.ChannelId, targetID).
		Return(&mmmodel.AppError{Id: "app.channel.remove_member.app_error", StatusCode: http.StatusInternalServerError})

	appErr := h.svc.RemoveSpaceMember(space, targetID, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.remove_member.failed.app_error", appErr.Id)
}

// TestServiceRemoveSpaceMember_SelfNonMemberOnOpenSpaceIs404 covers a non-member's self-removal
// from an open space. The read gate admits non-members to an open space by design, so the caller
// can already see it exists and there is nothing left to hide: the absent membership reports as a
// plain 404 rather than the existence-hiding 403, which would misreport a no-op as an
// authorization failure.
func TestServiceRemoveSpaceMember_SelfNonMemberOnOpenSpaceIs404(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)
	require.Equal(t, model.ViewAccessOpen, space.ViewAccess, "fixture must be open for this case")

	selfID := mmmodel.NewId()

	appErr := h.svc.RemoveSpaceMember(space, selfID, selfID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
	require.Equal(t, "app.space.member.user_not_found.app_error", appErr.Id)
}

// TestServiceRemoveSpaceMember_SelfNonMemberOnPrivateSpaceIs403 is the other half of the split: on
// a private space the same caller is a non-member the read gate denies, so reporting the absent
// membership as 404 would confirm the space exists to someone who cannot read it. They get the
// shared existence-hiding 403 instead.
func TestServiceRemoveSpaceMember_SelfNonMemberOnPrivateSpaceIs403(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	private := model.ViewAccessPrivate
	updated, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &private}, space.UpdateAt, false)
	require.NoError(t, err)

	selfID := mmmodel.NewId()

	appErr := h.svc.RemoveSpaceMember(updated, selfID, selfID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id)
}

// TestServiceRemoveSpaceMember_LastMemberRejected verifies the sole remaining member cannot be
// removed: membership is the only gate on every space and page route, so a memberless space
// would be permanently unreachable through the plugin API.
func TestServiceRemoveSpaceMember_LastMemberRejected(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	soleID := mmmodel.NewId()
	// The sole membership lives on the master DB; no other authorized member exists.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, soleID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, soleID, 0)

	appErr := h.svc.RemoveSpaceMember(space, soleID, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
	require.Equal(t, "app.space.remove_member.last_member.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, soleID)
}

// TestServiceRemoveSpaceMember_LastActiveMemberRejected verifies the guard counts only members
// who can still reach the space: a former team member's lingering channel-member row must not
// license removing the last active member, which would strand the space behind members who all
// fail the team half of the access gate.
func TestServiceRemoveSpaceMember_LastActiveMemberRejected(t *testing.T) {
	mockAPI := &plugintest.API{}
	activeID := mmmodel.NewId()
	formerID := mmmodel.NewId()
	// Core's team-permission filter is what reports the departure: registered before the harness's
	// admit-everyone default, it drops the former member from the audience.
	mockAPI.On("FilterUsersWithTeamPermission", mock.Anything, mock.Anything, mmmodel.PermissionReadSpace).
		Return(func(_ string, ids []string, _ *mmmodel.Permission) ([]string, *mmmodel.AppError) {
			return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == formerID }), nil
		})
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	// Master-DB membership: the only other row belongs to a member who already left the team.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, activeID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, activeID, 0)
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, formerID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, formerID, 1)

	appErr := h.svc.RemoveSpaceMember(space, activeID, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
	require.Equal(t, "app.space.remove_member.last_member.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, activeID)
}

// TestServiceRemoveSpaceMember_FormerTeamMemberRemovable verifies the complementary direction:
// a stale member who already left the team can be removed while an active member remains.
func TestServiceRemoveSpaceMember_FormerTeamMemberRemovable(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	activeID := mmmodel.NewId()
	formerID := mmmodel.NewId()
	// Master-DB membership: an active member remains, and the target already left the team.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, activeID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, activeID, 0)
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, formerID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, formerID, 1)
	mockAPI.On("DeleteChannelMember", space.ChannelId, formerID).Return(nil)

	appErr := h.svc.RemoveSpaceMember(space, formerID, "")
	require.Nil(t, appErr)
	mockAPI.AssertCalled(t, "DeleteChannelMember", space.ChannelId, formerID)
}

// TestServiceAddSpaceMember_FormerTeamMemberRejected verifies AddSpaceMember refuses a target who
// left the space's team before any backing-channel side effect: such a member could never pass
// the access gate, and admitting them would let the last-member guard count an unreachable user.
func TestServiceAddSpaceMember_FormerTeamMemberRejected(t *testing.T) {
	mockAPI := &plugintest.API{}
	targetID := mmmodel.NewId()
	// Registered before the harness's read_space catch-all because testify matches expectations
	// first-registered-first; keyed on targetID so the creator's grant stays on the catch-all.
	mockAPI.On("HasPermissionToTeam", targetID, mock.AnythingOfType("string"), mmmodel.PermissionReadSpace).Return(false)
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	_, appErr := h.svc.AddSpaceMember(space, targetID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.member.not_team_member.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "AddChannelMember", space.ChannelId, targetID)
}

// TestServiceMovePage_PublishesMovedEvent verifies the page_moved event carries the old and new
// parent ids so clients can invalidate exactly the two affected child lists without a full reload.
func TestServiceMovePage_PublishesMovedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	moved, appErr := h.svc.MovePage(page.Id, space.Id, &parent.Id, nil, new(page.UpdateAt), false, userID)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_moved", map[string]any{
		"page_id":       page.Id,
		"space_id":      space.Id,
		"old_parent_id": "",
		"new_parent_id": parent.Id,
	}, &mmmodel.WebsocketBroadcast{ChannelId: moved.ChannelId})
}
