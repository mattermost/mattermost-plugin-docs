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
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// GetSpaceMembers returns one page of space's members plus whether more members exist beyond
// it, each projected to their effective/granted capabilities. page/perPage are normalized
// like every other paginated method (page and perPage both clamped). The pluginapi member
// listing is page-indexed rather than offset-based, so when the requested page comes back full a
// one-row probe at the next page's first slot decides has-more. space is the caller's
// already-fetched record, from its manage gate.
func (s *Service) GetSpaceMembers(space *model.Space, page, perPage int) ([]*model.SpaceMember, bool, *mmmodel.AppError) {
	if space == nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("GetSpaceMembers", "space_id", space.Id); appErr != nil {
		return nil, false, appErr
	}
	defaultCapabilities, err := s.spaceDefaultCapabilities(space)
	if err != nil {
		return nil, false, schemeAppError("GetSpaceMembers", err)
	}
	page = ClampPage(page)
	perPage = ClampPerPage(perPage)
	channelMembers, err := s.client.Channel.ListMembers(space.ChannelId, page, perPage)
	if err != nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	// One lookup for the whole page, not one per member: the marker is stored as a single set per
	// space, not one KV entry per member.
	autoJoined, err := s.autoJoinedIDs(space.Id)
	if err != nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	members := make([]*model.SpaceMember, 0, len(channelMembers))
	for _, cm := range channelMembers {
		members = append(members, toSpaceMember(cm, defaultCapabilities, slices.Contains(autoJoined, cm.UserId)))
	}
	hasMore := false
	if len(channelMembers) == perPage {
		// A page of size 1 holds exactly one element, so its page index equals that element's
		// offset: requesting page (page+1)*perPage at size 1 fetches precisely the first member
		// beyond the current window.
		probe, probeErr := s.client.Channel.ListMembers(space.ChannelId, (page+1)*perPage, 1)
		if probeErr != nil {
			return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(probeErr)
		}
		hasMore = len(probe) > 0
	}
	return members, hasMore, nil
}

// toSpaceMember builds the wire representation of a channel member's capability state.
// autoJoined reports whether the member carries the auto-join provenance marker.
func toSpaceMember(cm *mmmodel.ChannelMember, defaultCapabilities []string, autoJoined bool) *model.SpaceMember {
	mc := model.PermissionsFromMember(cm.ExplicitRoles, cm.SchemeAdmin, cm.SchemeGuest, defaultCapabilities)
	member := &model.SpaceMember{
		UserId:              cm.UserId,
		Capabilities:        mc.Effective,
		GrantedCapabilities: mc.Granted,
		IsAdmin:             mc.IsAdmin,
		IsGuest:             mc.IsGuest,
		AutoJoined:          autoJoined,
	}
	member.EnsureCapabilities()
	return member
}

// AddSpaceMember adds a user to space's backing channel at the space default (SchemeUser, no
// per-member grants). space is the caller's already-fetched record, from its manage gate.
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
	active, memberErr := s.isActiveTeamMember(space.TeamId, userID)
	if memberErr != nil {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
	}
	if !active {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.not_team_member.app_error", nil, "", http.StatusForbidden)
	}
	defaultCapabilities, err := s.spaceDefaultCapabilities(space)
	if err != nil {
		return nil, schemeAppError("AddSpaceMember", err)
	}
	// Core affirms an add of an already-existing member, returning their membership unchanged
	// rather than erroring — so this also covers a deliberate re-add of a member the auto-join
	// pre-step joined earlier. Either way it is a deliberate admin act on this membership, the same
	// legitimizing act SetSpaceMemberCapabilities' clear is for, so the marker is cleared here too.
	// The add and the clear run together under the space-keyed lock — the same lock UndoAutoJoin
	// takes to check the marker before deleting — so the clear can never be separated from the
	// affirm it legitimizes: UndoAutoJoin either runs wholly before (deletes the stale membership,
	// and this call's locked add then recreates it fresh) or wholly after (finds the marker already
	// cleared and skips the delete). An unlocked clear could instead land in the gap between
	// UndoAutoJoin's marker check and its delete, so its delete removes a membership this call just
	// legitimized.
	var member *mmmodel.ChannelMember
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		added, addErr := s.client.Channel.AddMember(space.ChannelId, userID)
		if addErr != nil {
			// A missing target user is the caller's mistake, not a server fault.
			if errors.Is(addErr, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("AddSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(addErr)
			}
			return mmmodel.NewAppError("AddSpaceMember", "app.space.add_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(addErr)
		}
		member = added
		if clearErr := s.clearAutoJoined(space.Id, member.UserId); clearErr != nil {
			// Best-effort: the membership add already committed.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", member.UserId, "err", clearErr)
		}
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("AddSpaceMember", lockErr)
	}
	payload := map[string]any{"space_id": space.Id, "user_id": member.UserId}
	s.publishMembershipEvent(wsEventSpaceMemberAdded, payload, space.ChannelId, member.UserId)
	return toSpaceMember(member, defaultCapabilities, false), nil
}

