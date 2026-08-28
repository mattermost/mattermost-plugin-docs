// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// errPresetSchemeMissing tags a preset-scheme lookup that found nothing, which means core has not
// seeded the space schemes on this server.
var errPresetSchemeMissing = errors.New("preset space scheme is not seeded")

// validateSpaceMutableFields enforces the Description/Icon size caps shared by CreateSpace and
// UpdateSpace. where identifies the calling operation for logs; the message keys are shared
// across callers.
func validateSpaceMutableFields(where, description, icon string) *mmmodel.AppError {
	if utf8.RuneCountInString(description) > model.SpaceDescriptionMaxRunes {
		return mmmodel.NewAppError(where, "app.shared.description_too_long.app_error", map[string]any{"MaxLength": model.SpaceDescriptionMaxRunes}, "", http.StatusBadRequest)
	}
	if len(icon) > model.SpaceIconMaxBytes {
		return mmmodel.NewAppError(where, "app.shared.icon_too_large.app_error", map[string]any{"MaxBytes": model.SpaceIconMaxBytes}, "", http.StatusBadRequest)
	}
	return nil
}

// archiveOrphanChannel archives a backing channel when a later step in space creation fails,
// to avoid an orphaned channel. reason describes the step that failed; cause is its error.
func (s *Service) archiveOrphanChannel(channelID, reason string, cause error) {
	if s.client == nil {
		return
	}
	if delErr := s.client.Channel.Delete(channelID); delErr != nil {
		// The channel now exists with no space row pointing at it and nothing will retry this
		// archive, so it needs an operator to clean it up: log at Error, not Warn.
		s.log.Error("compensating channel archive failed; channel is orphaned and must be archived manually", "channel_id", channelID, "failure_reason", reason, "cause_err", cause, "delete_err", delErr)
	}
}

// resolveSpaceScheme picks the backing-channel scheme that gives a space's plain members the
// requested permissions. A set matching one of the seeded presets resolves to that preset's
// scheme; any other set resolves to a scheme in the shared pool keyed by the set itself, created
// on first use and thereafter shared by every space configured that way.
//
// roles names the three generated roles of the resolved scheme in both cases, keeping the member
// assignment tied to the same scheme result without another channel/scheme lookup.
//
// Both kinds arrive fully configured: a preset is seeded, and core writes a plugin-created
// scheme's three roles with their final permissions in the transaction that creates it. Callers
// change defaults by selecting another pooled scheme rather than patching these roles. Nothing here
// is owned by one space.
func (s *Service) resolveSpaceScheme(permissions []string) (schemeID string, roles *schemeRoles, err error) {
	// Normalize before the permission set is persisted: the validators are dedup-tolerant, so a
	// repeated allowlisted token in the request is deduplicated here rather than written verbatim
	// into the generated role's Permissions column.
	permissions = mmmodel.NormalizePermissions(permissions)
	if presetName, ok := model.SchemeNameForDefaultPermissions(permissions); ok {
		scheme, getErr := s.getSchemeByName(presetName)
		if getErr != nil {
			// Core seeds the presets; the plugin only reads them. A miss therefore means the server
			// is unseeded, not that the caller named something that does not exist, so it is tagged
			// to keep it out of the shared not-found translation.
			if store.IsErrNotFound(getErr) {
				return "", nil, fmt.Errorf("%w: %s", errPresetSchemeMissing, presetName)
			}
			return "", nil, getErr
		}
		return scheme.Id, rolesFromScheme(scheme), nil
	}
	scheme, poolErr := s.client.Scheme.GetOrCreatePluginChannelScheme(
		spaceUserRolePermissions(permissions),
		spaceAdminRolePermissions(),
		spaceGuestRolePermissions(),
	)
	if poolErr != nil {
		return "", nil, normalizeUnsupportedSchemeAPI(poolErr)
	}
	if scheme == nil {
		return "", nil, errUnsupportedSchemeAPI
	}
	return scheme.Id, rolesFromScheme(scheme), nil
}

// resolveCreateSpaceAccess resolves CreateSpace's access inputs together: the view access (nil
// defaults to open), the default permission set (nil uses the site-level new-space template), and
// the backing-channel scheme that set requires.
func (s *Service) resolveCreateSpaceAccess(viewAccess *model.ViewAccess, defaultPermissions *[]string) (model.ViewAccess, []string, string, *schemeRoles, *mmmodel.AppError) {
	va := model.ViewAccessOpen
	if viewAccess != nil {
		va = *viewAccess
	}
	if !va.IsValid() {
		return "", nil, "", nil, mmmodel.NewAppError("CreateSpace", "app.space.create.invalid_view_access.app_error", nil, "", http.StatusBadRequest)
	}

	permissions := s.newSpaceDefaultPermissions()
	if defaultPermissions != nil {
		permissions = *defaultPermissions
	}
	if capErr := model.ValidateDefaultPermissions(permissions); capErr != nil {
		return "", nil, "", nil, capErr
	}

	schemeID, resolvedRoles, schemeErr := s.resolveSpaceScheme(permissions)
	if schemeErr != nil {
		return "", nil, "", nil, s.schemeAppError("CreateSpace", schemeErr)
	}
	return va, permissions, schemeID, resolvedRoles, nil
}

