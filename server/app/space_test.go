// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"net/http"
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
func openTestServiceWithAPI(t *testing.T, mockAPI *plugintest.API) *testHarness {
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
	// CreateSpace requires the creator to be a team member; default every test to a live
	// membership so tests that aren't specifically exercising the rejection don't need their own
	// stub. TestServiceCreateSpace_NotTeamMember overrides this to exercise the rejection path.
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil).Maybe()
	// plugintest flattens a log call's variadic pairs into the mock's argument list, so a stub only
	// matches calls with exactly that many arguments. Cover each shape the service emits: LogWarn
	// with message plus two key/value pairs (ResolveSpaceRead/GetSpacesForTeam client-not-wired
	// denials), LogWarn with message plus three pairs (DeleteSpace, restoreSpaceChannel), and
	// LogError with message plus four (archiveOrphanChannel).
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// LogError shapes: message plus two pairs (the custom-scheme retire failures), plus three
	// pairs (UpdateSpace's channel-metadata sync failure) and plus four (archiveOrphanChannel).
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// CreateSpace assigns the creator SchemeAdmin via the scheme's resolved role-name string; no
	// test asserts the exact roles argument, so a wildcard catch-all covers every create.
	mockAPI.On("UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	h.svc = app.New(h.store, &client.Log, client)
	return h
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

// TestServiceCreateSpace_ChannelIdRejected verifies a caller-supplied ChannelId is rejected
// before any backing-channel side effect.
func TestServiceCreateSpace_ChannelIdRejected(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	space := &model.Space{ChannelId: mmmodel.NewId(), TeamId: mmmodel.NewId(), Title: "Bad Space"}

	_, appErr := h.svc.CreateSpace(space, mmmodel.NewId(), nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	mockAPI.AssertNotCalled(t, "CreateChannel")
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
	mockAPI.AssertNotCalled(t, "AddChannelMember")
	mockAPI.AssertNotCalled(t, "DeleteChannel")
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
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)

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
			mockAPI.AssertNotCalled(t, "CreateChannel")
		})
	}

	t.Run("nil space", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)

		_, appErr := h.svc.CreateSpace(nil, mmmodel.NewId(), nil, nil)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		mockAPI.AssertNotCalled(t, "CreateChannel")
	})
}

// TestServiceCreateSpace_NotTeamMember verifies CreateSpace rejects a creator who is not a member
// of the target team, before any backing-channel side effect — otherwise any authenticated user
// could create a real channel in a team they don't belong to.
func TestServiceCreateSpace_NotTeamMember(t *testing.T) {
	h := openTestService(t)
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{}).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	h.svc = app.New(h.store, &client.Log, client)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(nil, &mmmodel.AppError{Message: "not a member", StatusCode: http.StatusNotFound})

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Not Allowed"}, userID, nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.create.not_team_member.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "CreateChannel")
}

// TestServiceCreateSpace_FormerTeamMemberBlocked verifies that a user who left the team is
// rejected: core keeps removed team members as rows with DeleteAt set, and GetTeamMember returns
// such a row without error, so the gate must inspect DeleteAt rather than rely on a not-found.
func TestServiceCreateSpace_FormerTeamMemberBlocked(t *testing.T) {
	h := openTestService(t)
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{}).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	h.svc = app.New(h.store, &client.Log, client)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: userID, DeleteAt: 1}, nil)

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Not Allowed"}, userID, nil, nil)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.create.not_team_member.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "CreateChannel")
}

// TestResolveSpaceRead_FormerTeamMemberDenied verifies that access to a team's space ends with
// team membership: leaving a team does not remove the user from the space's backing channel, so a
// former team member still holds a ChannelMember row — the team gate (which must read DeleteAt,
// since core returns removed memberships without error) is what blocks them.
func TestResolveSpaceRead_FormerTeamMemberDenied(t *testing.T) {
	mockAPI := &plugintest.API{}
	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	// Registered before the harness, whose GetTeamMember catch-all returns an active membership:
	// mock.Mock matches expectations in registration order.
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: userID, DeleteAt: 1}, nil)
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), teamID)

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
	mustCreateSpace(t, h.store, h.db, collisionChannelID)

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
	mustCreateSpace(t, h.store, h.db, collisionChannelID)

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

