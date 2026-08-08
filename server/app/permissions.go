// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// ReadResolution reports how a read was admitted, so callers (notably the auto-join pre-step)
// can tell a real membership/sysadmin read from the non-member open-space fall-through.
type ReadResolution int

const (
	ReadDenied ReadResolution = iota
	ReadViaSysadmin
	ReadViaMember
	ReadViaOpenFallthrough
)

// String names the resolution, so a log field or a failed test assertion reports how the read was
// admitted rather than the underlying integer.
func (r ReadResolution) String() string {
	switch r {
	case ReadDenied:
		return "denied"
	case ReadViaSysadmin:
		return "sysadmin"
	case ReadViaMember:
		return "member"
	case ReadViaOpenFallthrough:
		return "open_fallthrough"
	default:
		return fmt.Sprintf("ReadResolution(%d)", int(r))
	}
}

// existenceHidingForbidden is the shared 403 every enforcement helper returns on a lookup miss
// or a denied check, so a caller cannot distinguish "doesn't exist", "not a member", and "no
// longer a member" by status code or message.
func existenceHidingForbidden(where string) *mmmodel.AppError {
	return mmmodel.NewAppError(where, "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden)
}

// ExistenceHidingForbidden is existenceHidingForbidden for the API layer, whose own gate helpers
// have to deny in the same indistinguishable terms. Exported so that message stays defined in one
// place: a second literal spelling of it elsewhere could drift and make the denials distinguishable.
func ExistenceHidingForbidden(where string) *mmmodel.AppError {
	return existenceHidingForbidden(where)
}

// isComplianceEnabled reports whether ComplianceSettings.Enable is set and true. When the setting
// cannot be read at all — no client, or no config — it reports true, so the fall-through this
// guards is suppressed rather than admitted: an undeterminable setting must not widen access.
func (s *Service) isComplianceEnabled() bool {
	if s.client == nil {
		return true
	}
	cfg := s.client.Configuration.GetConfig()
	if cfg == nil {
		return true
	}
	return mmmodel.SafeDereference(cfg.ComplianceSettings.Enable)
}

// hasOpenTeamFallthrough reports whether userID holds the non-member team read_public_channel
// fall-through into teamID's open spaces. Suppressed under compliance mode. member is userID's
// already-resolved membership in teamID; pass nil when the caller does not hold one and this
// resolves the team row itself.
func (s *Service) hasOpenTeamFallthrough(member *mmmodel.TeamMember, userID, teamID string) bool {
	if s.isComplianceEnabled() {
		return false
	}
	return s.teamPermGranted(member, userID, teamID, mmmodel.PermissionReadPublicChannel)
}

// readResolutionFrom evaluates the read gate against space for userID, given the caller's
// already-resolved sysadmin status and team membership. A nil member means the caller is not an
// active member of the space's team.
func (s *Service) readResolutionFrom(sysadmin bool, member *mmmodel.TeamMember, space *model.Space, userID string) ReadResolution {
	if sysadmin {
		return ReadViaSysadmin
	}
	if member != nil && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionReadPage) {
		return ReadViaMember
	}
	if space.ViewAccess == model.ViewAccessOpen && member != nil && s.hasOpenTeamFallthrough(member, userID, space.TeamId) {
		return ReadViaOpenFallthrough
	}
	return ReadDenied
}

// ResolveSpaceRead resolves the read gate for space against userID, reporting how the read was
// admitted so callers can gate auto-join to the fall-through case only. where identifies the
// calling operation for the 500 an activeTeamMember lookup failure surfaces as. On that
// failure the returned resolution is ReadDenied but the error is non-nil, so callers must check
// the error before trusting the resolution.
func (s *Service) ResolveSpaceRead(where string, space *model.Space, userID string) (ReadResolution, *mmmodel.AppError) {
	member, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return ReadDenied, appErr
	}
	return s.readResolutionFrom(sysadmin, member, space, userID), nil
}

