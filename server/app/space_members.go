// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"
	"slices"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetSpaceMembers returns one page of space's members plus whether more members exist beyond
// it, each projected to their effective/granted capabilities. page/perPage are normalized
// like every other paginated method (page and perPage both clamped). The pluginapi member
// listing is page-indexed rather than offset-based, so when the requested page comes back full a
// one-row probe at the next page's first slot decides has-more. space is the caller's
// already-fetched record (from its manage gate), so no re-read here.
func (s *Service) GetSpaceMembers(space *model.Space, page, perPage int) ([]*model.SpaceMember, bool, *mmmodel.AppError) {
	if space == nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("GetSpaceMembers", "space_id", space.Id); appErr != nil {
		return nil, false, appErr
	}
	defaultCapabilities, err := s.spaceDefaultCapabilities(space)
	if err != nil {
		return nil, false, storeAppError("GetSpaceMembers", err)
	}
	page = ClampPage(page)
	perPage = ClampPerPage(perPage)
	channelMembers, err := s.client.Channel.ListMembers(space.ChannelId, page, perPage)
	if err != nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.list_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	members := make([]*model.SpaceMember, 0, len(channelMembers))
	for _, cm := range channelMembers {
		members = append(members, toSpaceMember(cm, defaultCapabilities))
	}
	hasMore := false
	if len(channelMembers) == perPage {
		// A page of size 1 holds exactly one element, so its page index equals that element's
		// offset: requesting page (page+1)*perPage at size 1 fetches precisely the first member
		// beyond the current window.
		probe, probeErr := s.client.Channel.ListMembers(space.ChannelId, (page+1)*perPage, 1)
		if probeErr != nil {
			return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.list_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(probeErr)
		}
		hasMore = len(probe) > 0
	}
	return members, hasMore, nil
}

// toSpaceMember builds the wire representation of a channel member's capability state.
func toSpaceMember(cm *mmmodel.ChannelMember, defaultCapabilities []string) *model.SpaceMember {
	mc := model.CapabilitiesFromMember(cm.ExplicitRoles, cm.SchemeAdmin, cm.SchemeGuest, defaultCapabilities)
	member := &model.SpaceMember{
		UserId:              cm.UserId,
		Capabilities:        mc.Effective,
		GrantedCapabilities: mc.Granted,
		IsAdmin:             mc.IsAdmin,
		IsGuest:             mc.IsGuest,
	}
	member.EnsureCapabilities()
	return member
}