// stubCustomSchemeCreate wires the mock calls a non-preset default-capability set needs: a
// CreateScheme returning a scheme with three generated roles, those roles registered so a channel
// repointed at the scheme resolves them, and a PatchRole that applies the plugin's writes. Returns
// the new scheme's id.
func stubCustomSchemeCreate(t *testing.T, mockAPI *plugintest.API) string {
	t.Helper()
	testutil.StubPooledSchemeMiss(mockAPI)
	schemeID := mmmodel.NewId()
	userRole, adminRole, guestRole := "custom_user_role", "custom_admin_role", "custom_guest_role"
	mockAPI.On("CreateScheme", mock.AnythingOfType("*model.Scheme")).Return(&mmmodel.Scheme{
		Id:                      schemeID,
		Name:                    model.SharedSchemeNamePrefix + mmmodel.NewId(),
		Scope:                   mmmodel.SchemeScopeChannel,
		DefaultChannelUserRole:  userRole,
		DefaultChannelAdminRole: adminRole,
		DefaultChannelGuestRole: guestRole,
	}, nil)
	testutil.RegisterSchemeRoles(schemeID, guestRole, userRole, adminRole)
	testutil.StubRole(mockAPI, userRole, nil)
	testutil.StubRole(mockAPI, adminRole, nil)
	testutil.StubRole(mockAPI, guestRole, nil)
	testutil.StubPatchRole(mockAPI)
	return schemeID
}

// TestServiceCreateSpace_PooledSchemeSurvivesAbandon covers the compensating path a non-preset
// default-capability set takes when a later create step fails: the doomed backing channel is
// archived, but the pooled scheme it pointed at is left alone. The pool is keyed by the capability
// set, so that scheme is not this space's to delete — another space may already be resolving to it,
// and deleting it would strip their members' capabilities.
func TestServiceCreateSpace_PooledSchemeSurvivesAbandon(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	channel := testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	pooledSchemeID := stubCustomSchemeCreate(t, mockAPI)
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
	mockAPI.AssertNotCalled(t, "DeleteScheme", mock.Anything)
	require.NotEmpty(t, pooledSchemeID)
}

// TestServiceCreateSpace_PresetSchemeSurvivesAbandon is the negative half of the case above: a
// preset scheme is shared by every space using it, so a failed create must archive the channel
// without ever deleting the scheme.
func TestServiceCreateSpace_PresetSchemeSurvivesAbandon(t *testing.T) {
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

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed Preset"}, userID, nil, nil)
	require.NotNil(t, appErr)
	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)
	mockAPI.AssertNotCalled(t, "DeleteScheme", mock.Anything)
}

// TestServiceCreateSpace_CustomSchemeConfiguredAfterChannelAttach verifies the ordering core
// requires: the role writes carrying space permissions land only after the backing channel already
// points at the scheme, since a caller-chosen scheme name is not accepted as proof of space scope.
func TestServiceCreateSpace_CustomSchemeConfiguredAfterChannelAttach(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	channel := testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)

	// Registered before stubCustomSchemeCreate's own catch-all PatchRole so this one matches first
	// (mock.Mock matches in registration order) and can record the permission set per role.
	channelAttached := false
	patched := map[string][]string{}
	mockAPI.On("PatchRole", mock.AnythingOfType("string"), mock.AnythingOfType("*model.RolePatch")).
		Run(func(args mock.Arguments) {
			require.True(t, channelAttached, "roles must be patched only after the channel attaches the scheme")
			roleID, ok := args.Get(0).(string)
			require.True(t, ok)
			roleName, ok := testutil.StubbedRoleName(roleID)
			require.True(t, ok, "PatchRole called with an unregistered role id %q", roleID)
			patch, ok := args.Get(1).(*mmmodel.RolePatch)
			require.True(t, ok)
			require.NotNil(t, patch.Permissions)
			patched[roleName] = *patch.Permissions
		}).
		Return(&mmmodel.Role{}, nil)

	customSchemeID := stubCustomSchemeCreate(t, mockAPI)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Run(func(args mock.Arguments) {
			created, ok := args.Get(0).(*mmmodel.Channel)
			require.True(t, ok)
			require.NotNil(t, created.SchemeId)
			require.Equal(t, customSchemeID, *created.SchemeId)
			channel.SchemeId = created.SchemeId
			channelAttached = true
		}).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space, appErr := h.svc.CreateSpace(
		&model.Space{TeamId: teamID, Title: "Custom Caps"}, userID, &[]string{"create_page"}, nil)
	require.Nil(t, appErr)
	require.NotNil(t, space)

	// Each generated role gets its own set: the user role the requested capabilities plus the
	// baseline read, the guest role read alone, and the admin role the full space-admin set.
	require.ElementsMatch(t, []string{"read_page", "create_page"}, patched["custom_user_role"])
	require.ElementsMatch(t, []string{"read_page"}, patched["custom_guest_role"])
	require.ElementsMatch(t, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions), patched["custom_admin_role"])
}

