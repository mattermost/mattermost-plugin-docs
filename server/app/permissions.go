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

// ReadResolution reports how a read was admitted, so callers (notably the auto-join pre-step)
// can tell a real membership/sysadmin read from the non-member open-space fall-through.
type ReadResolution int

const (
	ReadDenied ReadResolution = iota
	ReadViaSysadmin
	ReadViaMember
	ReadViaOpenFallthrough
)

// existenceHidingForbidden is the shared 403 every enforcement helper returns on a lookup miss
// or a denied check, so a caller cannot distinguish "doesn't exist", "not a member", and "no
// longer a member" by status code or message.
func existenceHidingForbidden(where string) *mmmodel.AppError {
	return mmmodel.NewAppError(where, "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden)
}

// complianceModeOff reports whether ComplianceSettings.Enable is unset or false. A nil client
// (or nil config) is treated as compliance-off, matching core's own SafeDereference default.
func (s *Service) complianceModeOff() bool {
	if s.client == nil {
		return true
	}
	cfg := s.client.Configuration.GetConfig()
	if cfg == nil {
		return true
	}
	return !mmmodel.SafeDereference(cfg.ComplianceSettings.Enable)
}

// openTeamFallthrough reports whether userID holds the non-member team read_public_channel
// fall-through into teamID's open spaces. Suppressed under compliance mode.
func (s *Service) openTeamFallthrough(userID, teamID string) bool {
	return s.client.User.HasPermissionToTeam(userID, teamID, mmmodel.PermissionReadPublicChannel) && s.complianceModeOff()
}

// readResolutionFrom evaluates the read gate from an already-resolved sysadmin/active pair, so a
// caller that has resolved them once (requireActiveMemberGate) never re-derives them: sysadmin
// override, then backing-channel membership, then — for an open space — the non-member team
// fall-through.
func (s *Service) readResolutionFrom(sysadmin, active bool, space *model.Space, userID string) ReadResolution {
	if sysadmin {
		return ReadViaSysadmin
	}
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionReadPage) {
		return ReadViaMember
	}
	if space.ViewAccess == model.ViewAccessOpen && active && s.openTeamFallthrough(userID, space.TeamId) {
		return ReadViaOpenFallthrough
	}
	return ReadDenied
}

