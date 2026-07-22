// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// WebSocket event names published after page and space mutations so clients on all cluster nodes
// can refresh the affected space list and page tree without a full reload. The platform prepends
// "custom_<pluginid>_" to each name on the wire, so the names carry no redundant plugin prefix.
//
// Every event is scoped to the space's backing channel, matching the visibility boundary the REST
// API enforces: reads are membership-gated (CheckSpaceMembership) and the team space list is
// filtered to the caller's channel memberships, so a broader broadcast would leak space existence
// and activity to users who cannot read the space. For the same reason a cross-space move
// publishes a separate payload to each side — the source channel learns only the source space's
// half (source space and old parent), the target channel only the target's half — rather than one
// payload naming both spaces. A member removal is additionally sent to the
// removed user directly, who has already left the channel when the channel-scoped broadcast fires
// and would otherwise never learn of it. space_deleted is likewise delivered to each member
// directly, from a snapshot taken before the backing channel is archived — channel-scoped
// delivery resolves recipients from live channels only, so a broadcast to the just-archived
// channel would reach nobody. A space delete or restore cascades to every live page in
// the space but deliberately publishes only the single space-level event — clients must treat
// space_deleted/space_restored as an invalidation of the space's entire page tree rather than
// expect per-page tombstone events (an unbounded per-page fan-out is not published).
const (
	wsEventPageCreated      = "page_created"
	wsEventPageUpdated      = "page_updated"
	wsEventPageDeleted      = "page_deleted"
	wsEventPageRestored     = "page_restored"
	wsEventPageMoved        = "page_moved"
	wsEventPageDuplicated   = "page_duplicated"
	wsEventPageMovedToSpace = "page_moved_to_space"
	// Unlike the other page_* events above — which carry only {page_id, space_id} as a
	// "something changed, refetch" signal — wsEventPagePresenceUpdated carries the full presence
	// snapshot inline ({page_id, space_id, active_editors, as_of, active_timeout_ms}), so clients
	// need no follow-up fetch. It is rate-limited on autosave but always fires on discard and publish.
	wsEventPagePresenceUpdated = "page_presence_updated"

	wsEventSpaceCreated       = "space_created"
	wsEventSpaceUpdated       = "space_updated"
	wsEventSpaceDeleted       = "space_deleted"
	wsEventSpaceRestored      = "space_restored"
	wsEventSpaceMemberAdded   = "space_member_added"
	wsEventSpaceMemberRemoved = "space_member_removed"
)

// publishToChannels publishes a WebSocket event broadcast to each non-empty, distinct channel ID.
// WS events are best-effort and must not fail the primary mutation; a nil client (store-only unit
// tests) makes this a no-op.
func (s *Service) publishToChannels(event string, payload map[string]any, channelIDs ...string) {
	if s.client == nil {
		return
	}
	seen := make(map[string]struct{}, len(channelIDs))
	for _, chID := range channelIDs {
		if _, ok := seen[chID]; chID == "" || ok {
			continue
		}
		seen[chID] = struct{}{}
		s.client.Frontend.PublishWebSocketEvent(event, payload, &mmmodel.WebsocketBroadcast{ChannelId: chID})
	}
}

// publishToUser publishes a WebSocket event to a single user's connections. WS events are
// best-effort and must not fail the primary mutation; a nil client makes this a no-op.
func (s *Service) publishToUser(event string, payload map[string]any, userID string) {
	if s.client == nil || userID == "" {
		return
	}
	s.client.Frontend.PublishWebSocketEvent(event, payload, &mmmodel.WebsocketBroadcast{UserId: userID})
}