// stubCustomSchemeCreateFailingPatch is stubCustomSchemeCreate with a PatchRole that always fails,
// so the configure step a freshly created custom scheme needs cannot complete. Returns the new
// scheme's id.
func stubCustomSchemeCreateFailingPatch(t *testing.T, mockAPI *plugintest.API) string {
	t.Helper()
	testutil.StubPooledSchemeMiss(mockAPI)
	schemeID := mmmodel.NewId()
	userRole, adminRole, guestRole := "unconfigurable_user_role", "unconfigurable_admin_role", "unconfigurable_guest_role"
	mockAPI.On("CreateScheme", mock.AnythingOfType("*model.Scheme")).Return(&mmmodel.Scheme{
		Id:                      schemeID,
		Name:                    model.SharedSchemeNamePrefix + mmmodel.NewId(),
		Scope:                   mmmodel.SchemeScopeChannel,
		DefaultChannelUserRole:  userRole,
		DefaultChannelAdminRole: adminRole,
		DefaultChannelGuestRole: guestRole,
	}, nil)
	testutil.RegisterSchemeRoles(schemeID, guestRole, userRole, adminRole)
	testutil.StubRole(mockAPI, userRole, nil)
	testutil.StubRole(mockAPI, adminRole, nil)
	testutil.StubRole(mockAPI, guestRole, nil)
	mockAPI.On("PatchRole", mock.AnythingOfType("string"), mock.AnythingOfType("*model.RolePatch")).
		Return(nil, &mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	return schemeID
}

// TestServiceCreateSpace_PooledSchemeConfigureFailureAbandons covers the CreateSpace branch that
// runs when the role writes fail after the backing channel already carries the pooled scheme: the
// create fails and the channel is archived, while the pooled scheme stays — it is shared, so a
// later space resolving to the same capability set reconfigures it. The creator is never added,
// since configuration precedes that.
func TestServiceCreateSpace_PooledSchemeConfigureFailureAbandons(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	channel := testutil.MustSeedChannelScheme(t, mockAPI, backingChannelID, mmmodel.SchemeNameSpaceContribute)
	customSchemeID := stubCustomSchemeCreateFailingPatch(t, mockAPI)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Run(func(args mock.Arguments) {
			created, ok := args.Get(0).(*mmmodel.Channel)
			require.True(t, ok)
			require.NotNil(t, created.SchemeId)
			channel.SchemeId = created.SchemeId
		}).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)

	_, appErr := h.svc.CreateSpace(
		&model.Space{TeamId: teamID, Title: "Unconfigurable Pooled"}, userID, &[]string{"create_page"}, nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.create.scheme_configure_failed.app_error", appErr.Id)

	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)
	mockAPI.AssertNotCalled(t, "DeleteScheme", mock.Anything)
	require.NotEmpty(t, customSchemeID)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)

	spaces, err := h.store.GetSpacesForTeam(teamID, userID, false, 0, 10)
	require.NoError(t, err)
	require.Empty(t, spaces)
}

// TestServiceSetSpaceDefaultCapabilities_ConfigureFailureRollsBack covers the repoint-then-configure
// failure branch: a space whose newly pooled scheme cannot be configured must be put back on its
// previous scheme rather than left on one whose roles may still carry core's default channel
// baseline. The pooled scheme itself is never deleted — it is shared, not this space's to retire.
func TestServiceSetSpaceDefaultCapabilities_ConfigureFailureRollsBack(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	channel := testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	pooledSchemeID := stubCustomSchemeCreateFailingPatch(t, mockAPI)

	space := mustCreateSpace(t, h.store, h.db, channelID)
	_, appErr := h.svc.SetSpaceDefaultCapabilities(space, []string{"create_page"}, mmmodel.NewId())

	require.NotNil(t, appErr)
	require.Equal(t, "app.space.default_capabilities.scheme_configure_failed.app_error", appErr.Id)
	require.NotNil(t, channel.SchemeId)
	require.Equal(t, testutil.PresetSchemeID(mmmodel.SchemeNameSpaceContribute), *channel.SchemeId,
		"the channel must be repointed back at the scheme it started on")
	require.NotEqual(t, pooledSchemeID, *channel.SchemeId)
	mockAPI.AssertNotCalled(t, "DeleteScheme", mock.Anything)
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
	space := mustCreateSpace(t, h.store, h.db, mmmodel.NewId())

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
	space := mustCreateSpace(t, h.store, h.db, mmmodel.NewId())

	_, appErr := h.svc.UpdateSpace(space, &model.SpacePatch{}, new(space.UpdateAt), false, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "model.space.patch.nothing_to_update.app_error", appErr.Id)

	// The space row is untouched: its UpdateAt did not advance.
	got, getErr := h.svc.GetSpace(space.Id)
	require.Nil(t, getErr)
	require.Equal(t, space.UpdateAt, got.UpdateAt)
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

	// The harness stubs GetTeamMember to an active membership and read_page to true.
	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), teamID)

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

	// The harness stubs GetTeamMember to an active membership, so the stranger clears the team
	// gate and is denied purely on the channel-scoped check.
	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), mmmodel.NewId())
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
// channel propagates as a 500 with the list_members error key.
func TestServiceGetSpaceMembers_ListFails(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(nil, &mmmodel.AppError{Id: "app.channel.get_members.app_error", StatusCode: http.StatusInternalServerError})

	_, _, appErr := h.svc.GetSpaceMembers(space, 0, 60)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.list_members.failed.app_error", appErr.Id)
}

