// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// WebSocket event contract tests: each mutation's event name, payload shape, and broadcast scope
// are pinned exactly — a typo in any of them would otherwise pass every Maybe-stubbed test.
// page_created and page_moved are pinned elsewhere (api_handler_test.go and space_test.go).

package app_test

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// TestServiceUpdatePage_PublishesUpdatedEvent pins page_updated: page/space payload, broadcast to
// the page's backing channel.
func TestServiceUpdatePage_PublishesUpdatedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	title := "Renamed"
	updated, appErr := h.svc.UpdatePage(page.Id, space.Id, &model.PagePatch{Title: &title}, new(page.EditAt), false, userID)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_updated",
		map[string]any{"page_id": page.Id, "space_id": space.Id},
		&mmmodel.WebsocketBroadcast{ChannelId: updated.ChannelId})
}

// TestServiceDeletePage_PublishesDeletedEvent pins page_deleted: page/space payload, broadcast to
// the deleted page's backing channel (returned by the store from the locked row).
func TestServiceDeletePage_PublishesDeletedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	require.Nil(t, h.svc.DeletePage(page.Id, space.Id, userID))

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_deleted",
		map[string]any{"page_id": page.Id, "space_id": space.Id},
		&mmmodel.WebsocketBroadcast{ChannelId: channelID})
}

// TestServiceDeletePage_FailurePublishesNothing verifies a failed mutation emits no event: the
// delete targets a space the page is not in, 404s, and page_deleted must not fire.
func TestServiceDeletePage_FailurePublishesNothing(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	appErr := h.svc.DeletePage(page.Id, mmmodel.NewId(), userID)
	require.NotNil(t, appErr)
	require.Equal(t, 404, appErr.StatusCode)

	mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "page_deleted", mock.Anything, mock.Anything)
}

// TestServiceRestorePage_PublishesRestoredEvent pins page_restored: page/space payload, broadcast
// to the restored page's backing channel.
func TestServiceRestorePage_PublishesRestoredEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	require.Nil(t, h.svc.DeletePage(page.Id, space.Id, userID))

	restored, appErr := h.svc.RestorePage(page.Id, space.Id, userID)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_restored",
		map[string]any{"page_id": restored.Id, "space_id": restored.SpaceId},
		&mmmodel.WebsocketBroadcast{ChannelId: restored.ChannelId})
}

// TestServiceDuplicatePage_PublishesDuplicatedEvent pins page_duplicated: the payload names the
// new copy (not the source page), broadcast to the copy's backing channel.
func TestServiceDuplicatePage_PublishesDuplicatedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	copyRoot, appErr := h.svc.DuplicatePage(page.Id, space, userID, false, nil, nil)
	require.Nil(t, appErr)
	require.NotEqual(t, page.Id, copyRoot.Id)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_duplicated",
		map[string]any{"page_id": copyRoot.Id, "space_id": copyRoot.SpaceId, "parent_id": copyRoot.ParentId},
		&mmmodel.WebsocketBroadcast{ChannelId: copyRoot.ChannelId})
}

// TestServiceMovePageToSpace_PublishesEventToBothChannels pins page_moved_to_space's dual
// broadcast: each channel receives only its own space's half of the move — the source channel
// learns the source space and old parent (so its clients drop the subtree), the target channel
// the target space and new parent (so its clients pick it up). Neither payload names the other
// space, so a member of only one side learns nothing about the other.
func TestServiceMovePageToSpace_PublishesEventToBothChannels(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	chA := mmmodel.NewId()
	spaceA := seedSpaceForTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceForTeam(t, h.store, chB, teamID)
	page := mustCreatePage(t, h.store, spaceA.Id, chA, userID, "")

	moved, appErr := h.svc.MovePageToSpace(page.Id, spaceA, spaceB, nil, new(page.UpdateAt), false, userID)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_moved_to_space",
		map[string]any{
			"page_id":         moved.Id,
			"source_space_id": spaceA.Id,
			"old_parent_id":   "",
		},
		&mmmodel.WebsocketBroadcast{ChannelId: chA})
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_moved_to_space",
		map[string]any{
			"page_id":         moved.Id,
			"target_space_id": spaceB.Id,
			"new_parent_id":   "",
		},
		&mmmodel.WebsocketBroadcast{ChannelId: chB})
	// Exactly the two half-payload events above: an extra broad or full-payload broadcast would
	// leak one space's details to the other side.
	mockAPI.AssertNumberOfCalls(t, "PublishWebSocketEvent", 2)
}

// TestServiceMovePageToSpace_NoOpPublishesNothing verifies the same-space/same-parent no-op emits
// no event: nothing moved, so clients have nothing to invalidate.
func TestServiceMovePageToSpace_NoOpPublishesNothing(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	same, appErr := h.svc.MovePageToSpace(page.Id, space, space, nil, new(page.UpdateAt), false, userID)
	require.Nil(t, appErr)
	require.Equal(t, page.Id, same.Id)

	mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "page_moved_to_space", mock.Anything, mock.Anything)
}

// TestServiceCreateSpace_PublishesCreatedEvent pins space_created: space-id payload, broadcast
// scoped to the backing channel (the visibility boundary the REST API enforces; the team space
// list is filtered to the caller's channel memberships).
func TestServiceCreateSpace_PublishesCreatedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_created",
		map[string]any{"space_id": space.Id},
		&mmmodel.WebsocketBroadcast{ChannelId: space.ChannelId})
}