// CreateSpace creates a ChannelTypeSpace ("S") backing channel via pluginapi, saves the
// space row pointing at it, and adds the creator as a member with SchemeAdmin. space.ChannelId
// must be empty — it is set from the created channel. defaultPermissions nil uses the live
// site-level new-space template supplied to the service (contribute by default); viewAccess nil
// defaults to open. If any step after the backing channel's
// creation fails, the backing channel is archived to avoid an orphan. Any scheme resolved along the
// way is left alone: presets and pooled schemes are shared, so none is this space's to remove.
//
// The channel create and the row save are separate systems with no shared transaction: a crash
// between them leaves a real channel with no space row and no persisted marker to key a retry
// off, so that window is cleaned up only by the best-effort compensating archive below (or an
// operator, if that also fails).
func (s *Service) CreateSpace(space *model.Space, userID string, defaultPermissions *[]string, viewAccess *model.ViewAccess) (*model.SpaceWithAccess, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.nil_input.app_error", nil, "", http.StatusBadRequest)
	}
	if space.ChannelId != "" {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.channel_id_not_allowed.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(space.TeamId) {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	// Reject a malformed acting user before any channel I/O, mirroring CreatePage.
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	// The backing channel must exist before the space row is saved; a nil client is a
	// hard precondition failure, not a recoverable skip.
	if appErr := s.requireClient("CreateSpace", "team_id", space.TeamId, "user_id", userID); appErr != nil {
		return nil, appErr
	}
	// Validate all in-memory fields before the first I/O call, mirroring CreatePage.
	title, titleErr := validateTitle("CreateSpace", space.Title, model.SpaceTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	space.Title = title
	// Validate Description and Icon before creating the backing channel, mirroring UpdateSpace.
	if fieldErr := validateSpaceMutableFields("CreateSpace", space.Description, space.Icon); fieldErr != nil {
		return nil, fieldErr
	}
	// The caller must hold create_space on the target team (or be sysadmin) before a backing
	// channel is stood up there. Core grants a team permission only through an active membership
	// in that team or a system role, so this also rejects a non-member supplying a team id —
	// otherwise any authenticated user could create a real, visible channel in any team. Unlike
	// the read/manage/delete gates, no space exists yet here, so there is nothing to
	// existence-hide behind — a plain 403 is correct.
	if !s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) &&
		!s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionCreateSpace) {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.forbidden.app_error", nil, "", http.StatusForbidden)
	}
	// Sanitize before it's used as the channel Header below — Space.PreSave sanitizes it again on
	// the store.CreateSpace path, but that happens after the channel is already created.
	space.Description = mmmodel.SanitizeUnicode(space.Description)
	space.CreatorId = userID

	va, permissions, schemeID, resolvedRoles, accessErr := s.resolveCreateSpaceAccess(viewAccess, defaultPermissions)
	if accessErr != nil {
		return nil, accessErr
	}
	space.ViewAccess = va

	s.log.Debug("Creating space", "team_id", space.TeamId, "user_id", userID)

	backingChannel := &mmmodel.Channel{
		TeamId:    space.TeamId,
		Type:      mmmodel.ChannelTypeSpace,
		Name:      "space-" + mmmodel.NewId()[:20],
		CreatorId: userID,
		SchemeId:  &schemeID,
	}
	applySpaceFieldsToChannel(backingChannel, space)
	if err := s.client.Channel.Create(backingChannel); err != nil {
		// The pluginapi wrapper copies the created channel — including its Id — into
		// backingChannel before its post-create bookkeeping, so an error alongside a populated
		// Id means the channel row already exists and must be archived, not leaked.
		if backingChannel.Id != "" {
			s.archiveOrphanChannel(backingChannel.Id, "channel create failed after creation", err)
		}
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.backing_channel_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}

	if _, addErr := s.client.Channel.AddMember(backingChannel.Id, userID); addErr != nil {
		// A space whose creator is not a member of its backing channel is a dead-end once per-space
		// membership gating lands (unreachable to everyone, creator included), so fail the create
		// and archive the orphan channel rather than continuing.
		s.archiveOrphanChannel(backingChannel.Id, "creator member-add failed", addErr)
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.add_member_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(addErr)
	}

	// The creator is added as SchemeAdmin — both the scheme's resolved generated role names, never
	// literals: on a scheme-backed channel core rejects the literal channel_user/channel_admin
	// tokens. The base user-role token is required, not optional (core resets all scheme flags and
	// rejects a string that leaves SchemeUser unset). The role names come from the scheme resolved
	// above, keeping this assignment tied to that exact selection without another lookup.
	if _, roleErr := s.client.Channel.UpdateChannelMemberRoles(backingChannel.Id, userID, resolvedRoles.UserRoleName+" "+resolvedRoles.AdminRoleName); roleErr != nil {
		s.archiveOrphanChannel(backingChannel.Id, "creator admin role assignment failed", roleErr)
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.admin_role_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(roleErr)
	}

	space.ChannelId = backingChannel.Id

	saved, err := s.store.CreateSpace(space)
	if err != nil {
		s.archiveOrphanChannel(backingChannel.Id, "row save failed", err)
		return nil, storeAppError("CreateSpace", err)
	}

	s.publishToChannels(wsEventSpaceCreated, map[string]any{"space_id": saved.Id}, saved.ChannelId)

	// Both halves of the access state are already settled here, so the wrapper is projected from
	// what this call established rather than re-resolved: defaultPermissions is the set just
	// applied to the scheme, and the creator was assigned SchemeAdmin above — a step whose failure
	// aborts the create, so reaching this point means the creator holds the full admin set.
	wrapper := &model.SpaceWithAccess{
		Space:              *saved,
		DefaultPermissions: mmmodel.NormalizePermissions(permissions),
		// The creator is made a space admin as part of this call, so the manage and delete tiers
		// both follow without a lookup — admin_space satisfies either gate on its own.
		Permissions: mmmodel.NormalizePermissions(append(model.AdminEffectivePermissions(), mmmodel.PermissionManageSpace.Id, mmmodel.PermissionDeleteSpace.Id)),
	}
	wrapper.Props = maps.Clone(saved.Props)
	wrapper.EnsurePermissions()
	return wrapper, nil
}