// AddSpaceMember adds a user to space's backing channel at the space default (SchemeUser, no
// per-member grants). space is the caller's already-fetched record (from its manage gate), so no
// re-read here.
func (s *Service) AddSpaceMember(space *model.Space, userID string) (*model.SpaceMember, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("AddSpaceMember", "space_id", space.Id, "user_id", userID); appErr != nil {
		return nil, appErr
	}
	// Reject a target who is not an active member of the space's team before touching the
	// backing channel. Core's channel-member add enforces the same integrity check but surfaces
	// it as an opaque failure; checking here keeps the status code honest and guarantees every
	// space member can pass the team half of the access gate — which the last-authorized-member
	// guard in RemoveSpaceMember relies on when deciding who can still reach the space.
	if space.TeamId != "" {
		active, memberErr := s.isActiveTeamMember(space.TeamId, userID)
		if memberErr != nil {
			return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
		if !active {
			return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.not_team_member.app_error", nil, "", http.StatusForbidden)
		}
	}
	defaultCapabilities, err := s.spaceDefaultCapabilities(space)
	if err != nil {
		return nil, storeAppError("AddSpaceMember", err)
	}
	member, err := s.client.Channel.AddMember(space.ChannelId, userID)
	if err != nil {
		// A missing target user is the caller's mistake, not a server fault.
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.add_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	payload := map[string]any{"space_id": space.Id, "user_id": member.UserId}
	s.publishToChannels(wsEventSpaceMemberAdded, payload, space.ChannelId)
	// Also delivered directly: the channel-scoped broadcast may not resolve a member added moments
	// earlier, and the new member has no other signal that they now have the space.
	s.publishToUser(wsEventSpaceMemberAdded, payload, member.UserId)
	return toSpaceMember(member, defaultCapabilities), nil
}

// hasOtherAuthorizedAdmin reports whether space's backing channel still has a SchemeAdmin member
// who can reach the space once excludeUserID is disregarded — the last-admin invariant's
// admin-side counterpart to hasOtherAuthorizedMember. excludeUserID, when non-empty, is skipped, so
// the answer describes what would remain after that user is demoted or removed.
func (s *Service) hasOtherAuthorizedAdmin(space *model.Space, excludeUserID string) (bool, error) {
	return s.hasOtherAuthorizedMemberMatching(space, excludeUserID, func(cm *mmmodel.ChannelMember) bool { return cm.SchemeAdmin })
}

// requireNotLastAdmin rejects an operation that would leave space without an admin who can still
// reach it, disregarding excludeUserID (the member being demoted or removed). Callers run it inside
// the space-keyed membership lock, alongside the mutation it guards. where attributes both the
// lookup failure and the rejection to the calling operation.
func (s *Service) requireNotLastAdmin(where string, space *model.Space, excludeUserID string) *mmmodel.AppError {
	otherAdmin, err := s.hasOtherAuthorizedAdmin(space, excludeUserID)
	if err != nil {
		return mmmodel.NewAppError(where, "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	if !otherAdmin {
		return mmmodel.NewAppError(where, "app.space.member.last_admin.app_error", nil, "", http.StatusConflict)
	}
	return nil
}

// SetSpaceMemberCapabilities replaces targetUserID's per-member granted capability set. Callers
// must already hold manage-tier authority over the space (RequireSpaceAdminOrTeamPerm with
// manage_space); this method
// additionally enforces the self/admin escalation guard and the last-admin invariant. Guest
// members are rejected: they stay read-only via the scheme's guest role. actingUserID is the
// caller, used only to decide the self-escalation guard.
func (s *Service) SetSpaceMemberCapabilities(space *model.Space, targetUserID string, capabilities []string, actingUserID string) (*model.SpaceMember, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(targetUserID) {
		return nil, mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := model.ValidateGrantedCapabilities(capabilities); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("SetSpaceMemberCapabilities", "space_id", space.Id, "user_id", targetUserID); appErr != nil {
		return nil, appErr
	}

	requestedAdmin := slices.Contains(capabilities, model.CapabilityAdminSpace)
	selfTargeted := targetUserID == actingUserID

	// The scheme-role read, the target's current admin status, the escalation-guard decision, and
	// the admin count all read state that a concurrent SetSpaceDefaultCapabilities (repoints the
	// channel's scheme) or SetSpaceMemberCapabilities/RemoveSpaceMember (the last-admin invariant)
	// call could change: a stale read taken before the lock lets a concurrent default-capabilities
	// repoint write a superseded scheme's role name, or a concurrent promotion/demotion flip admin
	// status, under this operation's feet — so every one of them runs inside the space-keyed
	// advisory lock, alongside the mutation itself.
	var appErr *mmmodel.AppError
	var newRoles string
	var newSchemeAdmin bool
	var resolvedRoles *schemeRoles
	var updatedMember *mmmodel.ChannelMember
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		var rolesErr error
		resolvedRoles, rolesErr = s.getSchemeRolesForChannel(space.ChannelId)
		if rolesErr != nil {
			appErr = storeAppError("SetSpaceMemberCapabilities", rolesErr)
			return appErr
		}
		newRoles, newSchemeAdmin = model.RolesForCapabilities(capabilities, resolvedRoles.UserRoleName)

		target, memErr := s.client.Channel.GetMember(space.ChannelId, targetUserID)
		if memErr != nil {
			if errors.Is(memErr, pluginapi.ErrNotFound) {
				appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(memErr)
				return appErr
			}
			appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
			return appErr
		}
		if target.SchemeGuest {
			appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.guest_not_assignable.app_error", nil, "", http.StatusBadRequest)
			return appErr
		}

		adminAffected := target.SchemeAdmin || requestedAdmin
		if adminAffected || selfTargeted {
			if e := s.RequireSpaceAdminOrSysadmin("SetSpaceMemberCapabilities", space, actingUserID); e != nil {
				appErr = e
				return e
			}
		}

		if target.SchemeAdmin && !newSchemeAdmin {
			if e := s.requireNotLastAdmin("SetSpaceMemberCapabilities", space, targetUserID); e != nil {
				appErr = e
				return e
			}
		}

		roles := newRoles
		if newSchemeAdmin {
			roles = roles + " " + resolvedRoles.AdminRoleName
		}
		member, updErr := s.client.Channel.UpdateChannelMemberRoles(space.ChannelId, targetUserID, roles)
		if updErr != nil {
			appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.update_capabilities_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updErr)
			return appErr
		}
		updatedMember = member
		return nil
	})
	if lockErr != nil {
		if appErr != nil {
			return nil, appErr
		}
		return nil, storeAppError("SetSpaceMemberCapabilities", lockErr)
	}

	// The scheme roles read under the lock still describe this channel: only
	// SetSpaceDefaultCapabilities repoints a channel's scheme, and it serializes behind the same
	// space-keyed lock, so the default capability set is projected from them rather than re-read.
	defaultCapabilities, defErr := s.defaultCapabilitiesForRoles(resolvedRoles)
	if defErr != nil {
		return nil, storeAppError("SetSpaceMemberCapabilities", defErr)
	}
	// The response is projected from the member the role update returned. Re-reading it would go to
	// a replica, which on a lagging one still carries the pre-update roles and would report the
	// caller's own committed change as not having taken effect.
	result := toSpaceMember(updatedMember, defaultCapabilities)

	payload := map[string]any{"space_id": space.Id, "user_id": targetUserID}
	// Delivered both ways deliberately: the channel-scoped broadcast covers observers, and the
	// direct publish guarantees the target learns its own capabilities changed even if the
	// channel-scoped resolution misses them on a space ("S") channel.
	s.publishToUser(wsEventSpaceMemberCapabilitiesUpdated, payload, targetUserID)
	s.publishToChannels(wsEventSpaceMemberCapabilitiesUpdated, payload, space.ChannelId)
	return result, nil
}

// RemoveSpaceMember removes a user from space's backing channel. Precedence: target existence
// resolves before the last-member/last-admin guards; an admin-target removal is additionally
// escalation-guarded (RequireSpaceAdminOrSysadmin) and last-admin-guarded, both under the same
// space-scoped advisory lock as SetSpaceMemberCapabilities' admin-revoke path. space is the
// caller's already-fetched record (from its gate), so no re-read here.
func (s *Service) RemoveSpaceMember(space *model.Space, userID, actingUserID string) *mmmodel.AppError {
	if space == nil {
		return mmmodel.NewAppError("RemoveSpaceMember", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("RemoveSpaceMember", "space_id", space.Id, "user_id", userID); appErr != nil {
		return appErr
	}

	// The target lookup, its admin status, the escalation-guard decision, and the admin count all
	// read state a concurrent SetSpaceMemberCapabilities/RemoveSpaceMember call could change
	// (the last-admin invariant) — a stale admin-status read taken before the lock lets a
	// concurrent promotion flip it under this operation's feet — so every one of them runs inside
	// the space-keyed advisory lock, alongside the mutation itself. This applies to self-removal
	// too, since the last-admin invariant covers the sole admin's self-leave.
	var appErr *mmmodel.AppError
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		target, memErr := s.client.Channel.GetMember(space.ChannelId, userID)
		if memErr != nil {
			if !errors.Is(memErr, pluginapi.ErrNotFound) {
				appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
				return appErr
			}
			// Non-member target: a manage-gated caller removing someone else has already proven
			// manage authority over this space, so there is nothing left to existence-hide behind
			// and it gets a plain 404, matching SetSpaceMemberCapabilities. Self-removal returns the
			// shared existence-hiding 403 whatever the space's ViewAccess — keying the code on
			// ViewAccess would let a caller read a space's exposure setting off the status alone,
			// the distinction the rest of the surface exists to deny.
			if userID != actingUserID {
				appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(memErr)
			} else {
				appErr = existenceHidingForbidden("RemoveSpaceMember")
			}
			return appErr
		}

		if target.SchemeAdmin {
			if e := s.RequireSpaceAdminOrSysadmin("RemoveSpaceMember", space, actingUserID); e != nil {
				appErr = e
				return e
			}
			if e := s.requireNotLastAdmin("RemoveSpaceMember", space, userID); e != nil {
				appErr = e
				return e
			}
		}
		hasOther, guardErr := s.hasOtherAuthorizedMember(space, userID)
		if guardErr != nil {
			appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(guardErr)
			return appErr
		}
		if !hasOther {
			appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.last_member.app_error", nil, "", http.StatusConflict)
			return appErr
		}
		if err := s.client.Channel.DeleteMember(space.ChannelId, userID); err != nil {
			if errors.Is(err, pluginapi.ErrNotFound) {
				appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
				return appErr
			}
			appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
			return appErr
		}
		return nil
	})
	if lockErr != nil {
		if appErr != nil {
			return appErr
		}
		// The store's own errors — notably the retryable ErrConflict a lock-acquisition timeout
		// yields — keep their conventional status codes rather than collapsing to a 500.
		return storeAppError("RemoveSpaceMember", lockErr)
	}
	payload := map[string]any{"space_id": space.Id, "user_id": userID}
	s.publishToChannels(wsEventSpaceMemberRemoved, payload, space.ChannelId)
	// The removed user has already left the backing channel, so the channel-scoped broadcast
	// above never reaches them; send the event to their own connections directly.
	s.publishToUser(wsEventSpaceMemberRemoved, payload, userID)
	return nil
}
