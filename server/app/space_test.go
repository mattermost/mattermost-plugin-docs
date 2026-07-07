// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"net/http"
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
	// archiveOrphanChannel's LogWarn carries failure_reason as its own field (not concatenated into
	// the message), one field pair more than DeleteSpace/RestoreSpace's warn logs.
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

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

	spaces, listErr := h.svc.GetSpacesForTeam(teamID, 0, 60)
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

// TestServicePatchSpace verifies PatchSpace applies only the supplied (non-nil) fields, preserves
// unspecified ones, can clear a field with an explicit empty string, and enforces the
// optimistic-lock baseline.
func TestServicePatchSpace(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	patched, appErr := h.svc.PatchSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("New Title"), Description: mmmodel.NewPointer("New Desc")}, space.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, "New Title", patched.Title)
	require.Equal(t, "New Desc", patched.Description)

	// A patch with only Icon leaves the previously-set Title/Description intact.
	patched2, appErr := h.svc.PatchSpace(space.Id, &model.SpacePatch{Icon: mmmodel.NewPointer("icon-data")}, patched.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, "New Title", patched2.Title, "unspecified fields are preserved")
	require.Equal(t, "New Desc", patched2.Description)
	require.Equal(t, "icon-data", patched2.Icon)

	// An explicit empty string clears a field (a nil field would leave it unchanged).
	patched3, appErr := h.svc.PatchSpace(space.Id, &model.SpacePatch{Description: mmmodel.NewPointer("")}, patched2.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, "", patched3.Description, "an explicit empty string clears the field")
	require.Equal(t, "New Title", patched3.Title)

	// A stale baseline is rejected as a conflict unless force is set.
	_, appErr = h.svc.PatchSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("Stale")}, space.UpdateAt, false)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	forced, appErr := h.svc.PatchSpace(space.Id, &model.SpacePatch{Title: mmmodel.NewPointer("Forced")}, space.UpdateAt, true)
	require.Nil(t, appErr)
	require.Equal(t, "Forced", forced.Title)
}

// TestServicePatchSpace_NoChangesRejected verifies an all-nil patch is rejected as a 400 rather
// than silently bumping UpdateAt (mirroring PagePatch's nothing-to-update guard).
func TestServicePatchSpace_NoChangesRejected(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.PatchSpace(space.Id, &model.SpacePatch{}, space.UpdateAt, false)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "model.space.patch.nothing_to_update.app_error", appErr.Id)

	// The space row is untouched: its UpdateAt did not advance.
	got, getErr := h.svc.GetSpace(space.Id)
	require.Nil(t, getErr)
	require.Equal(t, space.UpdateAt, got.UpdateAt)
}
