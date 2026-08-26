// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// ReadResolution reports how a read was admitted, so callers can tell a real membership/sysadmin
// read from the non-member open-space fall-through — the distinction JoinOpenSpace admits on.
type ReadResolution int

const (
	ReadDenied ReadResolution = iota
	ReadViaSysadmin
	ReadViaMember
	ReadViaOpenFallthrough
)

// ExistenceHidingForbidden is the shared 403 every enforcement helper returns on a lookup miss or
// a denied check, so a caller cannot distinguish "doesn't exist", "not a member", and "no longer a
// member" by status code or message. Exported because the API layer's own gate helpers have to
// deny in the same indistinguishable terms, and a second literal spelling of the message there
// could drift.
func ExistenceHidingForbidden(where string) *mmmodel.AppError {
	return mmmodel.NewAppError(where, "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden)
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
// fall-through into teamID's open spaces. Suppressed under compliance mode.
func (s *Service) hasOpenTeamFallthrough(userID, teamID string) bool {
	if s.isComplianceEnabled() {
		return false
	}
	return s.client.User.HasPermissionToTeam(userID, teamID, mmmodel.PermissionReadPublicChannel)
}

// readResolutionFrom evaluates the read gate against space for userID, given the caller's
// already-resolved sysadmin status and team membership. A nil member means the caller is not an
// active member of the space's team.
//
// Team read_space gates both branches below, the same permission the team listing requires.
// Without it here, revoking read_space would leave every space still reachable by id to anyone
// holding read_page on its backing channel, so the permission would govern discovery alone while
// the admin console offers it as access. Both team_user and team_guest hold it by default.
func (s *Service) readResolutionFrom(sysadmin bool, member *mmmodel.TeamMember, space *model.Space, userID string) ReadResolution {
	if sysadmin {
		return ReadViaSysadmin
	}
	if member == nil || !s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionReadSpace) {
		return ReadDenied
	}
	if s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionReadPage) {
		return ReadViaMember
	}
	if space.ViewAccess == model.ViewAccessOpen && s.hasOpenTeamFallthrough(userID, space.TeamId) {
		return ReadViaOpenFallthrough
	}
	return ReadDenied
}

// ResolveSpaceRead resolves the read gate for space against userID, reporting how the read was
// admitted so callers can tell the fall-through case apart. where identifies the
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

// requireActiveMemberGate performs the checks that precede the permission-specific branches of
// every exported gate: client wiring, a malformed-user-id 400, existence-hiding on a nil space,
// the sysadmin override, and active-team-membership resolution with its 500 on a genuine lookup
// failure.
//
// A non-nil appErr must be returned by the caller immediately; sysadmin=true means the caller may
// admit without further checks. A nil member means the caller is not an active team member. The
// membership itself is returned rather than a bool so a caller needing a team permission resolves
// it from these roles instead of re-reading the row.
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
		return nil, false, ExistenceHidingForbidden(where)
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
	return s.evaluatePagePermission(where, space, userID, perm, member != nil)
}

// resolveGuest reports whether userID holds core's system-wide guest role.
//
// Read from the user rather than from the backing channel's ChannelMember.SchemeGuest: the two
// carry the same standing — a demotion stamps system_guest on the user and mirrors it onto every
// membership in the same transaction — and the user is the record that standing originates from.
func (s *Service) resolveGuest(userID string) (bool, error) {
	user, err := s.client.User.Get(userID)
	if err != nil {
		return false, err
	}
	return user.IsGuest(), nil
}