// requireNotLastAdmin rejects an operation that would leave space without an admin who can still
// reach it, disregarding excludeUserID (the member being demoted or removed). Callers run it inside
// the space-keyed membership lock, alongside the mutation it guards. where attributes both the
// lookup failure and the rejection to the calling operation.
func (s *Service) requireNotLastAdmin(where string, space *model.Space, excludeUserID string) *mmmodel.AppError {
	_, otherAdmin, err := s.otherAuthorizedMembers(space, excludeUserID)
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
	if appErr := model.ValidateGrantedPermissions(capabilities); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("SetSpaceMemberCapabilities", "space_id", space.Id, "user_id", targetUserID); appErr != nil {
		return nil, appErr
	}

	requestedAdmin := slices.Contains(capabilities, mmmodel.PermissionAdminSpace.Id)
	selfTargeted := targetUserID == actingUserID

	// The scheme-role read, the target's current admin status, the escalation-guard decision, and
	// the admin count all read state that a concurrent SetSpaceDefaultCapabilities (repoints the
	// channel's scheme) or SetSpaceMemberCapabilities/RemoveSpaceMember (the last-admin invariant)
	// call could change: a stale read taken before the lock lets a concurrent default-capabilities
	// repoint write a superseded scheme's role name, or a concurrent promotion/demotion flip admin
	// status, mid-operation — so every one of them runs inside the space-keyed
	// advisory lock, alongside the mutation itself.
	var newRoles string
	var newSchemeAdmin bool
	var defaultCapabilities []string
	var updatedMember *mmmodel.ChannelMember
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		resolvedRoles, rolesErr := s.getSchemeRolesForChannel(space.ChannelId)
		if rolesErr != nil {
			return schemeAppError("SetSpaceMemberCapabilities", rolesErr)
		}
		newRoles, newSchemeAdmin = model.RolesForPermissions(capabilities, resolvedRoles.UserRoleName)
		// Resolved here, before the write below commits, rather than after the lock: the space's
		// default capability set does not change during a member-capability write (only
		// SetSpaceDefaultCapabilities changes it, serialized behind this same lock), so nothing about
		// resolving it depends on the write having happened. Doing it here means a failure here is
		// caught before anything commits, instead of surfacing a committed write as a 500 and
		// silently skipping the WS event below.
		defaults, defErr := s.defaultCapabilitiesForRoles(resolvedRoles)
		if defErr != nil {
			return schemeAppError("SetSpaceMemberCapabilities", defErr)
		}
		defaultCapabilities = defaults

		// Master-backed on purpose: the plugin API's membership reads come from a replica, and the
		// admin flag read here decides whether the escalation and last-admin guards below run at
		// all — a lagging replica hiding a just-committed promotion would skip both (see the
		// rationale at the top of store/membership_store.go).
		targetAdmin, targetGuest, memErr := s.store.MemberSchemeFlags(space.ChannelId, targetUserID)
		if memErr != nil {
			if store.IsErrNotFound(memErr) {
				return mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(memErr)
			}
			return mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		if targetGuest {
			return mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.guest_not_assignable.app_error", nil, "", http.StatusBadRequest)
		}

		adminAffected := targetAdmin || requestedAdmin
		if adminAffected || selfTargeted {
			if e := s.RequireSpaceAdminOrSysadmin("SetSpaceMemberCapabilities", space, actingUserID); e != nil {
				return e
			}
		}

		if targetAdmin && !newSchemeAdmin {
			if e := s.requireNotLastAdmin("SetSpaceMemberCapabilities", space, targetUserID); e != nil {
				return e
			}
		}

		roles := newRoles
		if newSchemeAdmin {
			roles = roles + " " + resolvedRoles.AdminRoleName
		}
		member, updErr := s.client.Channel.UpdateChannelMemberRoles(space.ChannelId, targetUserID, roles)
		if updErr != nil {
			return mmmodel.NewAppError("SetSpaceMemberCapabilities", "app.space.member.update_capabilities_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updErr)
		}
		updatedMember = member
		// A deliberate admin act on this member's capabilities supersedes whatever brought them into
		// the space, so any auto-join provenance marker is now stale. Best-effort: the role write
		// already committed, which is what governs the member's access.
		if clearErr := s.clearAutoJoined(space.Id, targetUserID); clearErr != nil {
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", targetUserID, "err", clearErr)
		}
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("SetSpaceMemberCapabilities", lockErr)
	}

	// The response is projected from the member the role update returned and the default
	// capabilities resolved inside the same lock, before the write committed. Re-reading either
	// afterward would go to a replica, which on a lagging one still carries the pre-update state and
	// would report the caller's own committed change as not having taken effect — and would leave a
	// fallible lookup after the commit, able to turn a successful write into a reported failure.
	result := toSpaceMember(updatedMember, defaultCapabilities, false)

	payload := map[string]any{"space_id": space.Id, "user_id": targetUserID}
	s.publishMembershipEvent(wsEventSpaceMemberCapabilitiesUpdated, payload, space.ChannelId, targetUserID)
	return result, nil
}

// RemoveSpaceMember removes a user from space's backing channel. Precedence: target existence
// resolves before the last-member/last-admin guards; an admin-target removal is additionally
// escalation-guarded (RequireSpaceAdminOrSysadmin) and last-admin-guarded, both under the same
// space-scoped advisory lock as SetSpaceMemberCapabilities' admin-revoke path. space is the
// caller's already-fetched record, from its gate.
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
	// concurrent promotion flip it mid-operation — so every one of them runs inside
	// the space-keyed advisory lock, alongside the mutation itself. This applies to self-removal
	// too, since the last-admin invariant covers the sole admin's self-leave.
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		// Master-backed on purpose: the plugin API's membership reads come from a replica, and the
		// admin flag read here decides whether the escalation and last-admin guards below run at
		// all — a lagging replica hiding a just-committed promotion would skip both (see the
		// rationale at the top of store/membership_store.go).
		targetAdmin, _, memErr := s.store.MemberSchemeFlags(space.ChannelId, userID)
		if memErr != nil {
			if !store.IsErrNotFound(memErr) {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
			}
			// Non-member target. Removing someone else means the caller already cleared the manage
			// gate, so the space is not hidden from them and it is a plain 404, matching
			// SetSpaceMemberCapabilities.
			//
			// Self-removal splits on ViewAccess. On a private space the caller is a non-member the
			// read gate would have denied, so the existence-hiding 403 is what they must see. On an
			// open space that gate admitted them by design and they can already read the space, so
			// there is no existence left to hide and reporting the absent membership as 404 is both
			// accurate and what the caller can act on.
			if userID != actingUserID || space.ViewAccess == model.ViewAccessOpen {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(memErr)
			}
			return existenceHidingForbidden("RemoveSpaceMember")
		}

		// Resolved before the scan below so an unauthorized caller is rejected without running it.
		if targetAdmin {
			if e := s.RequireSpaceAdminOrSysadmin("RemoveSpaceMember", space, actingUserID); e != nil {
				return e
			}
		}
		// The last-admin and last-member invariants are answered from one walk: removing an admin
		// needs both, and the admin set is a subset of the reachable set.
		hasOther, hasOtherAdmin, guardErr := s.otherAuthorizedMembers(space, userID)
		if guardErr != nil {
			// Attributed to whichever invariant the caller is actually being held to, so the failure
			// of the shared walk reports the same id each guard reported when it walked alone.
			if targetAdmin {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(guardErr)
			}
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(guardErr)
		}
		if targetAdmin && !hasOtherAdmin {
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.last_admin.app_error", nil, "", http.StatusConflict)
		}
		if !hasOther {
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.last_member.app_error", nil, "", http.StatusConflict)
		}
		if err := s.client.Channel.DeleteMember(space.ChannelId, userID); err != nil {
			if errors.Is(err, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
			}
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
		if clearErr := s.clearAutoJoined(space.Id, userID); clearErr != nil {
			// Best-effort: the membership is already gone, which is what any later check acts on; a
			// stale marker only degrades the provenance signal a future membership review reads.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", userID, "err", clearErr)
		}
		return nil
	})
	if lockErr != nil {
		// The store's own errors — notably the retryable ErrConflict a lock-acquisition timeout
		// yields — keep their conventional status codes rather than collapsing to a 500.
		return membershipLockAppError("RemoveSpaceMember", lockErr)
	}
	payload := map[string]any{"space_id": space.Id, "user_id": userID}
	s.publishMembershipEvent(wsEventSpaceMemberRemoved, payload, space.ChannelId, userID)
	return nil
}
