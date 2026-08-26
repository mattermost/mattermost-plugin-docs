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
// it. page/perPage are normalized like every other paginated method (page and perPage both
// clamped). The pluginapi member
// listing is page-indexed rather than offset-based, so when the requested page comes back full a
// one-row probe at the next page's first slot decides has-more. space is the caller's
// already-fetched record, from its read gate.
//
// withPermissions selects the projection. The route admits anyone who can read the space, so that
// its member count and avatars render for an ordinary member, but the per-member permission matrix
// is management state: it is emitted only to a caller who holds the manage tier the membership
// writes require. Without it the roster reports who is in the space and which of them administer
// it — what core lets any channel member see of an ordinary channel — and the state behind the
// matrix is never looked up rather than looked up and discarded.
func (s *Service) GetSpaceMembers(space *model.Space, page, perPage int, withPermissions bool) ([]*model.SpaceMember, bool, *mmmodel.AppError) {
	if space == nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("GetSpaceMembers", "space_id", space.Id); appErr != nil {
		return nil, false, appErr
	}
	var defaultPermissions []string
	if withPermissions {
		resolved, err := s.spaceDefaultPermissions(space)
		if err != nil {
			return nil, false, schemeAppError("GetSpaceMembers", err)
		}
		defaultPermissions = resolved
	}
	page = ClampPage(page)
	perPage = ClampPerPage(perPage)
	channelMembers, err := s.client.Channel.ListMembers(space.ChannelId, page, perPage)
	if err != nil {
		return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	autoJoinedSet := map[string]struct{}{}
	if withPermissions {
		// One lookup for the whole page rather than one per member: the markers are per-membership
		// rows, so reading the space's set once and testing each member against it in memory costs a
		// single query instead of perPage of them.
		autoJoined, markerErr := s.store.AutoJoinedIDs(space.Id)
		if markerErr != nil {
			return nil, false, mmmodel.NewAppError("GetSpaceMembers", "app.space.get_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(markerErr)
		}
		autoJoinedSet = make(map[string]struct{}, len(autoJoined))
		for _, id := range autoJoined {
			autoJoinedSet[id] = struct{}{}
		}
	}
	members := make([]*model.SpaceMember, 0, len(channelMembers))
	for _, cm := range channelMembers {
		if !withPermissions {
			members = append(members, toRedactedSpaceMember(cm))
			continue
		}
		_, wasAutoJoined := autoJoinedSet[cm.UserId]
		members = append(members, toSpaceMember(cm, defaultPermissions, wasAutoJoined))
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

// toRedactedSpaceMember builds the roster entry a caller without the manage tier receives: identity
// and role standing, with no permission matrix and no auto-join provenance. Both flags come straight
// off the membership, so this needs neither the space's default permission set nor the marker table.
func toRedactedSpaceMember(cm *mmmodel.ChannelMember) *model.SpaceMember {
	member := &model.SpaceMember{
		UserId:  cm.UserId,
		IsAdmin: cm.SchemeAdmin,
		IsGuest: cm.SchemeGuest,
	}
	member.EnsurePermissions()
	return member
}

// toSpaceMember builds the wire representation of a channel member's permission state.
// autoJoined reports whether the member carries the auto-join provenance marker.
func toSpaceMember(cm *mmmodel.ChannelMember, defaultPermissions []string, autoJoined bool) *model.SpaceMember {
	mc := model.PermissionsFromMember(cm.ExplicitRoles, cm.SchemeAdmin, cm.SchemeGuest, defaultPermissions)
	member := &model.SpaceMember{
		UserId:             cm.UserId,
		Permissions:        mc.Effective,
		GrantedPermissions: mc.Granted,
		IsAdmin:            mc.IsAdmin,
		IsGuest:            mc.IsGuest,
		AutoJoined:         autoJoined,
	}
	member.EnsurePermissions()
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
	// it as an opaque failure; checking here makes the status code report the real cause, and
	// guarantees every space member can pass the team half of the access gate — which the
	// last-authorized-member guard in RemoveSpaceMember relies on when deciding who can still
	// reach the space.
	active, memberErr := s.isActiveTeamMember(space.TeamId, userID)
	if memberErr != nil {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
	}
	if !active {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.not_team_member.app_error", nil, "", http.StatusForbidden)
	}
	// Adding an already-existing member is a no-op for core, which returns their membership
	// unchanged rather than erroring — so this also covers a deliberate re-add of a member the
	// caller joined themselves earlier. Either way it is a deliberate admin act on this membership,
	// so the auto-join marker no longer describes how they got here and is cleared.
	var member *mmmodel.ChannelMember
	var defaultPermissions []string
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		// Resolved inside the lock, and first, for the two reasons SetSpaceMemberPermissions
		// resolves it inside its own: a concurrent SetSpaceDefaultPermissions repoints the backing
		// channel's scheme behind this same lock, so a read taken before it could describe a
		// default set the space no longer carries by the time the response is projected from it;
		// and resolving before the add means a resolution failure is caught before anything
		// commits.
		defaults, defErr := s.spaceDefaultPermissions(space)
		if defErr != nil {
			return schemeAppError("AddSpaceMember", defErr)
		}
		defaultPermissions = defaults

		added, addErr := s.client.Channel.AddMember(space.ChannelId, userID)
		if addErr != nil {
			// A missing target user is the caller's mistake, not a server fault.
			if errors.Is(addErr, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("AddSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(addErr)
			}
			return mmmodel.NewAppError("AddSpaceMember", "app.space.add_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(addErr)
		}
		if clearErr := s.store.ClearAutoJoined(space.Id, userID); clearErr != nil {
			// The membership is now deliberate. Do not report that successful core mutation as
			// failed when provenance cleanup fails, but keep the stale marker visible in logs.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", userID, "err", clearErr)
		}
		member = added
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("AddSpaceMember", lockErr)
	}
	payload := map[string]any{"space_id": space.Id, "user_id": member.UserId}
	s.publishMembershipEvent(wsEventSpaceMemberAdded, payload, space.ChannelId, member.UserId)
	return toSpaceMember(member, defaultPermissions, false), nil
}

// PruneSelfJoinedMembers removes memberships currently carrying auto-join provenance before an
// open-to-private transition. Deliberate membership changes normally clear that marker. Callers
// hold the space membership lock, so a genuine removal failure aborts the transition and leaves the
// failed marker in place for a retry. Successfully removed memberships stay removed even if a later
// removal fails; they are safe while the space remains open and their users can explicitly rejoin.
func (s *Service) PruneSelfJoinedMembers(space *model.Space) ([]string, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("PruneSelfJoinedMembers", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("PruneSelfJoinedMembers", "space_id", space.Id); appErr != nil {
		return nil, appErr
	}
	selfJoined, err := s.store.AutoJoinedIDs(space.Id)
	if err != nil {
		return nil, mmmodel.NewAppError("PruneSelfJoinedMembers", "app.space.prune_self_joined.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}

	removed := make([]string, 0, len(selfJoined))
	for _, userID := range selfJoined {
		if delErr := s.client.Channel.DeleteMember(space.ChannelId, userID); delErr != nil {
			if !errors.Is(delErr, pluginapi.ErrNotFound) {
				s.log.Error("failed to remove a self-joined member; leaving the space open",
					"space_id", space.Id, "user_id", userID, "err", delErr)
				return removed, mmmodel.NewAppError("PruneSelfJoinedMembers", "app.space.prune_self_joined.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(delErr)
			}
			// Already gone. The marker is stale and still needs clearing below.
		}
		if clearErr := s.store.ClearAutoJoined(space.Id, userID); clearErr != nil {
			// Best-effort, as everywhere else this marker is cleared: the membership is gone, which
			// is what any later check acts on, and a stale marker grants nothing.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", userID, "err", clearErr)
		}
		removed = append(removed, userID)
	}
	return removed, nil
}

// requireNotLastAdmin rejects an operation that would leave space without an admin who can still
// reach it, disregarding excludeUserID (the member being demoted or removed). Callers run it inside
// the space-keyed membership lock, alongside the mutation it guards. where attributes both the
// lookup failure and the rejection to the calling operation.
func (s *Service) requireNotLastAdmin(where string, space *model.Space, excludeUserID string) *mmmodel.AppError {
	_, otherAdmin, err := s.store.OtherAuthorizedMembers(space.ChannelId, space.TeamId, excludeUserID)
	if err != nil {
		return mmmodel.NewAppError(where, "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	if !otherAdmin {
		return mmmodel.NewAppError(where, "app.space.member.last_admin.app_error", nil, "", http.StatusConflict)
	}
	return nil
}

// SetSpaceMemberPermissions replaces targetUserID's per-member granted permission set. Callers
// must already hold manage-tier authority over the space (RequireSpaceAdminOrTeamPerm with
// manage_space); this method
// additionally enforces the self/admin escalation guard and the last-admin invariant. Guest
// members are rejected: they stay read-only via the scheme's guest role. actingUserID is the
// caller, used only to decide the self-escalation guard.
func (s *Service) SetSpaceMemberPermissions(space *model.Space, targetUserID string, permissions []string, actingUserID string) (*model.SpaceMember, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("SetSpaceMemberPermissions", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(targetUserID) {
		return nil, mmmodel.NewAppError("SetSpaceMemberPermissions", "app.space.member.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := model.ValidateGrantedPermissions(permissions); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("SetSpaceMemberPermissions", "space_id", space.Id, "user_id", targetUserID); appErr != nil {
		return nil, appErr
	}

	requestedAdmin := slices.Contains(permissions, mmmodel.PermissionAdminSpace.Id)
	selfTargeted := targetUserID == actingUserID

	// The scheme-role read, the target's current admin status, the escalation-guard decision, and
	// the admin count all read state that a concurrent SetSpaceDefaultPermissions (repoints the
	// channel's scheme) or SetSpaceMemberPermissions/RemoveSpaceMember (the last-admin invariant)
	// call could change: a stale read taken before the lock lets a concurrent default-permissions
	// repoint write a superseded scheme's role name, or a concurrent promotion/demotion flip admin
	// status, mid-operation — so every one of them runs inside the space-keyed
	// advisory lock, alongside the mutation itself.
	var newRoles string
	var newSchemeAdmin bool
	var defaultPermissions []string
	var updatedMember *mmmodel.ChannelMember
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		resolvedRoles, rolesErr := s.getSchemeRolesForChannel(space.ChannelId)
		if rolesErr != nil {
			return schemeAppError("SetSpaceMemberPermissions", rolesErr)
		}
		newRoles, newSchemeAdmin = model.RolesForPermissions(permissions, resolvedRoles.UserRoleName)
		// Resolved here, before the write below commits, rather than after the lock: the space's
		// default permission set does not change during a member-permission write (only
		// SetSpaceDefaultPermissions changes it, serialized behind this same lock), so nothing about
		// resolving it depends on the write having happened. Doing it here means a failure here is
		// caught before anything commits, instead of surfacing a committed write as a 500 and
		// silently skipping the WS event below.
		defaults, defErr := s.defaultPermissionsForRoles(resolvedRoles)
		if defErr != nil {
			return schemeAppError("SetSpaceMemberPermissions", defErr)
		}
		defaultPermissions = defaults

		// Master-backed on purpose: the plugin API's membership reads come from a replica, and the
		// admin flag read here decides whether the escalation and last-admin guards below run at
		// all — a lagging replica hiding a just-committed promotion would skip both (see the
		// rationale at the top of store/membership_store.go).
		targetAdmin, targetGuest, memErr := s.store.MemberSchemeFlags(space.ChannelId, targetUserID)
		if memErr != nil {
			if store.IsErrNotFound(memErr) {
				return mmmodel.NewAppError("SetSpaceMemberPermissions", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(memErr)
			}
			return mmmodel.NewAppError("SetSpaceMemberPermissions", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		if targetGuest {
			return mmmodel.NewAppError("SetSpaceMemberPermissions", "app.space.member.guest_not_assignable.app_error", nil, "", http.StatusBadRequest)
		}

		adminAffected := targetAdmin || requestedAdmin
		if adminAffected || selfTargeted {
			if e := s.RequireSpaceAdminOrSysadmin("SetSpaceMemberPermissions", space, actingUserID); e != nil {
				return e
			}
		}

		if targetAdmin && !newSchemeAdmin {
			if e := s.requireNotLastAdmin("SetSpaceMemberPermissions", space, targetUserID); e != nil {
				return e
			}
		}

		roles := newRoles
		if newSchemeAdmin {
			roles = roles + " " + resolvedRoles.AdminRoleName
		}
		member, updErr := s.client.Channel.UpdateChannelMemberRoles(space.ChannelId, targetUserID, roles)
		if updErr != nil {
			return mmmodel.NewAppError("SetSpaceMemberPermissions", "app.space.member.update_permissions_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updErr)
		}
		// Only a successful deliberate permission change supersedes auto-join provenance. Cleanup
		// cannot be atomic with core's role mutation, so log a failure without misreporting the
		// committed role update as rejected.
		if clearErr := s.store.ClearAutoJoined(space.Id, targetUserID); clearErr != nil {
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", targetUserID, "err", clearErr)
		}
		updatedMember = member
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("SetSpaceMemberPermissions", lockErr)
	}

	// Project from the member returned by the role update and the defaults resolved under the same
	// lock. A post-write replica read could return pre-update state or turn a committed write into a
	// reported failure.
	result := toSpaceMember(updatedMember, defaultPermissions, false)

	payload := map[string]any{"space_id": space.Id, "user_id": targetUserID}
	s.publishMembershipEvent(wsEventSpaceMemberPermissionsUpdated, payload, space.ChannelId, targetUserID)
	return result, nil
}

// RemoveSpaceMember removes a user from space's backing channel. Precedence: target existence
// resolves before the last-member/last-admin guards; an admin-target removal is additionally
// escalation-guarded (RequireSpaceAdminOrSysadmin) and last-admin-guarded, both under the same
// space-scoped advisory lock as SetSpaceMemberPermissions' admin-revoke path. space is the
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
	// read state a concurrent SetSpaceMemberPermissions/RemoveSpaceMember call could change
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
			// SetSpaceMemberPermissions.
			//
			// Self-removal splits on ViewAccess. On a private space the caller is a non-member the
			// read gate would have denied, so the existence-hiding 403 is what they must see. On an
			// open space that gate admitted them by design and they can already read the space, so
			// there is no existence left to hide and reporting the absent membership as 404 is both
			// accurate and what the caller can act on.
			if userID != actingUserID || space.ViewAccess == model.ViewAccessOpen {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(memErr)
			}
			return ExistenceHidingForbidden("RemoveSpaceMember")
		}

		// Resolved before the scan below so an unauthorized caller is rejected without running it.
		if targetAdmin {
			if e := s.RequireSpaceAdminOrSysadmin("RemoveSpaceMember", space, actingUserID); e != nil {
				return e
			}
		}
		// The last-admin and last-member invariants are answered from one walk: removing an admin
		// needs both, and the admin set is a subset of the reachable set.
		hasOther, hasOtherAdmin, guardErr := s.store.OtherAuthorizedMembers(space.ChannelId, space.TeamId, userID)
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
		if clearErr := s.store.ClearAutoJoined(space.Id, userID); clearErr != nil {
			// The membership is already gone; provenance cleanup must not reverse that success.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", userID, "err", clearErr)
		}
		return nil
	})
	if lockErr != nil {
		// The store's own errors — notably the retryable ErrLockTimeout a lock-acquisition timeout
		// yields — keep their conventional status codes rather than collapsing to a 500.
		return membershipLockAppError("RemoveSpaceMember", lockErr)
	}
	payload := map[string]any{"space_id": space.Id, "user_id": userID}
	s.publishMembershipEvent(wsEventSpaceMemberRemoved, payload, space.ChannelId, userID)
	return nil
}
