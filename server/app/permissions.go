// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

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
// already-resolved sysadmin status and team standing. active=false means the caller has no
// standing in the space's team: not an active member, or a member without team read_space.
func (s *Service) readResolutionFrom(sysadmin bool, active bool, space *model.Space, userID string) ReadResolution {
	if sysadmin {
		return ReadViaSysadmin
	}
	if !active {
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
// admitted so callers can tell the fall-through case apart. where identifies the calling
// operation for the errors requireActiveMemberGate reports. On such an error the returned
// resolution is ReadDenied but the error is non-nil, so callers must check the error before
// trusting the resolution.
func (s *Service) ResolveSpaceRead(where string, space *model.Space, userID string) (ReadResolution, *mmmodel.AppError) {
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return ReadDenied, appErr
	}
	return s.readResolutionFrom(sysadmin, active, space, userID), nil
}

// requireActiveMemberGate performs the checks that precede the permission-specific branches of
// every exported gate: client wiring, a malformed-user-id 400, existence-hiding on a nil space,
// the sysadmin override, and the caller's team standing.
//
// A non-nil appErr must be returned by the caller immediately; sysadmin=true means the caller may
// admit without further checks. active reports whether the caller holds team read_space, which
// core resolves from an active membership in the space's team (a removed membership grants
// nothing) or a system role; callers branch on it.
//
// Team read_space gates both branches below, the same permission the team listing requires: it is
// what keeps a space and its pages unreachable by id once read_space is revoked, even for a caller
// who still holds read_page on the backing channel. Resolved here, ahead of every gate, so a space
// read and a page read in the same space cannot disagree about it. Both team_user and team_guest
// hold it by default.
func (s *Service) requireActiveMemberGate(where string, space *model.Space, userID string) (active bool, sysadmin bool, appErr *mmmodel.AppError) {
	if appErr = s.requireClient(where, "space_id", spaceIDOrEmpty(space), "user_id", userID); appErr != nil {
		return false, false, appErr
	}
	// A malformed user id is a caller fault, not a denial: it reports as a 400 so it stays
	// distinguishable from the existence-hiding 403 every genuine denial returns.
	if !mmmodel.IsValidId(userID) {
		return false, false, mmmodel.NewAppError(where, "app.space.access.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if space == nil {
		return false, false, ExistenceHidingForbidden(where)
	}
	if s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) {
		return false, true, nil
	}
	return s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionReadSpace), false, nil
}

// RequireSpacePagePermission gates a page-scoped operation on perm: sysadmin override, then
// backing-channel membership (plus active team membership) via HasPermissionToChannel, then —
// for a read permission on an open space only — the non-member fall-through. Any other case
// (including a nil space, mirroring a lookup miss upstream) yields the shared existence-hiding
// 403.
func (s *Service) RequireSpacePagePermission(where string, space *model.Space, userID string, perm *mmmodel.Permission) *mmmodel.AppError {
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	return s.evaluatePagePermission(where, space, userID, perm, active)
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
// A guest is held to read_page whatever the composed channel permission says: demoting a user to
// guest clears SchemeUser/SchemeAdmin, but the capability roles a prior grant wrote into
// ExplicitRoles remain (a capability role is a core role carrying exactly one page permission),
// and core composes those into the member's channel permissions regardless of guest standing.
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
// resolution must be used in the same call that obtained it. A resolution obtained at one gate and
// used at another, after the handler has done other work, opens a window in which a revocation is
// missed.
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
// A non-nil appErr is a failure of the check itself, distinct from a denial, and must be
// propagated as such rather than converted to a 403.
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
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if s.adminOrTeamPermFrom(sysadmin, active, space, userID, teamPerm) {
		return nil
	}
	return ExistenceHidingForbidden(where)
}

// adminOrTeamPermFrom is RequireSpaceAdminOrTeamPerm's decision, evaluated against an
// already-resolved sysadmin status and team standing so a caller answering two questions about
// the same space resolves that standing once. Ordered so the team lookup only runs for a caller
// who is neither sysadmin nor space admin.
func (s *Service) adminOrTeamPermFrom(sysadmin bool, active bool, space *model.Space, userID string, teamPerm *mmmodel.Permission) bool {
	if sysadmin {
		return true
	}
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionAdminSpace) {
		return true
	}
	return s.readResolutionFrom(false, active, space, userID) != ReadDenied &&
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
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return false, appErr
	}
	if s.readResolutionFrom(sysadmin, active, space, userID) == ReadDenied {
		return false, ExistenceHidingForbidden(where)
	}
	return s.adminOrTeamPermFrom(sysadmin, active, space, userID, mmmodel.PermissionManageSpace), nil
}

// RequireSpaceAdminOrSysadmin gates the space-wide exposure-policy knobs (ViewAccess, default
// permissions) and admin-affecting member changes: sysadmin, or channel admin_space plus active
// team membership. No team-manage_space branch — those knobs are stricter than ordinary manage.
func (s *Service) RequireSpaceAdminOrSysadmin(where string, space *model.Space, userID string) *mmmodel.AppError {
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	if active {
		// The SchemeAdmin flag is read from the master row, not composed through
		// HasPermissionToChannel: composition can still reflect the cached, pre-demotion roles.
		// Re-running this gate inside the space-membership lock catches a demotion that took
		// effect while the caller was waiting.
		//
		// The flag is the whole signal for admin_space: RolesForPermissions turns an admin_space
		// grant into SchemeAdmin rather than an ExplicitRoles token, PermissionsFromMember derives
		// the permission back from that flag alone, and the permission is rejected as a space
		// default and carried by no team or system role. A non-member has no row and is denied.
		schemeAdmin, _, flagErr := s.store.GetMemberSchemeFlags(space.ChannelId, userID)
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
	// Only a denial falls through to the second attempt: a failure of the check itself is distinct
	// from a denial and must be propagated as such rather than converted into a 403. Mirrors
	// ResolveSpaceRemovePage's handling of the same two-attempt shape.
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

// JoinOpenSpace lives in space_members.go, next to AddSpaceMember.