// requireActiveMemberGate performs the checks that precede the permission-specific branches
// of RequireSpacePagePermission, RequireSpaceAdminOrTeamPerm, and RequireSpaceAdminOrSysadmin
// (RequireSpacePagePermissionFrom skips them, reusing a ReadResolution its caller already
// resolved): client wiring, a malformed-user-id 400, existence-hiding on a nil space, the sysadmin
// override, and active-team-membership resolution with its 500 on a genuine lookup failure. A
// non-nil appErr must be returned by the caller immediately. Otherwise, when sysadmin is true the
// caller may return nil immediately; when it is false the caller continues with member to evaluate
// its own permission-specific branches. A nil member means the caller is not an active team
// member; the membership itself is returned rather than a bool so a caller needing a team
// permission resolves it from these roles instead of re-reading the row.
func (s *Service) requireActiveMemberGate(where string, space *model.Space, userID string) (member *mmmodel.TeamMember, sysadmin bool, appErr *mmmodel.AppError) {
	if appErr = s.requireClient(where, "space_id", spaceIDOrEmpty(space), "user_id", userID); appErr != nil {
		return nil, false, appErr
	}
	// A malformed user id is a caller fault, not a denial: it reports as a 400 so it stays
	// distinguishable from the existence-hiding 403 every genuine denial returns.
	if !mmmodel.IsValidId(userID) {
		return nil, false, mmmodel.NewAppError(where, "app.space.access.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if space == nil {
		return nil, false, existenceHidingForbidden(where)
	}
	if s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) {
		return nil, true, nil
	}
	member, err := s.activeTeamMember(space.TeamId, userID)
	if err != nil {
		return nil, false, mmmodel.NewAppError(where, "app.space.access.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return member, false, nil
}

// RequireSpacePagePermission gates a page-scoped operation on perm: sysadmin override, then
// backing-channel membership (plus active team membership) via HasPermissionToChannel, then —
// for a read permission on an open space only — the non-member fall-through. Any other case
// (including a nil space, mirroring a lookup miss upstream) yields the shared existence-hiding
// 403.
func (s *Service) RequireSpacePagePermission(where string, space *model.Space, userID string, perm *mmmodel.Permission) *mmmodel.AppError {
	member, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	return s.evaluatePagePermission(where, space, userID, perm, member != nil, member)
}

// isGuest reports whether userID holds core's system-wide guest role. Read from the user rather
// than from the backing channel's ChannelMember.SchemeGuest: the two carry the same standing —
// a demotion stamps system_guest on the user and mirrors it onto every membership in the same
// transaction — and the user is the record that standing originates from.
func (s *Service) isGuest(userID string) (bool, error) {
	user, err := s.client.User.Get(userID)
	if err != nil {
		return false, err
	}
	return user.IsGuest(), nil
}

// evaluatePagePermission grants perm to an active member holding it on the backing channel, or —
// for a read permission on an open space only — to an active team member via the non-member
// fall-through. Any other case yields the shared existence-hiding 403. active is the caller's
// already-resolved team-membership status; member is the membership behind it when the caller
// holds one, and nil when active was established without reading the row.
//
// A guest is held to read_page whatever the composed channel permission says. Demoting a user to
// guest clears SchemeUser/SchemeAdmin but leaves the atomic capability roles (one role per granted
// capability) a prior grant wrote into ExplicitRoles, and core composes those into the member's channel permissions regardless of
// guest standing — so a gate that trusted the composed permission alone would let a demoted guest
// keep writing.
func (s *Service) evaluatePagePermission(where string, space *model.Space, userID string, perm *mmmodel.Permission, active bool, member *mmmodel.TeamMember) *mmmodel.AppError {
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, perm) {
		if perm.Id == mmmodel.PermissionReadPage.Id {
			return nil
		}
		guest, err := s.isGuest(userID)
		if err != nil {
			return mmmodel.NewAppError(where, "app.space.access.user_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
		if !guest {
			return nil
		}
		return existenceHidingForbidden(where)
	}
	if perm.Id == mmmodel.PermissionReadPage.Id && space.ViewAccess == model.ViewAccessOpen && active &&
		s.hasOpenTeamFallthrough(member, userID, space.TeamId) {
		return nil
	}
	return existenceHidingForbidden(where)
}

// RequireSpacePagePermissionFrom is RequireSpacePagePermission for a caller that has already
// resolved the read gate for the same space and user via ResolveSpaceRead, so the team-membership
// lookup behind that resolution is not repeated. admittedVia must be a non-denied resolution;
// every such resolution other than ReadViaSysadmin already established active team membership.
//
// That team-active status is carried forward, not re-verified: a revocation landing between the
// admitting read and this call is not caught until the next call. The channel-scoped check below
// is always re-resolved, because the auto-join pre-step may have changed channel membership in
// between.
func (s *Service) RequireSpacePagePermissionFrom(where string, space *model.Space, userID string, perm *mmmodel.Permission, admittedVia ReadResolution) *mmmodel.AppError {
	if admittedVia == ReadDenied {
		return existenceHidingForbidden(where)
	}
	if admittedVia == ReadViaSysadmin {
		return nil
	}
	if appErr := s.requireClient(where, "space_id", spaceIDOrEmpty(space), "user_id", userID); appErr != nil {
		return appErr
	}
	if space == nil {
		return existenceHidingForbidden(where)
	}
	return s.evaluatePagePermission(where, space, userID, perm, true, nil)
}

// ResolveSpacePageOwnOrAny decides a page operation that is granted by either of two permissions:
// a broad one covering any page, or a narrower own-page one that applies when the caller owns the
// target. delete_page with delete_own_page is the pair; move-to-space's remove-class gate is the
// other. The caller is admitted by anyPerm alone, or by ownPerm when ownerMatches. Each of the two
// attempts has to tell a denial apart from a failed lookup, which is why the sequence lives here
// instead of being repeated per handler.
//
// ownOnly reports that ownPerm was what admitted the caller. It matters where the operation
// reaches further than the one page whose owner was checked here: MovePageToSpace relocates an
// entire subtree, so an own-admitted caller must additionally be shown to own every page in it.
//
// admitted=false with a nil appErr means neither permission grants the operation. The caller
// writes its own denial, so the 403 carries the caller's operation label rather than this one's.
// A non-nil appErr is a failure of the check itself, not a denial: reporting it as a denial would
// present a backend outage to the user as "not authorized".
func (s *Service) ResolveSpacePageOwnOrAny(space *model.Space, userID, anyWhere string, anyPerm *mmmodel.Permission, ownWhere string, ownPerm *mmmodel.Permission, ownerMatches bool, admittedVia ReadResolution) (ownOnly, admitted bool, appErr *mmmodel.AppError) {
	anyErr := s.RequireSpacePagePermissionFrom(anyWhere, space, userID, anyPerm, admittedVia)
	if anyErr == nil {
		return false, true, nil
	}
	if anyErr.StatusCode != http.StatusForbidden {
		return false, false, anyErr
	}
	if !ownerMatches {
		return false, false, nil
	}
	ownErr := s.RequireSpacePagePermissionFrom(ownWhere, space, userID, ownPerm, admittedVia)
	if ownErr == nil {
		return true, true, nil
	}
	if ownErr.StatusCode != http.StatusForbidden {
		return false, false, ownErr
	}
	return false, false, nil
}

// RequireSpaceAdminOrTeamPerm gates an elevated space operation: sysadmin, channel admin_space
// (plus active team membership), or teamPerm on the space's team — the latter only once the read
// resolver has already admitted the caller, so a team-wide grant authorizes acting only on spaces
// the caller can already read. Callers pass the operation's own team permission: manage_space for
// the manage tier, delete_space for delete/restore.
func (s *Service) RequireSpaceAdminOrTeamPerm(where string, space *model.Space, userID string, teamPerm *mmmodel.Permission) *mmmodel.AppError {
	member, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	if member != nil && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionAdminSpace) {
		return nil
	}
	if s.readResolutionFrom(false, member, space, userID) != ReadDenied &&
		s.teamPermGranted(member, userID, space.TeamId, teamPerm) {
		return nil
	}
	return existenceHidingForbidden(where)
}

// RequireSpaceAdminOrSysadmin gates the space-wide exposure-policy knobs (ViewAccess, default
// capabilities) and admin-affecting member changes: sysadmin, or channel admin_space plus active
// team membership. No team-manage_space branch — those knobs are stricter than ordinary manage.
func (s *Service) RequireSpaceAdminOrSysadmin(where string, space *model.Space, userID string) *mmmodel.AppError {
	member, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	if member != nil && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionAdminSpace) {
		return nil
	}
	return existenceHidingForbidden(where)
}

// requirePageWriteFrom runs the auto-join pre-step for perm and then gates on it, the shared tail
// of every page-write authorization on an already-resolved read. joined reports whether the
// pre-step created a membership, so a caller whose guarded write then fails can undo it.
func (s *Service) requirePageWriteFrom(where string, space *model.Space, userID string, perm *mmmodel.Permission, admittedVia ReadResolution) (joined bool, appErr *mmmodel.AppError) {
	joined, appErr = s.AutoJoinIfDefaultGranted(space, userID, admittedVia, perm, nil)
	if appErr != nil {
		return false, appErr
	}
	return joined, s.RequireSpacePagePermissionFrom(where, space, userID, perm, admittedVia)
}

// RequireSpaceDraftWrite gates a draft mutation on the caller holding either page-creation or
// page-edit authority in the space. A draft is a pending page private to its author, so this
// establishes only that the caller may contribute pages here at all; the exact permission the
// content needs is enforced at publish (RequireSpacePublish), the point where the draft becomes
// state other users can see.
func (s *Service) RequireSpaceDraftWrite(where string, space *model.Space, userID string, admittedVia ReadResolution) (joined bool, appErr *mmmodel.AppError) {
	createJoined, createErr := s.requirePageWriteFrom(where, space, userID, mmmodel.PermissionCreatePage, admittedVia)
	if createErr == nil {
		return createJoined, nil
	}
	// Only a denial falls through to the second attempt: a failure of the check itself must not be
	// retried into a 403, which would present a backend outage to the user as "not authorized".
	// Mirrors ResolveSpacePageOwnOrAny's handling of the same two-attempt shape.
	if createErr.StatusCode != http.StatusForbidden {
		return createJoined, createErr
	}
	// Either attempt can have joined, so the two results are combined: the caller must be able to
	// undo a membership the create_page attempt created even when the edit_page attempt is the one
	// that admitted it.
	editJoined, editErr := s.requirePageWriteFrom(where, space, userID, mmmodel.PermissionEditPage, admittedVia)
	return createJoined || editJoined, editErr
}

// RequireSpacePublish gates publishing a draft on the permission its target actually needs:
// create_page when the draft becomes a new page, edit_page when it updates a live one. isNewPage
// comes from the publish path's own classification of the target row (a fresh draft versus one
// updating a live page).
//
// joined reports whether the auto-join pre-step created a membership; a caller whose publish then
// fails must pass it to UndoAutoJoin.
func (s *Service) RequireSpacePublish(where string, space *model.Space, userID string, admittedVia ReadResolution, isNewPage bool) (joined bool, appErr *mmmodel.AppError) {
	perm := mmmodel.PermissionEditPage
	if isNewPage {
		perm = mmmodel.PermissionCreatePage
	}
	return s.requirePageWriteFrom(where, space, userID, perm, admittedVia)
}

// DefaultRolesGrantPermission reports whether space's current default capability set (the scheme's
// generated user role) grants perm to a plain member — the auto-join admission test. ErrNotFound
// from the underlying lookup — a channel without a scheme, or one that no longer exists at all —
// reports false, not an error.
func (s *Service) DefaultRolesGrantPermission(space *model.Space, perm *mmmodel.Permission) (bool, error) {
	// Checked before the lookup below, which reaches through the client itself.
	if s.client == nil || space == nil {
		return false, nil
	}
	roles, err := s.getSchemeRolesForChannel(space.ChannelId)
	if err != nil {
		if store.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return s.client.User.RolesGrantPermission([]string{roles.UserRoleName}, perm.Id), nil
}

// spaceIDOrEmpty is a nil-safe accessor used only for log context.
func spaceIDOrEmpty(space *model.Space) string {
	if space == nil {
		return ""
	}
	return space.Id
}

// AutoJoinIfDefaultGranted is the auto-join pre-step: when a non-member's write was admitted only via
// the open-space read fall-through (admittedVia == ReadViaOpenFallthrough) and the space's
// current default capability set grants perm to a plain member, it joins userID to the backing
// channel (idempotent, and published as a membership-added event) so the subsequent write-gate
// re-check passes as a member. The
// open-read admission is re-validated before joining: a concurrent open->private flip between the
// admitting read and this pre-step aborts the join.
// ownerCheck, when non-nil, must additionally hold (used for delete_own_page, where the caller
// must already own the target page before being joined). Returns whether a join happened.
func (s *Service) AutoJoinIfDefaultGranted(space *model.Space, userID string, admittedVia ReadResolution, perm *mmmodel.Permission, ownerCheck func() (bool, error)) (bool, *mmmodel.AppError) {
	if admittedVia != ReadViaOpenFallthrough {
		return false, nil
	}
	if space == nil {
		return false, existenceHidingForbidden("AutoJoinIfDefaultGranted")
	}
	if appErr := s.requireClient("AutoJoinIfDefaultGranted", "space_id", space.Id, "user_id", userID); appErr != nil {
		return false, appErr
	}

	// A set of defaults that cannot grant perm can never admit this write, so the answer is settled
	// before taking the space-keyed lock, which holds a dedicated connection and serializes every
	// membership and scheme mutation on the space. Only a clean negative short-circuits: a failed
	// lookup falls through, leaving the in-lock check below authoritative.
	if granted, grantErr := s.DefaultRolesGrantPermission(space, perm); grantErr == nil && !granted {
		return false, nil
	}

	joined := false
	var joinedUserID, joinedChannelID string
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		fresh, getErr := s.store.GetSpace(space.Id, false)
		if getErr != nil {
			if store.IsErrNotFound(getErr) {
				return nil // Space vanished or was deleted concurrently: no join.
			}
			return mmmodel.NewAppError("AutoJoinIfDefaultGranted", "app.space.auto_join.get_space_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(getErr)
		}
		resolution, resErr := s.ResolveSpaceRead("AutoJoinIfDefaultGranted", fresh, userID)
		if resErr != nil {
			return resErr
		}
		if resolution != ReadViaOpenFallthrough {
			return nil // Re-validation failed: e.g. the space flipped private concurrently.
		}
		granted, grantErr := s.DefaultRolesGrantPermission(fresh, perm)
		if grantErr != nil {
			return grantErr
		}
		if !granted {
			return nil
		}
		if ownerCheck != nil {
			owns, ownErr := ownerCheck()
			if ownErr != nil {
				return ownErr
			}
			if !owns {
				return nil
			}
		}
		// The existence check must not miss a member: core returns the existing membership
		// unchanged for an add of a current member, so a false negative here would fall through to
		// the add below, report a join that never happened, and the undo paired with it would then
		// remove a membership the caller already held. The plugin API answers this lookup from a
		// read replica, which can miss a membership committed on the primary a moment earlier, so
		// the check reads the master through the plugin store instead. A failed lookup must not
		// fall through either — only a clean "not a member" may reach the add.
		isMember, memErr := s.store.IsChannelMember(fresh.ChannelId, userID)
		if memErr != nil {
			return mmmodel.NewAppError("AutoJoinIfDefaultGranted", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		if isMember {
			return nil // Already a member.
		}
		member, addErr := s.client.Channel.AddMember(fresh.ChannelId, userID)
		if addErr != nil {
			// A vanished user is a caller-state condition, not a server fault; AddSpaceMember
			// classifies the same failure the same way.
			if errors.Is(addErr, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("AutoJoinIfDefaultGranted", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(addErr)
			}
			return addErr
		}
		joined = true
		joinedUserID, joinedChannelID = member.UserId, fresh.ChannelId
		// Best-effort: the membership itself is what the write gate re-checks next, so a marker
		// write failure must not fail an auto-join that otherwise succeeded. It only degrades the
		// membership review's provenance signal for this member.
		if markErr := s.markAutoJoined(fresh.Id, member.UserId); markErr != nil {
			s.log.Warn("failed to record auto-join provenance marker", "space_id", fresh.Id, "user_id", member.UserId, "err", markErr)
		}
		return nil
	})
	if lockErr != nil {
		return false, membershipLockAppError("AutoJoinIfDefaultGranted", lockErr)
	}
	// Published after the lock is released, since the membership lock holds a dedicated connection
	// for its whole duration.
	if joined {
		payload := map[string]any{"space_id": space.Id, "user_id": joinedUserID}
		s.publishMembershipEvent(wsEventSpaceMemberAdded, payload, joinedChannelID, joinedUserID)
	}
	return joined, nil
}

// UndoAutoJoin removes a membership AutoJoinIfDefaultGranted created for a write that then failed,
// so a rejected request leaves no membership behind. Callers pass the joined result of the gate
// that admitted them; when it is false this does nothing.
//
// The pre-step runs ahead of the guarded write, so the rejections that land after the gate — a
// cycle or depth breach on a move, a stale optimistic-lock baseline, or the subtree-ownership 403 —
// all happen with the membership already created.
//
// Removal is best-effort and reported only in the log: the caller is already returning the write's
// own error, and replacing it with a cleanup failure would hide why the request was rejected. A
// leftover membership grants no more than the space defaults already granted the caller.
//
// Guarded by the auto-join provenance marker: the pre-step releases the membership lock before the
// guarded write it admits runs, so an admin can legitimize the same membership (a capability grant
// or a deliberate re-add, both of which clear the marker) in the window before this call re-takes
// the lock. Deleting unconditionally would then discard a membership the admin just granted, on the
// strength of a request that has nothing to do with it. The marker is checked here, not before the
// lock, so the check and the delete/clear observe one consistent state.
func (s *Service) UndoAutoJoin(joined bool, space *model.Space, userID string) {
	if !joined || space == nil || s.client == nil {
		return
	}
	removed := false
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		ids, idsErr := s.autoJoinedIDs(space.Id)
		if idsErr != nil {
			// Cannot confirm the marker either way; fail closed toward leaving the membership in
			// place rather than risking discarding one an admin just legitimized.
			s.log.Warn("failed to check auto-join provenance marker before undo; leaving the membership in place", "space_id", space.Id, "user_id", userID, "err", idsErr)
			return nil
		}
		if !slices.Contains(ids, userID) {
			s.log.Debug("auto-join undo skipped: membership was legitimized concurrently", "space_id", space.Id, "user_id", userID)
			return nil
		}
		if err := s.client.Channel.DeleteMember(space.ChannelId, userID); err != nil {
			return err
		}
		removed = true
		if clearErr := s.clearAutoJoined(space.Id, userID); clearErr != nil {
			// Best-effort, matching the marker write itself: the membership is already gone, which
			// is what the write gate re-checks; a stale marker only degrades the provenance signal.
			s.log.Warn("failed to clear auto-join provenance marker", "space_id", space.Id, "user_id", userID, "err", clearErr)
		}
		return nil
	})
	if lockErr != nil {
		s.log.Error("failed to remove the membership an auto-join created for a rejected write; the user remains a member of the space",
			"space_id", space.Id, "user_id", userID, "err", lockErr)
		return
	}
	if !removed {
		return
	}
	payload := map[string]any{"space_id": space.Id, "user_id": userID}
	s.publishMembershipEvent(wsEventSpaceMemberRemoved, payload, space.ChannelId, userID)
}