// GetSpace returns the live space with the given ID.
func (s *Service) GetSpace(spaceID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetSpace", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	space, err := s.store.GetSpace(spaceID, false)
	if err != nil {
		return nil, storeAppError("GetSpace", err)
	}
	return space, nil
}

// GetSpaceWithDeleted returns the space by ID including soft-deleted spaces.
func (s *Service) GetSpaceWithDeleted(spaceID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetSpaceWithDeleted", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	space, err := s.store.GetSpace(spaceID, true)
	if err != nil {
		return nil, storeAppError("GetSpaceWithDeleted", err)
	}
	return space, nil
}

// spaceDefaultPermissions resolves space's default permission set in wire form
// (read_page-free): the generated user role's stored permission set projected onto the permission
// vocabulary. The projection covers presets and pooled schemes alike, since a preset's generated
// user role carries exactly that preset's permissions.
func (s *Service) spaceDefaultPermissions(space *model.Space) ([]string, error) {
	roles, err := s.getSchemeRolesForChannel(space.ChannelId)
	if err != nil {
		return nil, err
	}
	return s.defaultPermissionsForRoles(roles)
}

// defaultPermissionsForRoles is spaceDefaultPermissions for a caller that already holds the
// backing channel's scheme roles.
func (s *Service) defaultPermissionsForRoles(roles *schemeRoles) ([]string, error) {
	return model.DefaultPermissionsFrom(roles.UserPermissions), nil
}

// BuildSpaceWithAccess resolves the GET /spaces/{id} response wrapper: the space's default
// permission set plus the caller's server-resolved effective permissions, never a hypothetical
// post-join grant. A denied read yields the shared existence-hiding 403.
func (s *Service) BuildSpaceWithAccess(space *model.Space, userID string) (*model.SpaceWithAccess, *mmmodel.AppError) {
	return s.buildSpaceWithAccess(space, userID, nil)
}