// TestServiceDefaultRolesGrantPermission_ChannelWithoutScheme covers schemeRolesFromChannel's
// fail-closed branch: a backing channel carrying no scheme (a space that lost its scheme) resolves
// to not-found, which DefaultRolesGrantPermission maps to "not granted" — never a silent
// fall-through to the team scheme's channel roles, and never a wrong-role grant. The lookup must
// short-circuit before reaching GetSchemeRolesForChannel.
func TestServiceDefaultRolesGrantPermission_ChannelWithoutScheme(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	// The mock never sets a SchemeId, standing in for a space whose backing channel carries none.
	mockAPI.On("GetChannelOfType", channelID, mmmodel.ChannelTypeSpace).
		Return(&mmmodel.Channel{Id: channelID, Type: mmmodel.ChannelTypeSpace}, nil)

	granted, err := h.svc.DefaultRolesGrantPermission(&model.Space{ChannelId: channelID}, mmmodel.PermissionCreatePage)
	require.NoError(t, err)
	require.False(t, granted)
	mockAPI.AssertNotCalled(t, "GetSchemeRolesForChannel", mock.Anything)
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
	// The target-existence resolve runs before the last-member/last-admin guards.
	mockAPI.On("GetChannelMember", space.ChannelId, targetID).Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: targetID}, nil)
	// The last-member guard scans the member list before removing; report another (active,
	// via the default GetTeamMember stub) member so the removal proceeds to the failing
	// DeleteChannelMember call.
	mockAPI.On("GetChannelMembers", space.ChannelId, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: targetID}, {ChannelId: space.ChannelId, UserId: mmmodel.NewId()}}, nil)
	mockAPI.On("DeleteChannelMember", space.ChannelId, targetID).
		Return(&mmmodel.AppError{Id: "app.channel.remove_member.app_error", StatusCode: http.StatusInternalServerError})

	appErr := h.svc.RemoveSpaceMember(space, targetID, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.remove_member.failed.app_error", appErr.Id)
}

// TestServiceRemoveSpaceMember_LastMemberRejected verifies the sole remaining member cannot be
// removed: membership is the only gate on every space and page route, so a memberless space
// would be permanently unreachable through the plugin API.
func TestServiceRemoveSpaceMember_LastMemberRejected(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	soleID := mmmodel.NewId()
	mockAPI.On("GetChannelMember", space.ChannelId, soleID).Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: soleID}, nil)
	mockAPI.On("GetChannelMembers", space.ChannelId, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: soleID}}, nil)

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
	// testify matches expectations first-registered-first, so the former-member stub must be
	// registered before the harness's catch-all active-member GetTeamMember stub; keying it on
	// formerID keeps every other user (the creator, activeID) on the catch-all.
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), formerID).
		Return(&mmmodel.TeamMember{UserId: formerID, DeleteAt: 1}, nil)
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	mockAPI.On("GetChannelMember", space.ChannelId, activeID).Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: activeID}, nil)
	mockAPI.On("GetChannelMembers", space.ChannelId, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: activeID}, {ChannelId: space.ChannelId, UserId: formerID}}, nil)

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
	mockAPI.On("GetChannelMember", space.ChannelId, formerID).Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: formerID}, nil)
	mockAPI.On("GetChannelMembers", space.ChannelId, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: activeID}, {ChannelId: space.ChannelId, UserId: formerID}}, nil)
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
	// Registered before the harness's catch-all active-member GetTeamMember stub because
	// testify matches expectations first-registered-first; keyed on targetID so the creator's
	// lookup stays on the catch-all.
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), targetID).
		Return(&mmmodel.TeamMember{UserId: targetID, DeleteAt: 1}, nil)
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
	space := mustCreateSpace(t, h.store, h.db, channelID)
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
