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
	mockAPI.On("GetConfig").Return(&mmmodel.Config{}).Maybe()
	// DeleteSpace/RestoreSpace log a debug line ("Deleting space"/"Restoring space", "space_id",
	// spaceID) before touching the store, mirroring CreatePage/DeletePage/RestorePage's convention.
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	// CreateSpace requires the creator to be a team member; default every test to a live
	// membership so tests that aren't specifically exercising the rejection don't need their own
	// stub. TestServiceCreateSpace_NotTeamMember overrides this to exercise the rejection path.
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil).Maybe()
	// plugintest flattens LogWarn's variadic pairs into the mock's argument list, so a stub only
	// matches calls with exactly that many arguments. Cover both shapes the service emits: message
	// plus three key/value pairs (DeleteSpace, restoreSpaceChannel) and plus four
	// (archiveOrphanChannel).
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
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

	mockAPI.On("CreateChannel", mock.MatchedBy(func(ch *mmmodel.Channel) bool {
		return ch.Type == mmmodel.ChannelTypeSpace && ch.TeamId == teamID
	})).Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space := &model.Space{TeamId: teamID, Title: "Test Space"}

	saved, appErr := h.svc.CreateSpace(space, userID)
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

	_, appErr := h.svc.CreateSpace(space, mmmodel.NewId())
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

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: mmmodel.NewId(), Title: "Doomed"}, mmmodel.NewId())
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.create.backing_channel_failed.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "AddChannelMember")
	mockAPI.AssertNotCalled(t, "DeleteChannel")
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

			_, appErr := h.svc.CreateSpace(space, mmmodel.NewId())
			require.NotNil(t, appErr)
			require.Equal(t, 400, appErr.StatusCode)
			mockAPI.AssertNotCalled(t, "CreateChannel")
		})
	}

	t.Run("nil space", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)

		_, appErr := h.svc.CreateSpace(nil, mmmodel.NewId())
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

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Not Allowed"}, userID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.create.not_team_member.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "CreateChannel")
}

// TestServiceDeleteSpace_ArchivesBackingChannel verifies DeleteSpace archives the space's
// backing channel after the soft-delete.
func TestServiceDeleteSpace_ArchivesBackingChannel(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed"}, userID)
	require.Nil(t, appErr)

	require.Nil(t, h.svc.DeleteSpace(space.Id))
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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed"}, userID)
	require.Nil(t, appErr)

	require.Nil(t, h.svc.DeleteSpace(space.Id), "DeleteSpace must succeed even though the channel archive fails")
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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)
	mockAPI.On("RestoreChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	// The channel is still archived, so the un-archive genuinely failed and must propagate.
	mockAPI.On("GetSpaceBackingChannel", backingChannelID).
		Return(&mmmodel.Channel{Id: backingChannelID, Type: mmmodel.ChannelTypeSpace, DeleteAt: 100}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Round Trip"}, userID)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(space.Id))

	_, appErr = h.svc.RestoreSpace(space.Id)
	require.NotNil(t, appErr, "RestoreSpace must fail when the channel un-archive fails")
	require.Equal(t, "app.space.restore.channel_restore_failed.app_error", appErr.Id)
	mockAPI.AssertCalled(t, "RestoreChannel", backingChannelID)

	// The space row itself is left restored even though the channel-restore call failed.
	got, appErr := h.svc.GetSpace(space.Id)
	require.Nil(t, appErr)
	require.Zero(t, got.DeleteAt)
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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	// The archive fails transiently: the space row is soft-deleted, the channel stays live.
	mockAPI.On("DeleteChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "transient", StatusCode: http.StatusInternalServerError})
	// Core rejects un-archiving a channel that was never archived.
	mockAPI.On("RestoreChannel", backingChannelID).
		Return(&mmmodel.AppError{Message: "channel is not archived", StatusCode: http.StatusBadRequest})
	mockAPI.On("GetSpaceBackingChannel", backingChannelID).
		Return(&mmmodel.Channel{Id: backingChannelID, Type: mmmodel.ChannelTypeSpace, DeleteAt: 0}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Never Archived"}, userID)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(space.Id))

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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: collisionChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", collisionChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", collisionChannelID).Return(nil)

	space := &model.Space{TeamId: teamID, Title: "Doomed Space"}

	_, appErr := h.svc.CreateSpace(space, userID)
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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: collisionChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", collisionChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", collisionChannelID).
		Return(&mmmodel.AppError{Message: "archive also failed", StatusCode: http.StatusInternalServerError})

	space := &model.Space{TeamId: teamID, Title: "Doomed Space"}

	_, appErr := h.svc.CreateSpace(space, userID)
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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).
		Return(nil, &mmmodel.AppError{Message: "boom", StatusCode: http.StatusInternalServerError})
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)

	_, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Doomed"}, userID)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.create.add_member_failed.app_error", appErr.Id)
	// The orphan channel is removed; no space row is persisted for the team.
	mockAPI.AssertCalled(t, "DeleteChannel", backingChannelID)

	spaces, listErr := h.svc.GetSpacesForTeam(teamID, "", 0, 60)
	require.Nil(t, listErr)
	require.Empty(t, spaces)
}