// TestServiceUpdateSpace_PublishesUpdatedEvent pins space_updated: space-id payload, broadcast
// scoped to the backing channel.
func TestServiceUpdateSpace_PublishesUpdatedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	// The post-update channel-metadata sync looks the backing channel up; a nil channel makes it
	// a no-op without needing channel-update expectations.
	mockAPI.On("GetChannelOfType", mock.Anything, mock.Anything).Return((*mmmodel.Channel)(nil), nil)

	title := "Renamed Space"
	updated, appErr := h.svc.UpdateSpace(space, &model.SpacePatch{Title: &title}, new(space.UpdateAt), false)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_updated",
		map[string]any{"space_id": updated.Id},
		&mmmodel.WebsocketBroadcast{ChannelId: updated.ChannelId})
}

// TestServiceDeleteSpace_PublishesDeletedEvent pins space_deleted: space-id payload, delivered
// directly to each backing-channel member snapshotted before the channel is archived — a
// channel-scoped broadcast after the archive would resolve zero recipients. Clients must treat
// it as an invalidation of the space's whole page tree (no per-page tombstone events are
// published).
func TestServiceDeleteSpace_PublishesDeletedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	memberA := mmmodel.NewId()
	memberB := mmmodel.NewId()
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{
			{ChannelId: channelID, UserId: memberA},
			{ChannelId: channelID, UserId: memberB},
		}, nil)
	mockAPI.On("DeleteChannel", channelID).Return(nil)

	require.Nil(t, h.svc.DeleteSpace(space))

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_deleted",
		map[string]any{"space_id": space.Id},
		&mmmodel.WebsocketBroadcast{UserId: memberA})
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_deleted",
		map[string]any{"space_id": space.Id},
		&mmmodel.WebsocketBroadcast{UserId: memberB})
	mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "space_deleted",
		mock.Anything, &mmmodel.WebsocketBroadcast{ChannelId: channelID})
}

// TestServiceDeleteSpace_SnapshotFailureFallsBackToChannelBroadcast verifies the delivery
// fallback when the pre-archive member snapshot fails: the event cannot be delivered per-user,
// so it is published as a channel-scoped broadcast — before the channel is archived, since a
// channel-scoped broadcast to an archived channel resolves zero recipients.
func TestServiceDeleteSpace_SnapshotFailureFallsBackToChannelBroadcast(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).
		Return(nil, &mmmodel.AppError{Message: "boom", StatusCode: 500})
	mockAPI.On("DeleteChannel", channelID).Return(nil)

	require.Nil(t, h.svc.DeleteSpace(space))

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_deleted",
		map[string]any{"space_id": space.Id},
		&mmmodel.WebsocketBroadcast{ChannelId: channelID})
	// The fallback must be published while the channel is still live: scan the recorded calls
	// and require the broadcast to precede the archive.
	publishIdx, archiveIdx := -1, -1
	for i, call := range mockAPI.Calls {
		switch call.Method {
		case "PublishWebSocketEvent":
			if publishIdx == -1 {
				publishIdx = i
			}
		case "DeleteChannel":
			archiveIdx = i
		}
	}
	require.GreaterOrEqual(t, publishIdx, 0, "space_deleted must be published")
	require.GreaterOrEqual(t, archiveIdx, 0, "the backing channel must be archived")
	require.Less(t, publishIdx, archiveIdx, "the fallback broadcast must be published before the channel is archived")
}

// TestServiceRestoreSpace_PublishesRestoredEvent pins space_restored: space-id payload, broadcast
// scoped to the backing channel. Like space_deleted, clients treat it as a whole-tree invalidation.
func TestServiceRestoreSpace_PublishesRestoredEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).Return(mmmodel.ChannelMembers{}, nil)
	mockAPI.On("DeleteChannel", channelID).Return(nil)
	mockAPI.On("RestoreChannel", channelID).Return(nil)

	require.Nil(t, h.svc.DeleteSpace(space))
	restored, appErr := h.svc.RestoreSpace(space.Id)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_restored",
		map[string]any{"space_id": restored.Id},
		&mmmodel.WebsocketBroadcast{ChannelId: restored.ChannelId})
}

// TestServiceAddSpaceMember_PublishesMemberAddedEvent pins space_member_added on add:
// space/user payload, broadcast scoped to the backing channel (matching the membership-gated
// reads, like every other space and page event).
func TestServiceAddSpaceMember_PublishesMemberAddedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	newUserID := mmmodel.NewId()
	mockAPI.On("AddChannelMember", space.ChannelId, newUserID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: newUserID}, nil)

	member, appErr := h.svc.AddSpaceMember(space, newUserID)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_added",
		map[string]any{"space_id": space.Id, "user_id": member.UserId},
		&mmmodel.WebsocketBroadcast{ChannelId: space.ChannelId})
}

// TestServiceRemoveSpaceMember_PublishesMemberRemovedEvent pins space_member_removed on remove:
// the channel-scoped broadcast for remaining members, plus a user-scoped publish to the removed
// user, who has already left the channel when the channel broadcast fires.
func TestServiceRemoveSpaceMember_PublishesMemberRemovedEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, creatorID := createSpaceForMemberTests(t, h, mockAPI)

	targetID := mmmodel.NewId()
	mockAPI.On("GetChannelMembers", space.ChannelId, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{
			{ChannelId: space.ChannelId, UserId: creatorID},
			{ChannelId: space.ChannelId, UserId: targetID},
		}, nil)
	mockAPI.On("DeleteChannelMember", space.ChannelId, targetID).Return(nil)

	require.Nil(t, h.svc.RemoveSpaceMember(space, targetID))

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_removed",
		map[string]any{"space_id": space.Id, "user_id": targetID},
		&mmmodel.WebsocketBroadcast{ChannelId: space.ChannelId})
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_removed",
		map[string]any{"space_id": space.Id, "user_id": targetID},
		&mmmodel.WebsocketBroadcast{UserId: targetID})
}