// buildSpaceWithAccess is BuildSpaceWithAccess with an optional default-permission value supplied
// by a write path. Supplying it avoids immediately re-reading a scheme after a repoint or create;
// member permissions are still derived from the membership returned by core.
func (s *Service) buildSpaceWithAccess(space *model.Space, userID string, knownDefaults []string) (*model.SpaceWithAccess, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("BuildSpaceWithAccess", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("BuildSpaceWithAccess", "space_id", space.Id, "user_id", userID); appErr != nil {
		return nil, appErr
	}
	// The read gate resolves first, so a denied caller gets the existence-hiding 403 instead of a
	// default-permission lookup result for a space they cannot see.
	resolution, resErr := s.ResolveSpaceRead("BuildSpaceWithAccess", space, userID)
	if resErr != nil {
		return nil, resErr
	}
	if resolution == ReadDenied {
		return nil, ExistenceHidingForbidden("BuildSpaceWithAccess")
	}
	defaultPermissions := knownDefaults
	if defaultPermissions == nil {
		var err error
		defaultPermissions, err = s.spaceDefaultPermissions(space)
		if err != nil {
			return nil, s.schemeAppError("BuildSpaceWithAccess", err)
		}
	}

	var permissions []string
	switch resolution {
	case ReadViaSysadmin:
		permissions = model.AdminEffectivePermissions()
	case ReadViaMember:
		member, memErr := s.client.Channel.GetMember(space.ChannelId, userID)
		if memErr != nil {
			return nil, mmmodel.NewAppError("BuildSpaceWithAccess", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		permissions = model.PermissionsFromMember(member.ExplicitRoles, member.SchemeAdmin, member.SchemeGuest, defaultPermissions).Effective
	case ReadViaOpenFallthrough:
		permissions = []string{mmmodel.PermissionReadPage.Id}
	default:
		return nil, ExistenceHidingForbidden("BuildSpaceWithAccess")
	}

	// The manage tier follows the same disjunction as requireSpaceManage's gate. Emitted as
	// the permission it is rather than as a separate answer field: a caller holding it may manage
	// this space, which is exactly what this set states. The read gate above already admitted the
	// caller, which is the precondition that gate puts on its team-permission branch. Ordered so the
	// team lookup only runs for a caller who is neither sysadmin nor space admin.
	if resolution == ReadViaSysadmin ||
		slices.Contains(permissions, mmmodel.PermissionAdminSpace.Id) ||
		s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionManageSpace) {
		// Re-normalized rather than plain-appended: every other producer of this field emits a
		// sorted, deduplicated set, and a client comparing two responses should not see the order
		// depend on which tier admitted the caller.
		permissions = mmmodel.NormalizePermissions(append(permissions, mmmodel.PermissionManageSpace.Id))
	}

	// The delete tier, resolved the same way against requireSpaceDelete's gate. Emitted separately
	// from the manage tier rather than folded into it: the two team permissions are independent — a
	// manage_space holder need not hold delete_space, and vice versa — so a client must gate archive
	// on this field, not on the manage tier.
	if resolution == ReadViaSysadmin ||
		slices.Contains(permissions, mmmodel.PermissionAdminSpace.Id) ||
		s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionDeleteSpace) {
		permissions = mmmodel.NormalizePermissions(append(permissions, mmmodel.PermissionDeleteSpace.Id))
	}

	wrapper := &model.SpaceWithAccess{
		Space:              *space,
		DefaultPermissions: defaultPermissions,
		Permissions:        permissions,

		// Only the fall-through reader may join: a member is already in, a sysadmin needs no
		// membership, and a guest never resolves this way (the fall-through takes
		// read_public_channel, which core's team_guest role does not carry). Defaults that confer
		// nothing beyond the read every reader has make joining pointless, and JoinOpenSpace
		// refuses it, so this must not offer it either.
		CanJoin: resolution == ReadViaOpenFallthrough && len(defaultPermissions) > 0,
	}
	wrapper.Props = maps.Clone(space.Props)
	wrapper.EnsurePermissions()
	return wrapper, nil
}

// SetSpaceDefaultPermissions changes space's default permission set: a set matching a seeded
// preset repoints the backing channel at that preset's scheme; any other set repoints it at the
// pooled scheme for that permission set, created on first use and shared by every space configured
// the same way. The superseded scheme is left in place — no scheme belongs to a single space, so
// there is nothing to retire. The repoint goes through pluginapi Channel.Update (not a
// store-direct write), so the new scheme takes effect on the next permission check rather than
// when a stale cached composition expires.
func (s *Service) SetSpaceDefaultPermissions(space *model.Space, permissions []string, actingUserID string) (*model.SpaceWithAccess, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("SetSpaceDefaultPermissions", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := model.ValidateDefaultPermissions(permissions); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("SetSpaceDefaultPermissions", "space_id", space.Id); appErr != nil {
		return nil, appErr
	}

	// Scheme identity represents the normalized default set; only a repoint is a change.
	var changed bool
	// Preserve the normalized request for a response that does not depend on replica visibility.
	var requested []string
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		// Re-authorized in the lock, since the route gate runs before the lock is taken: an admin
		// demoted in the interval must not still get this write through.
		//
		// Unconditional here, unlike the member-scoped writes. SetSpaceMemberPermissions and
		// RemoveSpaceMember re-run this gate only when the target is an admin or the caller
		// themselves, because that is the range over which the cached route gate cannot be trusted:
		// this gate reads SchemeAdmin from the master, and only an admin-affecting write needs an
		// answer fresher than the cache's. A space-wide default has no target to narrow by, so
		// there is no equivalent condition to apply.
		if appErr := s.RequireSpaceAdminOrSysadmin("SetSpaceDefaultPermissions", space, actingUserID); appErr != nil {
			return appErr
		}

		channel, chanErr := s.client.Channel.GetChannelOfType(space.ChannelId, mmmodel.ChannelTypeSpace)
		if chanErr != nil {
			return mmmodel.NewAppError("SetSpaceDefaultPermissions", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(chanErr)
		}
		if channel == nil || channel.SchemeId == nil {
			return mmmodel.NewAppError("SetSpaceDefaultPermissions", "app.space.default_permissions.channel_scheme_missing.app_error", nil, "", http.StatusInternalServerError)
		}
		currentSchemeID := *channel.SchemeId
		requested = mmmodel.NormalizePermissions(permissions)

		targetSchemeID, _, schemeErr := s.resolveSpaceScheme(requested)
		if schemeErr != nil {
			return s.schemeAppError("SetSpaceDefaultPermissions", schemeErr)
		}
		if targetSchemeID == currentSchemeID {
			// Pooled schemes are keyed by the normalized permission set and normal app APIs treat
			// their generated roles as immutable, so this assignment is already satisfied.
			return nil
		}

		// Changing the default moves the space to the scheme expressing the new set and leaves the
		// old one untouched for whichever spaces still point at it. The app does not rewrite the
		// pooled schemes' roles, so changing the channel assignment does not alter another space.
		channel.SchemeId = &targetSchemeID
		if updErr := s.client.Channel.Update(channel); updErr != nil {
			return mmmodel.NewAppError("SetSpaceDefaultPermissions", "app.space.default_permissions.repoint_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updErr)
		}
		changed = true

		// The superseded scheme is left in place: presets and plugin-created schemes are shared by
		// every space expressing that permission set, so none is this space's to delete.
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("SetSpaceDefaultPermissions", lockErr)
	}

	// Use the resolved set in the response; the scheme may not yet be visible on a replica.
	fresh, getErr := s.GetSpace(space.Id)
	if getErr != nil {
		// The scheme repoint already committed; report from the requested set and the pre-update
		// space, still firing the WS event, instead of reporting a post-commit read failure as an
		// update failure.
		s.log.Warn("SetSpaceDefaultPermissions: post-commit re-read failed; responding from the requested set", "space_id", space.Id, "err", getErr)
		if changed {
			s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": space.Id}, space.ChannelId)
		}
		return s.buildSpaceWithAccess(space, actingUserID, requested)
	}
	if changed {
		s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": fresh.Id}, fresh.ChannelId)
	}
	return s.buildSpaceWithAccess(fresh, actingUserID, requested)
}

// GetSpaceMembers, AddSpaceMember, SetSpaceMemberPermissions, and RemoveSpaceMember live in
// space_members.go alongside the escalation and last-admin guards.

// GetSpacesForTeam returns one page of a team's live spaces, plus whether more exist beyond it.
// userID must be an active team member; a sysadmin is not exempt from that requirement. A
// non-sysadmin must also hold team read_space (the list-entry gate; every team_user holds it by
// default). The result is the union of spaces the caller is a backing-channel
// member of and open spaces the caller can reach through the same non-member fall-through
// single-space read uses: team read_public_channel held, and compliance mode off.
func (s *Service) GetSpacesForTeam(teamID, userID string, page, perPage int) ([]*model.Space, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(teamID) {
		return nil, false, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, false, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	if appErr := s.requireClient("GetSpacesForTeam", "team_id", teamID, "user_id", userID); appErr != nil {
		return nil, false, appErr
	}
	if !s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) &&
		!s.client.User.HasPermissionToTeam(userID, teamID, mmmodel.PermissionReadSpace) {
		return nil, false, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.forbidden.app_error", nil, "", http.StatusForbidden)
	}
	callerHasOpenFallthrough := s.hasOpenTeamFallthrough(userID, teamID)
	spaces, err := s.store.GetSpacesForTeam(teamID, userID, callerHasOpenFallthrough, offset, limit)
	if err != nil {
		return nil, false, storeAppError("GetSpacesForTeam", err)
	}
	spaces, hasMore := trimPage(spaces, limit)
	return spaces, hasMore, nil
}

// normalizeAndValidateSpacePatch normalizes a space patch's Title (trimmed, empty rejected) in
// place and fail-fast validates the supplied Description/Icon sizes; a nil field means "leave
// unchanged" and is not validated. Patch-shape validation is deferred to SpacePatch.IsValid,
// mirroring normalizeAndValidatePagePatch.
func normalizeAndValidateSpacePatch(where string, patch *model.SpacePatch) *mmmodel.AppError {
	if validErr := patch.IsValid(); validErr != nil {
		return validErr
	}
	if patch.Title != nil {
		normalized, titleErr := validateTitle(where, *patch.Title, model.SpaceTitleMaxRunes)
		if titleErr != nil {
			return titleErr
		}
		patch.Title = &normalized
	}
	if patch.ViewAccess != nil && !patch.ViewAccess.IsValid() {
		return mmmodel.NewAppError(where, "app.space.update.invalid_view_access.app_error", nil, "", http.StatusBadRequest)
	}
	description, icon := "", ""
	if patch.Description != nil {
		description = *patch.Description
	}
	if patch.Icon != nil {
		icon = *patch.Icon
	}
	return validateSpaceMutableFields(where, description, icon)
}

// UpdateSpace applies the non-nil fields of patch onto the space and saves it. A non-nil
// field (including an empty string) overwrites the current value, so a field can be cleared.
// Optimistic-locked on expectedUpdateAt: the caller passes the UpdateAt it last read, and a stale
// baseline yields a conflict unless force overrides the check and applies the update anyway; a
// nil expectedUpdateAt without force is rejected. The store merges the patch into the current
// row, so a forced update overwrites only the fields the patch supplies — concurrent changes to
// other fields are preserved. space is the caller's already-fetched record (from its
// membership gate); only its Id is used here.
//
// A patch that changes ViewAccess requires RequireSpaceAdminOrSysadmin against the live row and is
// rejected when force=true. actingUserID is used only for that escalation check.
//
// Every patch is serialized against other mutations of the same space. An open-to-private
// transition also removes every auto-joined member. A removal failure aborts the transition and
// leaves the space open; every membership removed before the failure stays removed and its owner
// is still notified.
func (s *Service) UpdateSpace(space *model.Space, patch *model.SpacePatch, expectedUpdateAt *int64, force bool, actingUserID string) (*model.Space, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := requireBaseline("UpdateSpace", "expected_update_at", expectedUpdateAt, force); appErr != nil {
		return nil, appErr
	}
	if appErr := normalizeAndValidateSpacePatch("UpdateSpace", patch); appErr != nil {
		return nil, appErr
	}
	if patch.ViewAccess != nil && force {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.view_access_force.app_error", nil, "", http.StatusBadRequest)
	}

	s.log.Debug("Updating space", "space_id", space.Id)

	// gateAndSnapshot runs under the first lock acquisition: it re-reads the live row and enforces
	// the ViewAccess escalation gate. A patch that is not an open-to-private flip writes the row
	// and returns here, in the one acquisition it needs. A flip instead validates the CAS baseline,
	// snapshots the channel id and auto-joined ids the unlocked removal pass below needs, and defers
	// the row write to the second acquisition.
	var result *model.Space
	var flipping bool
	var flipChannelID string
	var flipAutoJoinedIDs []string
	gateAndSnapshot := func() error {
		var live *model.Space
		if patch.ViewAccess != nil {
			gotLive, liveErr := s.store.GetSpace(space.Id, false)
			if liveErr != nil {
				return storeAppError("UpdateSpace", liveErr)
			}
			live = gotLive
			if *patch.ViewAccess != live.ViewAccess {
				if appErr := s.RequireSpaceAdminOrSysadmin("UpdateSpace", live, actingUserID); appErr != nil {
					return appErr
				}
			}
		}
		flipping = patch.ViewAccess != nil && *patch.ViewAccess == model.ViewAccessPrivate &&
			live != nil && live.ViewAccess == model.ViewAccessOpen
		if !flipping {
			updated, appErr := s.writeSpacePatch(space.Id, patch, expectedUpdateAt, force)
			if appErr != nil {
				return appErr
			}
			result = updated
			return nil
		}
		// Reject a stale request before anything is removed; the second acquisition below repeats
		// this exact check once the removals complete.
		if live.UpdateAt != mmmodel.SafeDereference(expectedUpdateAt) {
			return storeAppError("UpdateSpace", &store.ErrConflict{Resource: "Space id=" + space.Id})
		}
		flipChannelID = live.ChannelId
		autoJoined, err := s.store.GetAutoJoinedIDs(space.Id)
		if err != nil {
			return mmmodel.NewAppError("UpdateSpace", "app.space.prune_self_joined.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
		flipAutoJoinedIDs = autoJoined
		return nil
	}
	if lockErr := s.store.WithSpaceMembershipLock(space.Id, gateAndSnapshot); lockErr != nil {
		return nil, membershipLockAppError("UpdateSpace", lockErr)
	}
	if !flipping {
		s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": result.Id}, result.ChannelId)
		return result, nil
	}

	// Removed with the lock released: PruneSelfJoinedMembers/removeAutoJoinedMembers issues one
	// Channel.DeleteMember RPC per snapshotted id, and the lock's dedicated pool connection must not
	// sit idle for their whole duration.
	prunedSpace := &model.Space{Id: space.Id, ChannelId: flipChannelID}
	prunedUserIDs, pruneErr := s.removeAutoJoinedMembers(prunedSpace, flipAutoJoinedIDs)
	publishPrunedMemberships := func() {
		if len(prunedUserIDs) == 0 {
			return
		}
		// Each removed user is told directly: they have just left the channel, so a channel-scoped
		// broadcast can no longer reach them — and they are exactly who has to learn their access
		// changed. The remaining members are told once, with no user id, that the roster changed:
		// a channel broadcast resolves the audience and makes every recipient refetch the roster,
		// so sending one per removed user would cost that work once per removal.
		for _, userID := range prunedUserIDs {
			s.publishToUser(wsEventSpaceMemberRemoved, map[string]any{"space_id": space.Id, "user_id": userID}, userID)
		}
		s.publishToChannels(wsEventSpaceMemberRemoved, map[string]any{"space_id": space.Id}, flipChannelID)
	}
	if pruneErr != nil {
		publishPrunedMemberships()
		return nil, pruneErr
	}

	// Re-acquire the lock to write the row: re-validate the CAS baseline and that the space is
	// still open, since a concurrent change during the unlocked removal window above must be
	// rejected the same way a stale baseline is.
	writeErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		live, liveErr := s.store.GetSpace(space.Id, false)
		if liveErr != nil {
			return storeAppError("UpdateSpace", liveErr)
		}
		if live.ViewAccess != model.ViewAccessOpen || live.UpdateAt != mmmodel.SafeDereference(expectedUpdateAt) {
			return storeAppError("UpdateSpace", &store.ErrConflict{Resource: "Space id=" + space.Id})
		}
		// A join that landed during the unlocked removal pass does not move UpdateAt, so the
		// baseline check above cannot see it. Re-read the markers and remove any such member here,
		// under the lock, where JoinOpenSpace cannot add another; the set is bounded by that window.
		stragglers, markErr := s.store.GetAutoJoinedIDs(space.Id)
		if markErr != nil {
			return mmmodel.NewAppError("UpdateSpace", "app.space.prune_self_joined.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(markErr)
		}
		if len(stragglers) > 0 {
			removed, stragglerErr := s.removeAutoJoinedMembers(prunedSpace, stragglers)
			prunedUserIDs = append(prunedUserIDs, removed...)
			if stragglerErr != nil {
				return stragglerErr
			}
		}
		updated, appErr := s.writeSpacePatch(space.Id, patch, expectedUpdateAt, force)
		if appErr != nil {
			return appErr
		}
		result = updated
		return nil
	})
	publishPrunedMemberships()
	if writeErr != nil {
		return nil, membershipLockAppError("UpdateSpace", writeErr)
	}
	if len(prunedUserIDs) > 0 {
		s.log.Debug("space view_access flipped to private", "space_id", result.Id, "pruned_members", len(prunedUserIDs))
	}
	s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": result.Id}, result.ChannelId)
	return result, nil
}

// writeSpacePatch applies patch to the space row and projects the result onto the backing
// channel's metadata. Both of UpdateSpace's lock acquisitions call this while holding the lock.
func (s *Service) writeSpacePatch(spaceID string, patch *model.SpacePatch, expectedUpdateAt *int64, force bool) (*model.Space, *mmmodel.AppError) {
	updated, err := s.store.UpdateSpace(spaceID, patch, mmmodel.SafeDereference(expectedUpdateAt), force)
	if err != nil {
		return nil, storeAppError("UpdateSpace", err)
	}
	// Stays under the lock: it projects the row this call just wrote onto the backing channel, so
	// two concurrent updates must not interleave their syncs and leave the channel carrying the
	// older title.
	if updated.ChannelId != "" && s.client != nil {
		if chanErr := s.syncSpaceChannelMetadata(updated.Id); chanErr != nil {
			// Deliberately not returned: the space row (the source of truth) committed, so
			// failing the request would misreport a successful update, and retrying it would
			// 409 on the now-stale baseline. The next successful UpdateSpace re-syncs the
			// channel. Logged at Error so the resulting name/header divergence is visible to
			// operators.
			s.log.Error("UpdateSpace: failed to sync backing channel metadata; display name/header stale until the next update", "channel_id", updated.ChannelId, "space_id", updated.Id, "err", chanErr)
		}
	}
	return updated, nil
}

// syncSpaceChannelMetadata projects persisted space metadata onto its backing channel. The space
// row remains the source of truth; a concurrently deleted space is a no-op.
func (s *Service) syncSpaceChannelMetadata(spaceID string) error {
	space, err := s.store.GetSpace(spaceID, false)
	if err != nil {
		if store.IsErrNotFound(err) {
			return nil
		}
		return err
	}
	channel, err := s.client.Channel.GetChannelOfType(space.ChannelId, mmmodel.ChannelTypeSpace)
	if err != nil {
		return err
	}
	if channel == nil {
		return nil
	}
	applySpaceFieldsToChannel(channel, space)
	return s.client.Channel.Update(channel)
}

// applySpaceFieldsToChannel projects the space fields mirrored on the backing channel:
// Title becomes the display name (capped to the channel limit) and Description the header.
// Both the create and update paths go through here so the projection cannot diverge.
func applySpaceFieldsToChannel(channel *mmmodel.Channel, space *model.Space) {
	channel.DisplayName, _ = mmmodel.LimitRunes(space.Title, mmmodel.ChannelDisplayNameMaxRunes)
	channel.Header = space.Description
}

// DeleteSpace soft-deletes a space and its pages (reversible via RestoreSpace), then archives the
// backing channel best-effort; RestoreSpace un-archives it on restore. The channel archive runs
// with elevated plugin permissions, independently of the requesting user. space is the caller's
// already-fetched record (from its membership gate), so no re-read here. Clients receive a single
// space_deleted event and must treat it as an invalidation of the space's whole page tree.
func (s *Service) DeleteSpace(space *model.Space) *mmmodel.AppError {
	if space == nil {
		return mmmodel.NewAppError("DeleteSpace", "app.space.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Deleting space", "space_id", space.Id)
	if err := s.store.DeleteSpace(space.Id); err != nil {
		return storeAppError("DeleteSpace", err)
	}
	// Channel-scoped WS delivery resolves recipients from live channels only, so a broadcast to
	// the backing channel after it is archived below would reach nobody. Snapshot the members
	// while the channel is still live and deliver space_deleted to each of them directly.
	recipients, snapErr := s.snapshotSpaceMemberIDs(space)
	if snapErr != nil {
		s.log.Warn("DeleteSpace: failed to snapshot backing-channel members for space_deleted delivery", "channel_id", space.ChannelId, "space_id", space.Id, "err", snapErr)
		// With no member snapshot, the channel broadcast is the only remaining delivery path.
		s.publishToChannels(wsEventSpaceDeleted, map[string]any{"space_id": space.Id}, space.ChannelId)
	}
	// Archive the backing channel best-effort. pluginapi.Channel.Delete soft-deletes the channel
	// (sets DeleteAt). Guarded with a client nil-check so store-only tests (which seed spaces
	// directly and never wire a client) don't panic.
	if space.ChannelId != "" && s.client != nil {
		if err := s.client.Channel.Delete(space.ChannelId); err != nil {
			s.log.Warn("DeleteSpace: failed to archive backing channel; channel may require manual cleanup", "channel_id", space.ChannelId, "space_id", space.Id, "err", err)
		}
	}
	if snapErr != nil {
		return nil
	}
	for _, userID := range recipients {
		s.publishToUser(wsEventSpaceDeleted, map[string]any{"space_id": space.Id}, userID)
	}
	return nil
}

// snapshotSpaceMemberIDs returns the user IDs of the backing-channel members of space who still
// pass the team half of the read gate — the audience the space's events may reach. A row can
// survive for a member the gate rejects (a team departure whose channel cleanup stopped partway,
// or a team scheme that withholds read_space), so its user is not delivered to. A nil client or a
// space with no backing channel yields no members and no error.
func (s *Service) snapshotSpaceMemberIDs(space *model.Space) ([]string, error) {
	if s.client == nil || space.ChannelId == "" {
		return nil, nil
	}
	audience, err := s.resolveSpaceAudience(space.ChannelId)
	if err != nil {
		return nil, err
	}
	return audience.admittedIDs(), nil
}

// RestoreSpace un-deletes a soft-deleted space by ID and un-archives its backing channel, returning
// the restored space. Fails with a conflict error if another live space already owns the backing
// channel. Unlike DeleteSpace's best-effort archive, a failed channel un-archive here is returned as
// an error rather than logged and swallowed: a space reported as restored while its backing channel
// stays archived is more visibly broken to callers than a deleted space whose channel lingers live,
// so the two are not symmetric. The space row itself is left restored; the caller can retry the
// restore (or un-archive the channel directly) rather than the operation silently reporting success.
//
// The channel unarchive runs with elevated plugin permissions, independently of the requesting user.
func (s *Service) RestoreSpace(spaceID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("RestoreSpace", "app.space.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Restoring space", "space_id", spaceID)
	if err := s.store.RestoreSpace(spaceID); err != nil {
		var invErr *store.ErrInvalidInput
		if errors.As(err, &invErr) && invErr.Reason == store.ReasonNotDeleted {
			// The space row is already live. If its backing channel is still archived, this is a
			// retry of a prior restore that completed the DB half but failed to un-archive the
			// channel (see the error returned by restoreSpaceChannel below) — finish that step now
			// instead of leaving the caller permanently stuck on this 400. If the channel is also
			// already live, there is genuinely nothing to retry, so fall through to the normal
			// not_deleted rejection below.
			if space, appErr := s.retryStuckChannelRestore(spaceID); space != nil || appErr != nil {
				if appErr == nil {
					s.publishToChannels(wsEventSpaceRestored, map[string]any{"space_id": space.Id}, space.ChannelId)
				}
				return space, appErr
			}
		}
		if appErr := restoreReasonAppError(err, map[string]*mmmodel.AppError{
			store.ReasonNotDeleted: mmmodel.NewAppError("RestoreSpace", "app.space.restore.not_deleted.app_error", nil, "", http.StatusConflict),
		}); appErr != nil {
			return nil, appErr
		}
		return nil, storeAppError("RestoreSpace", err)
	}
	space, getErr := readBackAfterRestore(
		mmmodel.NewAppError("RestoreSpace", "app.space.restore.read_back_failed.app_error", nil, "", http.StatusInternalServerError),
		func() (*model.Space, *mmmodel.AppError) {
			return s.GetSpace(spaceID)
		})
	if getErr != nil {
		return nil, getErr
	}
	if appErr := s.restoreSpaceChannel(space); appErr != nil {
		return nil, appErr
	}
	s.publishToChannels(wsEventSpaceRestored, map[string]any{"space_id": space.Id}, space.ChannelId)
	return space, nil
}

// retryStuckChannelRestore checks whether spaceID's backing channel is still archived despite the
// space row already being live — the signature of a prior RestoreSpace call that completed the DB
// half but failed on the channel un-archive (restoreSpaceChannel below). If so, it completes the
// channel restore now and returns the space. A (nil, nil) return means the channel is already
// live and there was nothing to retry; the caller then handles the original not_deleted error
// normally.
func (s *Service) retryStuckChannelRestore(spaceID string) (*model.Space, *mmmodel.AppError) {
	got, getErr := s.GetSpace(spaceID)
	if getErr != nil {
		return nil, getErr
	}
	if s.client == nil {
		return nil, nil
	}
	archived, getChanErr := s.backingChannelArchived(got.ChannelId)
	if getChanErr != nil {
		return nil, mmmodel.NewAppError("RestoreSpace", "app.space.restore.channel_restore_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(getChanErr)
	}
	if !archived {
		return nil, nil
	}
	if appErr := s.restoreSpaceChannel(got); appErr != nil {
		return nil, appErr
	}
	return got, nil
}

// backingChannelArchived reports whether channelID resolves to a channel that is currently
// archived. A channel that no longer exists reports false: there is nothing left to un-archive.
func (s *Service) backingChannelArchived(channelID string) (bool, error) {
	channel, err := s.client.Channel.GetChannelOfType(channelID, mmmodel.ChannelTypeSpace)
	if err != nil {
		// The pluginapi client normalizes a 404 to its own sentinel rather than passing the
		// AppError through, so the status code is not readable from the returned error.
		if errors.Is(err, pluginapi.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return channel != nil && channel.DeleteAt != 0, nil
}

// restoreSpaceChannel un-archives space's backing channel. No-op when client is nil or ChannelId is
// empty. A channel that is already live is treated as success rather than an error: DeleteSpace
// archives the channel best-effort, so a soft-deleted space can legitimately own a channel that was
// never archived, and core rejects un-archiving a live channel with a 400. Failing here would leave
// such a space permanently un-restorable — the row restores, the channel call fails, and a retry is
// then rejected because the row is already live.
func (s *Service) restoreSpaceChannel(space *model.Space) *mmmodel.AppError {
	if s.client == nil {
		return nil
	}
	if err := s.client.Channel.Restore(space.ChannelId); err != nil {
		// Distinguish "nothing to un-archive" from a genuine failure. Only a live (or absent)
		// channel is benign; if it is still archived the un-archive really did fail.
		archived, checkErr := s.backingChannelArchived(space.ChannelId)
		if checkErr == nil && !archived {
			s.log.Warn("backing channel was already live; treating un-archive as a no-op", "channel_id", space.ChannelId, "space_id", space.Id, "err", err)
			return nil
		}
		return mmmodel.NewAppError("RestoreSpace", "app.space.restore.channel_restore_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return nil
}

// GetSpacePages returns one page of metadata summaries for a space's live pages, plus whether
// more exist beyond it. space is the caller's already-fetched record (from its membership gate),
// so no re-read here.
func (s *Service) GetSpacePages(space *model.Space, page, perPage int) ([]*model.PageSummary, bool, *mmmodel.AppError) {
	if space == nil {
		return nil, false, mmmodel.NewAppError("GetSpacePages", "app.space.get_pages.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetSpacePages(space.Id, offset, limit)
	if storeErr != nil {
		return nil, false, storeAppError("GetSpacePages", storeErr)
	}
	pages, hasMore := trimPage(pages, limit)
	return pages, hasMore, nil
}