// TestServiceRestoreSpace_UnarchivesBackingChannel verifies a create→delete→restore round trip
// un-archives the backing channel and brings the space back live.
func TestServiceRestoreSpace_UnarchivesBackingChannel(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	backingChannelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", backingChannelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", backingChannelID).Return(nil)
	mockAPI.On("RestoreChannel", backingChannelID).Return(nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Round Trip"}, userID)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(space.Id))
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

	patched, appErr := h.svc.UpdateSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("New Title"), Description: mmmodel.NewPointer("New Desc")}, space.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, "New Title", patched.Title)
	require.Equal(t, "New Desc", patched.Description)

	// A patch with only Icon leaves the previously-set Title/Description intact.
	patched2, appErr := h.svc.UpdateSpace(space.Id, &model.SpacePatch{Icon: mmmodel.NewPointer("icon-data")}, patched.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, "New Title", patched2.Title, "unspecified fields are preserved")
	require.Equal(t, "New Desc", patched2.Description)
	require.Equal(t, "icon-data", patched2.Icon)

	// An explicit empty string clears a field (a nil field would leave it unchanged).
	patched3, appErr := h.svc.UpdateSpace(space.Id, &model.SpacePatch{Description: mmmodel.NewPointer("")}, patched2.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, "", patched3.Description, "an explicit empty string clears the field")
	require.Equal(t, "New Title", patched3.Title)

	// A stale baseline is rejected as a conflict unless force is set.
	_, appErr = h.svc.UpdateSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("Stale")}, space.UpdateAt, false)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	forced, appErr := h.svc.UpdateSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("Forced")}, space.UpdateAt, true)
	require.Nil(t, appErr)
	require.Equal(t, "Forced", forced.Title)

	// A whitespace-only title is rejected.
	_, appErr = h.svc.UpdateSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("   ")}, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.title_required.app_error", appErr.Id)

	// A title that exceeds SpaceTitleMaxRunes is rejected.
	longTitle := strings.Repeat("x", model.SpaceTitleMaxRunes+1)
	_, appErr = h.svc.UpdateSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer(longTitle)}, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.title_too_long.app_error", appErr.Id)

	// A description that exceeds SpaceDescriptionMaxRunes is rejected with the documented error ID.
	longDesc := strings.Repeat("x", model.SpaceDescriptionMaxRunes+1)
	_, appErr = h.svc.UpdateSpace(space.Id, &model.SpacePatch{Description: mmmodel.NewPointer(longDesc)}, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.description_too_long.app_error", appErr.Id)

	// An icon that exceeds SpaceIconMaxBytes is rejected with the documented error ID.
	largeIcon := strings.Repeat("i", model.SpaceIconMaxBytes+1)
	_, appErr = h.svc.UpdateSpace(space.Id, &model.SpacePatch{Icon: mmmodel.NewPointer(largeIcon)}, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.shared.icon_too_large.app_error", appErr.Id)
}

// TestServiceUpdateSpace_NoChangesRejected verifies an all-nil patch is rejected as a 400 rather
// than silently bumping UpdateAt (mirroring PagePatch's nothing-to-update guard).
func TestServiceUpdateSpace_NoChangesRejected(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.UpdateSpace(space.Id, &model.SpacePatch{}, space.UpdateAt, false)
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

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", channelID).Return(nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(space.Id))

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

// TestCheckSpaceMembership_MemberAllowed verifies that a user who is a member of the
// space's backing channel is allowed through.
func TestCheckSpaceMembership_MemberAllowed(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID)
	require.Nil(t, appErr)

	mockAPI.On("GetChannelMember", space.ChannelId, userID).Return(&mmmodel.ChannelMember{}, nil)

	_, appErr = h.svc.CheckSpaceMembership(space.Id, userID, false)
	require.Nil(t, appErr)
}

// TestCheckSpaceMembership_NonMemberBlocked verifies that a user who is not a member of the
// space's backing channel receives a 403 Forbidden.
func TestCheckSpaceMembership_NonMemberBlocked(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID)
	require.Nil(t, appErr)

	strangerID := mmmodel.NewId()
	mockAPI.On("GetChannelMember", space.ChannelId, strangerID).
		Return(nil, &mmmodel.AppError{StatusCode: http.StatusNotFound})

	_, appErr = h.svc.CheckSpaceMembership(space.Id, strangerID, false)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id)
}

