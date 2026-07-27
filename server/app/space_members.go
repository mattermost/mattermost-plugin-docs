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
	defaultCaps, err := s.spaceDefaultCapabilities(space)
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
		members = append(members, projectSpaceMember(cm, defaultCaps))
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

// projectSpaceMember builds the wire projection of a channel member's capability state.
func projectSpaceMember(cm *mmmodel.ChannelMember, defaultCaps []string) *model.SpaceMember {
	mc := model.CapabilitiesFromMember(cm.ExplicitRoles, cm.SchemeAdmin, cm.SchemeGuest, defaultCaps)
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
	defaultCaps, err := s.spaceDefaultCapabilities(space)
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
	s.publishToChannels(wsEventSpaceMemberAdded, map[string]any{"space_id": space.Id, "user_id": member.UserId}, space.ChannelId)
	return projectSpaceMember(member, defaultCaps), nil
}

// hasOtherAuthorizedAdmin reports whether space's backing channel still has a SchemeAdmin member
// who can reach the space once excludeUserID is disregarded — the last-admin invariant's
// admin-side counterpart to hasOtherAuthorizedMember. excludeUserID, when non-empty, is skipped, so
// the answer describes what would remain after that user is demoted or removed.
func (s *Service) hasOtherAuthorizedAdmin(space *model.Space, excludeUserID string) (bool, error) {
	return s.hasOtherAuthorizedMemberMatching(space, excludeUserID, func(cm *mmmodel.ChannelMember) bool { return cm.SchemeAdmin })
}

// SetSpaceMemberCapabilities replaces targetUserID's per-member granted capability set. Callers
// must already hold manage-tier authority over the space (see RequireSpaceManage); this method
// additionally enforces the self/admin escalation guard and the last-admin invariant. Guest
// members are rejected: they stay read-only via the scheme's guest role. actingUserID is the
// caller, used only to decide the self-escalation guard.
func (s *Service) SetSpaceMemberCapabilities(space *model.Space, targetUserID string, caps []string, actingUserID string) (*model.SpaceMember, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(targetUserID) {
		return nil, mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := model.ValidateGrantedCapabilities(caps); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("SetSpaceMemberCapabilities", "space_id", space.Id, "user_id", targetUserID); appErr != nil {
		return nil, appErr
	}

	requestedAdmin := slices.Contains(caps, model.CapabilityAdminSpace)
	selfTargeted := targetUserID == actingUserID

	// The scheme-role read, the target's current admin status, the escalation-guard decision, and
	// the admin count all read state that a concurrent SetSpaceDefaultCapabilities (repoints the
	// channel's scheme) or SetSpaceMemberCapabilities/RemoveSpaceMember (the last-admin invariant)
	// call could change: a stale read taken before the lock lets a concurrent default-capabilities
	// repoint write a retired scheme's role name, or a concurrent promotion/demotion flip admin
	// status, under this operation's feet — so every one of them runs inside the space-keyed
	// advisory lock, alongside the mutation itself.
	var appErr *mmmodel.AppError
	var newRoles string
	var newSchemeAdmin bool
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		schemeRoles, rolesErr := s.store.GetSchemeRolesForChannel(space.ChannelId)
		if rolesErr != nil {
			appErr = storeAppError("SetSpaceMemberCapabilities", rolesErr)
			return appErr
		}
		newRoles, newSchemeAdmin = model.RolesForCapabilities(caps, schemeRoles.UserRoleName)

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
			otherAdmin, cntErr := s.hasOtherAuthorizedAdmin(space, targetUserID)
			if cntErr != nil {
				appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(cntErr)
				return appErr
			}
			if !otherAdmin {
				appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.last_admin.app_error", nil, "", http.StatusConflict)
				return appErr
			}
		}

		roles := newRoles
		if newSchemeAdmin {
			roles = roles + " " + schemeRoles.AdminRoleName
		}
		if _, updErr := s.client.Channel.UpdateChannelMemberRoles(space.ChannelId, targetUserID, roles); updErr != nil {
			appErr = mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.update_capabilities_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updErr)
			return appErr
		}
		return nil
	})
	if lockErr != nil {
		if appErr != nil {
			return nil, appErr
		}
		return nil, storeAppError("SetSpaceMemberCapabilities", lockErr)
	}

	defaultCaps, defErr := s.spaceDefaultCapabilities(space)
	if defErr != nil {
		return nil, storeAppError("SetSpaceMemberCapabilities", defErr)
	}
	fresh, memErr := s.client.Channel.GetMember(space.ChannelId, targetUserID)
	var result *model.SpaceMember
	if memErr != nil {
		// The role update already committed, so re-reporting this as a failure would misreport
		// success as an error; project the response from the requested capability set instead
		// (guests are rejected above, so schemeGuest is always false here), still firing the WS
		// event.
		s.log.Warn("SetSpaceMemberCapabilities: post-commit member re-read failed; responding from the requested set", "space_id", space.Id, "user_id", targetUserID, "err", memErr)
		result = projectSpaceMember(&mmmodel.ChannelMember{
			UserId:        targetUserID,
			ExplicitRoles: newRoles,
			SchemeAdmin:   newSchemeAdmin,
		}, defaultCaps)
	} else {
		result = projectSpaceMember(fresh, defaultCaps)
	}

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
			// Non-member target: existence-hiding on a private space (matches the read resolver's
			// convention), a plain 404 on an open space (existence is already public there). That
			// split only applies to self-removal — a manage-gated caller removing someone else has
			// already proven manage authority over this space, so there is nothing left to
			// existence-hide behind; it always gets the plain 404, matching
			// SetSpaceMemberCapabilities.
			if userID != actingUserID || space.ViewAccess == model.ViewAccessOpen {
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
			otherAdmin, cntErr := s.hasOtherAuthorizedAdmin(space, userID)
			if cntErr != nil {
				appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(cntErr)
				return appErr
			}
			if !otherAdmin {
				appErr = mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.last_admin.app_error", nil, "", http.StatusConflict)
				return appErr
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