// ResolveSpaceRead resolves the read gate for space against userID, reporting how the read was
// admitted so callers can gate auto-join to the fall-through case only. where identifies the
// calling operation for the 500 an isActiveTeamMember lookup failure surfaces as. On that
// failure the returned resolution is ReadDenied but the error is non-nil, so callers must check
// the error first — treating the resolution alone as authoritative would misreport an outage as
// "not authorized".
func (s *Service) ResolveSpaceRead(where string, space *model.Space, userID string) (ReadResolution, *mmmodel.AppError) {
	if appErr := s.requireClient(where, "space_id", spaceIDOrEmpty(space), "user_id", userID); appErr != nil {
		return ReadDenied, appErr
	}
	if space == nil {
		return ReadDenied, existenceHidingForbidden(where)
	}
	if s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) {
		return ReadViaSysadmin, nil
	}
	active, err := s.isActiveTeamMember(space.TeamId, userID)
	if err != nil {
		return ReadDenied, mmmodel.NewAppError(where, "app.space.access.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return s.readResolutionFrom(false, active, space, userID), nil
}

// requireActiveMemberGate runs the four-gate preamble shared by RequireSpacePagePermission,
// requireChannelAdminOrTeamPerm, and RequireSpaceAdminOrSysadmin (RequireSpacePagePermissionFrom
// skips it, reusing a ReadResolution its caller already resolved): client wiring,
// existence-hiding on a nil space, the sysadmin override, and
// active-team-membership resolution with its 500 on a genuine lookup failure. A non-nil appErr
// must be returned by the caller immediately. Otherwise, when sysadmin is true the caller may
// return nil immediately; when it is false the caller continues with active to evaluate its own
// permission-specific branches.
func (s *Service) requireActiveMemberGate(where string, space *model.Space, userID string) (active, sysadmin bool, appErr *mmmodel.AppError) {
	if appErr = s.requireClient(where, "space_id", spaceIDOrEmpty(space), "user_id", userID); appErr != nil {
		return false, false, appErr
	}
	if space == nil {
		return false, false, existenceHidingForbidden(where)
	}
	if s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) {
		return false, true, nil
	}
	active, err := s.isActiveTeamMember(space.TeamId, userID)
	if err != nil {
		return false, false, mmmodel.NewAppError(where, "app.space.access.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return active, false, nil
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

// evaluatePagePermission grants perm to an active member holding it on the backing channel, or —
// for a read permission on an open space only — to an active team member via the non-member
// fall-through. Any other case yields the shared existence-hiding 403. active is the caller's
// already-resolved team-membership status.
func (s *Service) evaluatePagePermission(where string, space *model.Space, userID string, perm *mmmodel.Permission, active bool) *mmmodel.AppError {
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, perm) {
		return nil
	}
	if perm.Id == mmmodel.PermissionReadPage.Id && space.ViewAccess == model.ViewAccessOpen && active &&
		s.openTeamFallthrough(userID, space.TeamId) {
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
	return s.evaluatePagePermission(where, space, userID, perm, true)
}

// ResolveSpacePageOwnOrAny evaluates a two-tier own/any permission pair: anyPerm if held, else
// ownPerm when ownerMatches. Reports whether the caller qualified only via ownPerm (ownOnly), so
// a caller that must push ownership enforcement further down — MovePageToSpace's subtree-wide
// check — can tell the two tiers apart. admitted=false with a nil appErr means neither tier
// admitted the caller; the caller writes its own denial so the operation label stays its own. A
// non-nil appErr is a genuine backend failure from the check itself, which the caller must
// surface as-is rather than reporting as a denial.
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

// requireChannelAdminOrTeamPerm gates an elevated space operation: sysadmin, channel admin_space
// (plus active team membership), or teamPerm on the space's team — the latter only once the read
// resolver has already admitted the caller, so a team-wide grant authorizes acting only on spaces
// the caller can already read.
func (s *Service) requireChannelAdminOrTeamPerm(where string, space *model.Space, userID string, teamPerm *mmmodel.Permission) *mmmodel.AppError {
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionAdminSpace) {
		return nil
	}
	if s.readResolutionFrom(false, active, space, userID) != ReadDenied &&
		s.client.User.HasPermissionToTeam(userID, space.TeamId, teamPerm) {
		return nil
	}
	return existenceHidingForbidden(where)
}

// RequireSpaceManage gates a manage-tier space operation: sysadmin, channel admin_space, or team
// manage_space.
func (s *Service) RequireSpaceManage(where string, space *model.Space, userID string) *mmmodel.AppError {
	return s.requireChannelAdminOrTeamPerm(where, space, userID, mmmodel.PermissionManageSpace)
}

// RequireSpaceAdminOrSysadmin gates the space-wide exposure-policy knobs (ViewAccess, default
// capabilities) and admin-affecting member changes: sysadmin, or channel admin_space plus active
// team membership. No team-manage_space branch — those knobs are stricter than ordinary manage.
func (s *Service) RequireSpaceAdminOrSysadmin(where string, space *model.Space, userID string) *mmmodel.AppError {
	active, sysadmin, appErr := s.requireActiveMemberGate(where, space, userID)
	if appErr != nil {
		return appErr
	}
	if sysadmin {
		return nil
	}
	if active && s.client.User.HasPermissionToChannel(userID, space.ChannelId, mmmodel.PermissionAdminSpace) {
		return nil
	}
	return existenceHidingForbidden(where)
}

// RequireSpaceDeleteAuthority gates space delete/restore: sysadmin, channel admin_space, or team
// delete_space.
func (s *Service) RequireSpaceDeleteAuthority(where string, space *model.Space, userID string) *mmmodel.AppError {
	return s.requireChannelAdminOrTeamPerm(where, space, userID, mmmodel.PermissionDeleteSpace)
}

// WouldDefaultGrant reports whether space's current default capability set (the scheme's
// generated user role) grants perm to a plain member — the auto-join admission test. Channel
// without a scheme (ErrNotFound) reports false, not an error.
func (s *Service) WouldDefaultGrant(space *model.Space, perm *mmmodel.Permission) (bool, error) {
	roles, err := s.store.GetSchemeRolesForChannel(space.ChannelId)
	if err != nil {
		if store.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if s.client == nil {
		return false, nil
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
	if appErr := s.requireClient("AutoJoinIfDefaultGranted", "space_id", space.Id, "user_id", userID); appErr != nil {
		return false, appErr
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
		granted, grantErr := s.WouldDefaultGrant(fresh, perm)
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
		if _, memErr := s.client.Channel.GetMember(fresh.ChannelId, userID); memErr == nil {
			return nil // Already a member.
		}
		member, addErr := s.client.Channel.AddMember(fresh.ChannelId, userID)
		if addErr != nil {
			return addErr
		}
		joined = true
		joinedUserID, joinedChannelID = member.UserId, fresh.ChannelId
		return nil
	})
	if lockErr != nil {
		var appErr *mmmodel.AppError
		if errors.As(lockErr, &appErr) {
			return false, appErr
		}
		return false, storeAppError("AutoJoinIfDefaultGranted", lockErr)
	}
	// Published after the lock is released: the membership lock holds a dedicated connection, so a
	// slow publish inside it would push concurrent membership mutations into a lock timeout.
	if joined {
		s.publishToChannels(wsEventSpaceMemberAdded, map[string]any{"space_id": space.Id, "user_id": joinedUserID}, joinedChannelID)
	}
	return joined, nil
}
