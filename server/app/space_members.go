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
			return nil, false, s.schemeAppError("GetSpaceMembers", err)
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
		autoJoined, markerErr := s.store.GetAutoJoinedIDs(space.Id)
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
		IsAutoJoined:       autoJoined,
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
	// Reject a target who cannot pass the team half of the access gate — team read_space, which
	// core grants only through an active membership in the space's team — before touching the
	// backing channel. Core's channel-member add enforces the membership half of that check but
	// surfaces it as an opaque failure; checking here makes the status code report the real
	// cause, and guarantees every space member can reach the space — which the
	// last-authorized-member guard in RemoveSpaceMember relies on when deciding who still can.
	if !s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionReadSpace) {
		return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.not_team_member.app_error", nil, "", http.StatusForbidden)
	}
	// Adding an already-existing member is a no-op for core, which returns their membership
	// unchanged rather than erroring — this covers a deliberate re-add of a member the caller
	// joined themselves earlier. Either way it is a deliberate admin act on this membership, so the
	// auto-join marker no longer describes how they got here and is cleared; the membership itself
	// did not change on a re-add, so alreadyMember (checked before the call) decides whether
	// space_member_added fires below.
	var member *mmmodel.ChannelMember
	var defaultPermissions []string
	var alreadyMember bool
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		// Resolved inside the lock, and first, for the two reasons SetSpaceMemberPermissions
		// resolves it inside its own: a concurrent SetSpaceDefaultPermissions repoints the backing
		// channel's scheme behind this same lock, so a read taken before it could describe a
		// default set the space no longer carries by the time the response is projected from it;
		// and resolving before the add means a resolution failure is caught before anything
		// commits.
		defaults, defErr := s.spaceDefaultPermissions(space)
		if defErr != nil {
			return s.schemeAppError("AddSpaceMember", defErr)
		}
		defaultPermissions = defaults

		existing, memErr := s.store.IsChannelMember(space.ChannelId, userID)
		if memErr != nil {
			return mmmodel.NewAppError("AddSpaceMember", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		alreadyMember = existing

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
			// failed when provenance cleanup fails, but keep the stale marker visible in logs; a
			// marker surviving this failure stays subject to the next open→private prune.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", userID, "err", clearErr)
		}
		member = added
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("AddSpaceMember", lockErr)
	}
	if !alreadyMember {
		payload := map[string]any{"space_id": space.Id, "user_id": member.UserId}
		s.publishMembershipEvent(wsEventSpaceMemberAdded, payload, space.ChannelId, member.UserId)
	}
	return toSpaceMember(member, defaultPermissions, false), nil
}