// evaluatePagePermission grants perm to an active member holding it on the backing channel, or —
// for a read permission on an open space only — to an active team member via the non-member
// fall-through. Any other case yields the shared existence-hiding 403. active is the caller's
// already-resolved team-membership status.
//
// A guest is held to read_page whatever the composed channel permission says. Demoting a user to
// guest clears SchemeUser/SchemeAdmin but leaves the capability roles a prior grant wrote
// into ExplicitRoles, and core composes those into the member's channel permissions regardless of
// guest standing — so a gate trusting the composed permission alone would let a demoted guest keep
// writing.
func (s *Service) evaluatePagePermission(where string, space *model.Space, userID string, perm *mmmodel.Permission, active bool) *mmmodel.AppError {
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, perm) {
		if perm.Id == mmmodel.PermissionReadPage.Id {
			return nil
		}
		guest, err := s.resolveGuest(userID)
		if err != nil {
			return mmmodel.NewAppError(where, "app.space.access.user_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
		if !guest {
			return nil
		}
		return ExistenceHidingForbidden(where)
	}
	if perm.Id == mmmodel.PermissionReadPage.Id && space.ViewAccess == model.ViewAccessOpen && active &&
		s.hasOpenTeamFallthrough(userID, space.TeamId) {
		return nil
	}
	return ExistenceHidingForbidden(where)
}

// requireSpacePagePermissionFrom is RequireSpacePagePermission for a caller that resolved the read
// gate for the same space and user moments earlier, inside the same operation. admittedVia must be
// a non-denied resolution; every such resolution other than ReadViaSysadmin already established
// active team membership, and resolving it ran the client-wiring and nil-space checks this
// therefore does not repeat.
//
// Unexported, and every caller resolves admittedVia itself rather than accepting one from its own
// caller: the team-active status it stands for is carried forward rather than re-verified, so the
// resolution must be spent in the same breath it was taken. A resolution handed across a request —
// taken at one gate and spent at another after the handler has done other work — widens that into a
// window a revocation can land in.
func (s *Service) requireSpacePagePermissionFrom(where string, space *model.Space, userID string, perm *mmmodel.Permission, admittedVia ReadResolution) *mmmodel.AppError {
	if admittedVia == ReadDenied {
		return ExistenceHidingForbidden(where)
	}
	if admittedVia == ReadViaSysadmin {
		return nil
	}
	return s.evaluatePagePermission(where, space, userID, perm, true)
}

// ResolveSpaceRemovePage decides a page operation granted by either delete_page, covering any
// page, or delete_own_page when the caller owns the target. Two operations need it: deleting a
// page, and the source side of a move to another space, which removes the page from the source
// just as a delete would. Each of the two attempts has to tell a denial apart from a failed
// lookup, which is why the sequence lives here instead of being repeated per handler.
//
// ownOnly reports that delete_own_page was what admitted the caller. It matters where the
// operation reaches further than the one page whose owner was checked here: MovePageToSpace
// relocates an entire subtree, so an own-admitted caller must additionally be shown to own every
// page in it.
//
// anyWhere and ownWhere label the two attempts for the caller's own operation.
//
// admitted=false with a nil appErr means neither permission grants the operation. The caller
// writes its own denial, so the 403 carries the caller's operation label rather than this one's.
// A non-nil appErr is a failure of the check itself, not a denial: reporting it as a denial would
// present a backend outage to the user as "not authorized".
func (s *Service) ResolveSpaceRemovePage(space *model.Space, userID, anyWhere, ownWhere string, ownerMatches bool) (ownOnly, admitted bool, appErr *mmmodel.AppError) {
	admittedVia, readErr := s.resolveReadForWrite(anyWhere, space, userID)
	if readErr != nil {
		return false, false, readErr
	}
	anyErr := s.requireSpacePagePermissionFrom(anyWhere, space, userID, mmmodel.PermissionDeletePage, admittedVia)
	if anyErr == nil {
		return false, true, nil
	}
	if anyErr.StatusCode != http.StatusForbidden {
		return false, false, anyErr
	}
	if !ownerMatches {
		return false, false, nil
	}
	ownErr := s.requireSpacePagePermissionFrom(ownWhere, space, userID, mmmodel.PermissionDeleteOwnPage, admittedVia)
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
	if s.adminOrTeamPermFrom(sysadmin, member, space, userID, teamPerm) {
		return nil
	}
	return ExistenceHidingForbidden(where)
}

// adminOrTeamPermFrom is RequireSpaceAdminOrTeamPerm's decision, evaluated against an
// already-resolved sysadmin status and team membership so a caller answering two questions about
// the same space resolves that membership once. Ordered so the team lookup only runs for a caller
// who is neither sysadmin nor space admin.
func (s *Service) adminOrTeamPermFrom(sysadmin bool, member *mmmodel.TeamMember, space *model.Space, userID string, teamPerm *mmmodel.Permission) bool {
	if sysadmin {
		return true
	}
	if member != nil && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionAdminSpace) {
		return true
	}
	return s.readResolutionFrom(false, member, space, userID) != ReadDenied &&
		s.client.User.HasPermissionToTeam(userID, space.TeamId, teamPerm)
}

// ResolveSpaceRosterAccess gates the member listing and reports whether the caller additionally
// holds the manage tier that selects the unredacted projection. Both answers come from one
// requireActiveMemberGate, since the roster route asks about the same space and user twice: it
// admits anyone who can read the space, and emits the permission matrix only to a manager.
//
// A denied read is the shared existence-hiding 403. A caller admitted for the read but short of the
// manage tier is not an error — canManage=false selects the redacted projection.
func (s *Service) ResolveSpaceRosterAccess(where string, space *model.Space, userID string) (canManage bool, appErr *mmmodel.AppError) {
	member, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return false, appErr
	}
	if s.readResolutionFrom(sysadmin, member, space, userID) == ReadDenied {
		return false, ExistenceHidingForbidden(where)
	}
	return s.adminOrTeamPermFrom(sysadmin, member, space, userID, mmmodel.PermissionManageSpace), nil
}

