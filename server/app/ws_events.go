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
// Every event is scoped to the space's backing channel. That is narrower than the read set — an
// open space is also readable by non-members through the team read_public_channel fall-through,
// and they receive no live updates until a write auto-joins them. Accepted: the same caller can
// always re-fetch. For the same reason a cross-space move publishes a separate payload to each
// side — the source channel learns only the source space's half (source space and old parent), the
// target channel only the target's half — rather than one payload naming both spaces.
//
// Three cases are additionally delivered to the affected user directly, because the channel-scoped
// broadcast cannot reach them: a removed user has already left the channel; a just-added or
// just-repermissioned member may not yet resolve as a recipient on the space ("S") channel; and
// space_deleted goes to a member snapshot taken before the backing channel is archived, since
// channel-scoped delivery resolves recipients from live channels only.
//
// A space delete or restore cascades to every live page in the space but publishes only the single
// space-level event — clients must treat space_deleted/space_restored as an invalidation of the
// space's entire page tree rather than expect per-page tombstones.
const (
	wsEventPageCreated      = "page_created"
	wsEventPageUpdated      = "page_updated"
	wsEventPageDeleted      = "page_deleted"
	wsEventPageRestored     = "page_restored"
	wsEventPageMoved        = "page_moved"
	wsEventPageDuplicated   = "page_duplicated"
	wsEventPageMovedToSpace = "page_moved_to_space"
	// Unlike the page_* events above — refetch signals carrying only the ids the client needs to
	// refetch — wsEventPagePresenceUpdated carries the full presence snapshot inline
	// ({page_id, space_id, active_editors, snapshot_at, active_timeout_ms}), so clients need no
	// follow-up fetch.
	wsEventPagePresenceUpdated = "page_presence_updated"

	wsEventSpaceCreated                  = "space_created"
	wsEventSpaceUpdated                  = "space_updated"
	wsEventSpaceDeleted                  = "space_deleted"
	wsEventSpaceRestored                 = "space_restored"
	wsEventSpaceMemberAdded              = "space_member_added"
	wsEventSpaceMemberRemoved            = "space_member_removed"
	wsEventSpaceMemberPermissionsUpdated = "space_member_permissions_updated"
)

// publishToChannels publishes a WebSocket event broadcast to each non-empty, distinct channel ID.
// WS events are best-effort and must not fail the primary mutation; a nil client (store-only unit
// tests) makes this a no-op.
//
// Core's hub delivers a channel broadcast to every raw ChannelMembers row with no team check, and
// former team members keep their backing-channel rows after leaving the team — so each broadcast
// carries an omit list of the members failing the team half of the access gate. When that list
// cannot be resolved the event is dropped for the channel rather than delivered unfiltered: a
// missed refresh signal only degrades liveness, while unfiltered delivery leaks space activity.
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
		omitted, err := s.store.InactiveTeamChannelMembers(chID)
		if err != nil {
			s.log.Warn("failed to resolve the WS omit list; dropping the event for this channel", "event", event, "channel_id", chID, "err", err)
			continue
		}
		broadcast := &mmmodel.WebsocketBroadcast{ChannelId: chID}
		if len(omitted) > 0 {
			broadcast.OmitUsers = make(map[string]bool, len(omitted))
			for _, id := range omitted {
				broadcast.OmitUsers[id] = true
			}
		}
		s.client.Frontend.PublishWebSocketEvent(event, payload, broadcast)
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

// publishMembershipEvent delivers a membership event both to the space's backing channel (the
// observers) and directly to the affected user's own connections: the channel-scoped broadcast may
// not resolve a member whose membership changed moments earlier, and cannot reach one who was just
// removed — yet the affected user is exactly who must learn their access changed.
func (s *Service) publishMembershipEvent(event string, payload map[string]any, channelID, userID string) {
	s.publishToChannels(event, payload, channelID)
	s.publishToUser(event, payload, userID)
}