// TestCheckSpaceMembership_SystemCallerSkipsCheck verifies that an empty userID (system
// caller) bypasses the membership check without any API call.
func TestCheckSpaceMembership_SystemCallerSkipsCheck(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID)
	require.Nil(t, appErr)

	// Empty userID = system caller; no GetChannelMember call must be made.
	_, appErr = h.svc.CheckSpaceMembership(space.Id, "", false)
	require.Nil(t, appErr)
}

// TestCheckSpaceMembership_IncludeDeleted verifies that includeDeleted=true reaches a
// soft-deleted space for the membership check, while includeDeleted=false returns 404.
func TestCheckSpaceMembership_IncludeDeleted(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("DeleteChannel", channelID).Return(nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID)
	require.Nil(t, appErr)
	require.Nil(t, h.svc.DeleteSpace(space.Id))

	// includeDeleted=false → GetSpace returns 404, which CheckSpaceMembership converts to 403
	// to prevent existence probing by non-members.
	_, appErr = h.svc.CheckSpaceMembership(space.Id, userID, false)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)

	// includeDeleted=true → GetSpaceWithDeleted finds the space; membership check proceeds.
	mockAPI.On("GetChannelMember", space.ChannelId, userID).Return(&mmmodel.ChannelMember{}, nil)
	_, appErr = h.svc.CheckSpaceMembership(space.Id, userID, true)
	require.Nil(t, appErr)
}

// TestCheckSpaceMembership_ChannelLookupFailed verifies that a non-404 error from
// GetChannelMember propagates as a 500 with the channel_lookup_failed error key.
func TestCheckSpaceMembership_ChannelLookupFailed(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()

	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: channelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", channelID, userID).Return(&mmmodel.ChannelMember{}, nil)

	space, appErr := h.svc.CreateSpace(&model.Space{TeamId: teamID, Title: "Test"}, userID)
	require.Nil(t, appErr)

	mockAPI.On("GetChannelMember", space.ChannelId, userID).
		Return(nil, &mmmodel.AppError{Id: "store.sql_channel.get_member.missing.app_error", StatusCode: http.StatusInternalServerError})

	_, appErr = h.svc.CheckSpaceMembership(space.Id, userID, false)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	require.Equal(t, "app.space.access.channel_lookup_failed.app_error", appErr.Id)
}