// RequireSpaceAdminOrSysadmin gates the space-wide exposure-policy knobs (ViewAccess, default
// permissions) and admin-affecting member changes: sysadmin, or channel admin_space plus active
// team membership. No team-manage_space branch — those knobs are stricter than ordinary manage.
func (s *Service) RequireSpaceAdminOrSysadmin(where string, space *model.Space, userID string) *mmmodel.AppError {
	member, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	if member != nil {
		// The SchemeAdmin flag is read from the master rather than composed through
		// HasPermissionToChannel, which answers from GetAllChannelMembersForUser's cache. Every
		// caller that re-runs this gate inside the space-membership lock does so to catch a
		// demotion that landed while it waited, and a cached composition can still report the
		// pre-demotion roles — so the re-check would admit exactly the actor it exists to exclude.
		//
		// The flag is the whole signal for admin_space: RolesForPermissions turns an admin_space
		// grant into SchemeAdmin rather than an ExplicitRoles token, PermissionsFromMember derives
		// the permission back from that flag alone, and the permission is rejected as a space
		// default and carried by no team or system role. A non-member has no row and is denied.
		schemeAdmin, _, flagErr := s.store.MemberSchemeFlags(space.ChannelId, userID)
		switch {
		case flagErr == nil && schemeAdmin:
			return nil
		case flagErr != nil && !errors.As(flagErr, new(*store.ErrNotFound)):
			return mmmodel.NewAppError(where, "app.space.access.member_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(flagErr)
		}
	}
	return ExistenceHidingForbidden(where)
}

// RequireSpacePageWrite gates a page write on perm: it resolves the read gate — a caller cannot be
// granted write authority over a space it cannot read — and then resolves perm against the
// caller's membership.
//
// Resolve the read inside the service operation instead of accepting a reusable authorization
// result from the caller.
func (s *Service) RequireSpacePageWrite(where string, space *model.Space, userID string, perm *mmmodel.Permission) *mmmodel.AppError {
	admittedVia, appErr := s.resolveReadForWrite(where, space, userID)
	if appErr != nil {
		return appErr
	}
	return s.requireSpacePagePermissionFrom(where, space, userID, perm, admittedVia)
}

// resolveReadForWrite resolves the read gate that precedes every write gate, mapping a denied read
// to the shared existence-hiding 403.
func (s *Service) resolveReadForWrite(where string, space *model.Space, userID string) (ReadResolution, *mmmodel.AppError) {
	admittedVia, appErr := s.ResolveSpaceRead(where, space, userID)
	if appErr != nil {
		return ReadDenied, appErr
	}
	if admittedVia == ReadDenied {
		return ReadDenied, ExistenceHidingForbidden(where)
	}
	return admittedVia, nil
}

// RequireSpaceDraftWrite gates a draft mutation on the caller holding either page-creation or
// page-edit authority in the space. A draft is a pending page private to its author, so this
// establishes only that the caller may contribute pages here at all; the exact permission the
// content needs is enforced at publish (RequireSpacePublish), the point where the draft becomes
// state other users can see.
func (s *Service) RequireSpaceDraftWrite(where string, space *model.Space, userID string) *mmmodel.AppError {
	admittedVia, readErr := s.resolveReadForWrite(where, space, userID)
	if readErr != nil {
		return readErr
	}
	createErr := s.requireSpacePagePermissionFrom(where, space, userID, mmmodel.PermissionCreatePage, admittedVia)
	if createErr == nil {
		return nil
	}
	// Only a denial falls through to the second attempt: a failure of the check itself must not be
	// retried into a 403, which would present a backend outage to the user as "not authorized".
	// Mirrors ResolveSpaceRemovePage's handling of the same two-attempt shape.
	if createErr.StatusCode != http.StatusForbidden {
		return createErr
	}
	return s.requireSpacePagePermissionFrom(where, space, userID, mmmodel.PermissionEditPage, admittedVia)
}

// RequireSpacePublish gates publishing a draft on the permission its target actually needs:
// create_page when the draft becomes a new page, edit_page when it updates a live one. isNewPage
// comes from the publish path's own classification of the target row (a fresh draft versus one
// updating a live page).
func (s *Service) RequireSpacePublish(where string, space *model.Space, userID string, isNewPage bool) *mmmodel.AppError {
	admittedVia, readErr := s.resolveReadForWrite(where, space, userID)
	if readErr != nil {
		return readErr
	}
	perm := mmmodel.PermissionEditPage
	if isNewPage {
		perm = mmmodel.PermissionCreatePage
	}
	return s.requireSpacePagePermissionFrom(where, space, userID, perm, admittedVia)
}

// spaceIDOrEmpty is a nil-safe accessor used only for log context.
func spaceIDOrEmpty(space *model.Space) string {
	if space == nil {
		return ""
	}
	return space.Id
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
			return schemeAppError("JoinOpenSpace", defErr)
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
		if _, addErr := s.client.Channel.AddMember(fresh.ChannelId, userID); addErr != nil {
			// The marker describes a membership that does not exist, which no gate consults, but it
			// would mislead a membership review — so it is cleared. Unlike the pre-step this
			// replaced, the failure is returned to a caller who asked for the join and can retry it,
			// rather than logged inside a page write that must not fail.
			if clearErr := s.store.ClearAutoJoined(fresh.Id, userID); clearErr != nil {
				s.log.Warn("failed to clear the auto-join provenance marker of a join that did not happen",
					"space_id", fresh.Id, "user_id", userID, "err", clearErr)
			}
			// A vanished user is a caller-state condition, not a server fault; AddSpaceMember
			// classifies the same failure the same way.
			if errors.Is(addErr, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("JoinOpenSpace", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(addErr)
			}
			return addErr
		}
		joined = true
		joinedChannelID = fresh.ChannelId
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
	return s.BuildSpaceWithAccess(space, userID)
}