// JoinOpenSpace joins userID to space's backing channel: the explicit self-join an open space
// offers a team member who can already read it but holds none of the permissions its defaults give
// a member. It is the only path that turns a space's view access into a membership.
//
// Idempotent. A caller who can already read the space as a member — or as a sysadmin, who needs no
// membership — is answered with server-resolved access rather than an error, so a client that
// cannot tell whether it has joined yet may simply call it.
//
// Admission is the open-space read fall-through and nothing else: the caller must be an active
// team member whose read of this space resolved through ViewAccessOpen rather than through a
// membership. A guest cannot reach it — the fall-through requires read_public_channel on the team
// and core's team_guest role carries only view_team and read_space — so no explicit guest clamp is
// needed here. A space whose defaults confer nothing beyond the read every reader already has is
// refused, since joining would grant exactly what the caller had without it.
//
// The read and the defaults are re-resolved inside the space membership lock, so an open->private
// flip or a default narrowed to read-only racing this call aborts the join rather than admitting
// on a setting that no longer holds.
func (s *Service) JoinOpenSpace(space *model.Space, userID string) (*model.SpaceWithAccess, *mmmodel.AppError) {
	if space == nil {
		return nil, ExistenceHidingForbidden("JoinOpenSpace")
	}
	if appErr := s.requireClient("JoinOpenSpace", "space_id", space.Id, "user_id", userID); appErr != nil {
		return nil, appErr
	}

	admittedVia, appErr := s.ResolveSpaceRead("JoinOpenSpace", space, userID)
	if appErr != nil {
		return nil, appErr
	}
	switch admittedVia {
	case ReadDenied:
		return nil, ExistenceHidingForbidden("JoinOpenSpace")
	case ReadViaSysadmin, ReadViaMember:
		return s.BuildSpaceWithAccess(space, userID)
	case ReadViaOpenFallthrough:
	}

	joined := false
	var joinedChannelID string
	var joinedMember *mmmodel.ChannelMember
	var latestSpace *model.Space
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		fresh, getErr := s.store.GetSpace(space.Id, false)
		if getErr != nil {
			if store.IsErrNotFound(getErr) {
				// Deleted concurrently: the space the caller asked to join is gone, and it must not
				// be distinguishable from one they were never allowed to see.
				return ExistenceHidingForbidden("JoinOpenSpace")
			}
			return mmmodel.NewAppError("JoinOpenSpace", "app.space.auto_join.get_space_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(getErr)
		}
		latestSpace = fresh
		resolution, resErr := s.ResolveSpaceRead("JoinOpenSpace", fresh, userID)
		if resErr != nil {
			return resErr
		}
		if resolution != ReadViaOpenFallthrough {
			// The space flipped private, or the caller became a member, between the read above and
			// this one. Either way there is nothing left for this call to do.
			return nil
		}
		defaults, defErr := s.spaceDefaultPermissions(fresh)
		if defErr != nil {
			return s.schemeAppError("JoinOpenSpace", defErr)
		}
		if len(defaults) == 0 {
			return mmmodel.NewAppError("JoinOpenSpace", "app.space.join.nothing_to_grant.app_error", nil, "", http.StatusForbidden)
		}
		// The existence check must not miss a member: core returns the existing membership
		// unchanged for an add of a current member, so a false negative would mark a member who
		// joined deliberately as having joined themselves. The plugin API answers this lookup from
		// a read replica, which can miss a membership committed on the primary a moment earlier, so
		// the check reads the master through the plugin store instead.
		isMember, memErr := s.store.IsChannelMember(fresh.ChannelId, userID)
		if memErr != nil {
			return mmmodel.NewAppError("JoinOpenSpace", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		if isMember {
			return nil
		}
		// The write is an idempotent upsert, so a retry of a join that already marked is a no-op.
		// A marker with no membership grants nothing: every gate derives authority from real
		// channel membership, never from this table, which only records how a member got here.
		if markErr := s.store.MarkAutoJoined(fresh.Id, userID); markErr != nil {
			return mmmodel.NewAppError("JoinOpenSpace", "app.space.auto_join.provenance_write_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(markErr)
		}
		added, addErr := s.client.Channel.AddMember(fresh.ChannelId, userID)
		if addErr != nil {
			// The marker describes a membership that does not exist, which no gate consults, but it
			// would mislead a membership review, so it is cleared. The failure is returned to the
			// caller who asked to join, so they can retry it.
			if clearErr := s.store.ClearAutoJoined(fresh.Id, userID); clearErr != nil {
				s.log.Warn("failed to clear the auto-join provenance marker of a join that did not happen",
					"space_id", fresh.Id, "user_id", userID, "err", clearErr)
			}
			// A vanished user is a caller-state condition, not a server fault; AddSpaceMember
			// classifies the same failure the same way.
			if errors.Is(addErr, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("JoinOpenSpace", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(addErr)
			}
			return mmmodel.NewAppError("JoinOpenSpace", "app.space.auto_join.add_member_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(addErr)
		}
		joined = true
		joinedChannelID = fresh.ChannelId
		joinedMember = added
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("JoinOpenSpace", lockErr)
	}
	// Published after the lock is released, since the membership lock holds a dedicated connection
	// for its whole duration.
	if joined {
		payload := map[string]any{"space_id": space.Id, "user_id": userID}
		s.publishMembershipEvent(wsEventSpaceMemberAdded, payload, joinedChannelID, userID)
	}
	// The response for a join this request performed is projected from the membership core returned
	// for its own write, not from a re-read: the plugin API's member lookup is answered from a read
	// replica, which can miss the row committed on the primary an instant earlier and would turn a
	// committed join into a reported failure. The not-joined paths keep the ordinary lookup.
	return s.buildSpaceWithAccess(latestSpace, userID, nil, joinedMember)
}

// PruneSelfJoinedMembers removes every membership currently carrying auto-join provenance from
// space's backing channel: the removal pass of an open-to-private transition. Deliberate
// membership changes normally clear that marker. Unlike the other membership mutations in this
// file, this one runs without the space membership lock held, since each removal is its own core
// RPC. A removal failure returns the members removed so far and leaves the failed marker in place
// for a later pass; successfully removed memberships stay removed.
func (s *Service) PruneSelfJoinedMembers(space *model.Space) ([]string, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("PruneSelfJoinedMembers", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("PruneSelfJoinedMembers", "space_id", space.Id); appErr != nil {
		return nil, appErr
	}
	selfJoined, err := s.store.GetAutoJoinedIDs(space.Id)
	if err != nil {
		return nil, mmmodel.NewAppError("PruneSelfJoinedMembers", "app.space.prune_self_joined.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return s.removeAutoJoinedMembers(space, selfJoined)
}

// removeAutoJoinedMembers is PruneSelfJoinedMembers for a caller that already snapshotted the
// auto-joined user ids (UpdateSpace takes that snapshot under the space membership lock, then
// calls this without holding it, since the per-member Channel.DeleteMember RPCs below must not run
// while the lock's dedicated connection is held).
func (s *Service) removeAutoJoinedMembers(space *model.Space, userIDs []string) ([]string, *mmmodel.AppError) {
	removed := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
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
	audience, err := s.resolveSpaceAudience(space.ChannelId)
	if err != nil {
		return mmmodel.NewAppError(where, "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	if _, otherAdmin := audience.others(excludeUserID); !otherAdmin {
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
			return s.schemeAppError("SetSpaceMemberPermissions", rolesErr)
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
			return s.schemeAppError("SetSpaceMemberPermissions", defErr)
		}
		defaultPermissions = defaults

		// Master-backed on purpose: this flag decides whether the escalation and last-admin guards
		// below run at all (see store/membership_store.go for why membership reads here bypass the
		// replica).
		targetAdmin, targetGuest, memErr := s.store.GetMemberSchemeFlags(space.ChannelId, targetUserID)
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
		// committed role update as rejected; a marker surviving this failure stays subject to the
		// next open→private prune.
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
		// Master-backed on purpose: this flag decides whether the escalation and last-admin guards
		// below run at all (see store/membership_store.go for why membership reads here bypass the
		// replica).
		targetAdmin, _, memErr := s.store.GetMemberSchemeFlags(space.ChannelId, userID)
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
		// The last-admin and last-member invariants are answered from one audience resolution:
		// removing an admin needs both, and the admin set is a subset of the reachable set.
		audience, guardErr := s.resolveSpaceAudience(space.ChannelId)
		if guardErr != nil {
			// Attributed to whichever invariant the caller is actually being held to, so the failure
			// of the shared walk reports the same id each guard reported when it walked alone.
			if targetAdmin {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.admin_count_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(guardErr)
			}
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(guardErr)
		}
		hasOther, hasOtherAdmin := audience.others(userID)
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
