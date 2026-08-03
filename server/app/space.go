// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"
	"slices"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

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

// requireClient rejects the operation when the pluginapi client is not wired, which every
// membership-gated space operation depends on. where identifies the calling operation for the
// log line and the returned AppError; kv are its extra log context pairs.
func (s *Service) requireClient(where string, kv ...any) *mmmodel.AppError {
	if s.client != nil {
		return nil
	}
	s.log.Warn("pluginapi client not wired; denying access", append([]any{"operation", where}, kv...)...)
	return mmmodel.NewAppError(where, "app.space.client_not_wired.app_error", nil, "", http.StatusInternalServerError)
}

// isActiveTeamMember reports whether userID currently belongs to teamID. Core keeps removed
// team members as rows with DeleteAt set — and GetMember returns such a row without error — so
// a missing row and a soft-deleted row both read as "not a member". Space access must check
// this, not just backing-channel membership: leaving a team does not remove a user from the
// team's space channels, so channel membership alone would let a former team member keep using
// known space and page IDs.
func (s *Service) isActiveTeamMember(teamID, userID string) (bool, error) {
	member, err := s.client.Team.GetMember(teamID, userID)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return member.DeleteAt == 0, nil
}

// forEachChannelMember visits every member of channelID page by page. Iteration ends early
// when visit returns stop=true or an error; the error is returned as-is.
func (s *Service) forEachChannelMember(channelID string, visit func(cm *mmmodel.ChannelMember) (stop bool, err error)) error {
	for page := 0; ; page++ {
		members, err := s.client.Channel.ListMembers(channelID, page, PerPageMaximum)
		if err != nil {
			return err
		}
		for _, cm := range members {
			stop, visitErr := visit(cm)
			if visitErr != nil {
				return visitErr
			}
			if stop {
				return nil
			}
		}
		if len(members) < PerPageMaximum {
			return nil
		}
	}
}

// hasOtherAuthorizedMemberMatching reports whether space has at least one backing-channel member
// other than excludeUserID that satisfies matches and can still reach the space — one who is also
// an active member of the space's team. Former team members keep their channel-member rows after
// leaving the team, so counting raw rows would let the last reachable member be removed and leave
// the space stranded behind members who all fail the team half of the access gate. Iteration stops
// at the first match. The no-team branch below is unreachable through CreateSpace, which requires
// a team id.
func (s *Service) hasOtherAuthorizedMemberMatching(space *model.Space, excludeUserID string, matches func(cm *mmmodel.ChannelMember) bool) (bool, error) {
	found := false
	err := s.forEachChannelMember(space.ChannelId, func(cm *mmmodel.ChannelMember) (bool, error) {
		if cm.UserId == excludeUserID || !matches(cm) {
			return false, nil
		}
		if space.TeamId == "" {
			found = true
			return true, nil
		}
		active, activeErr := s.isActiveTeamMember(space.TeamId, cm.UserId)
		if activeErr != nil {
			return false, activeErr
		}
		if active {
			found = true
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// hasOtherAuthorizedMember reports whether any backing-channel member other than excludeUserID can
// still reach the space.
func (s *Service) hasOtherAuthorizedMember(space *model.Space, excludeUserID string) (bool, error) {
	return s.hasOtherAuthorizedMemberMatching(space, excludeUserID, func(*mmmodel.ChannelMember) bool { return true })
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
// requested capabilities. A set matching one of the seeded presets resolves to that preset's
// scheme, which is shared by every space using the same preset. Any other set gets a scheme
// created here and used by this space alone.
//
// A custom scheme is returned with its generated roles still carrying core's default channel
// baseline; the caller gives them their exact permission sets through configureSpaceCustomScheme
// once a backing channel points at the scheme, which is what lets core admit the role writes.
//
// customRoles is non-nil only in the second case, and names the roles that configure step must
// write. Its nil-ness also tells the two cases apart when a later step fails: a scheme created here
// is referenced by nothing else, so the caller must delete it (see cleanupCustomScheme), while a
// preset scheme is shared with other spaces and must be left alone.
func (s *Service) resolveSpaceScheme(capabilities []string) (schemeID string, customRoles *schemeRoles, err error) {
	// Normalize before the permission set is persisted: the validators are dedup-tolerant, so
	// without this a request repeating one allowlisted token would write that repetition verbatim
	// into the generated role's Permissions column.
	capabilities = model.NormalizeCapabilitySet(capabilities)
	if presetName, ok := model.SchemeNameForDefaultCapabilities(capabilities); ok {
		id, getErr := s.getSchemeIDByName(presetName)
		if getErr != nil {
			return "", nil, getErr
		}
		return id, nil, nil
	}
	return s.createSpaceCustomScheme()
}

// cleanupCustomScheme best-effort retires schemeID when createdCustom is true and a create-step
// fails before any channel references the scheme (channel create never happened, or the repoint
// onto it failed), so the scheme is already unreferenced and DeleteScheme accepts it directly.
func (s *Service) cleanupCustomScheme(schemeID string, createdCustom bool) {
	if !createdCustom {
		return
	}
	if err := s.retireSpaceCustomScheme(schemeID); err != nil {
		s.log.Error("failed to retire space custom scheme after a failed create; it must be deleted manually", "scheme_id", schemeID, "err", err)
	}
}

// abandonBackingChannel runs the compensating cleanup shared by every CreateSpace failure that
// happens after the backing channel exists: retire the custom scheme it was created for, then
// archive the channel. The scheme retirement clears the channel's scheme reference first, because
// archiving alone would not clear it and core still counts an archived space channel as a live
// reference that blocks the delete (see detachAndDeleteCustomScheme).
func (s *Service) abandonBackingChannel(channelID, reason string, cause error, schemeID string, createdCustom bool) {
	if createdCustom {
		s.detachAndDeleteCustomScheme(channelID, schemeID)
	}
	s.archiveOrphanChannel(channelID, reason, cause)
}

// CreateSpace creates a ChannelTypeSpace ("S") backing channel via pluginapi, saves the
// space row pointing at it, and adds the creator as a member with SchemeAdmin. space.ChannelId
// must be empty — it is set from the created channel. defaultCapabilities nil defaults to the
// contribute preset; viewAccess nil defaults to open. If any step after the backing channel's
// creation fails, the backing channel is archived to avoid an orphan, and a newly created custom
// scheme (a non-preset defaultCapabilities) is best-effort retired.
//
// The channel create and the row save are separate systems with no shared transaction: a crash
// between them leaves a real channel with no space row and no persisted marker to key a retry
// off, so that window is cleaned up only by the best-effort compensating archive below (or an
// operator, if that also fails).
func (s *Service) CreateSpace(space *model.Space, userID string, defaultCapabilities *[]string, viewAccess *string) (*model.Space, *mmmodel.AppError) {
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
	// Reject a creator who isn't an active member of the target team before standing up a backing
	// channel there — otherwise any authenticated user could create a real, visible channel in any
	// team by supplying its id.
	active, memberErr := s.isActiveTeamMember(space.TeamId, userID)
	if memberErr != nil {
		// A transient/backend failure must not be misreported as "not a team member".
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
	}
	if !active {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.not_team_member.app_error", nil, "", http.StatusForbidden)
	}
	// Team membership alone does not authorize creating a space in it: the caller must also hold
	// create_space on the team (or be sysadmin). Unlike the read/manage/delete gates, no space
	// exists yet here, so there is nothing to existence-hide behind — a plain 403 is correct.
	if !s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) &&
		!s.client.User.HasPermissionToTeam(userID, space.TeamId, mmmodel.PermissionCreateSpace) {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.forbidden.app_error", nil, "", http.StatusForbidden)
	}
	// Sanitize before it's used as the channel Header below — Space.PreSave sanitizes it again on
	// the store.CreateSpace path, but that happens after the channel is already created.
	space.Description = mmmodel.SanitizeUnicode(space.Description)
	space.CreatorId = userID

	va := model.ViewAccessOpen
	if viewAccess != nil {
		va = *viewAccess
	}
	if va != model.ViewAccessOpen && va != model.ViewAccessPrivate {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.invalid_view_access.app_error", nil, "", http.StatusBadRequest)
	}
	space.ViewAccess = va

	capabilities, _ := model.DefaultCapabilitiesForSchemeName(mmmodel.SchemeNameSpaceContribute)
	if defaultCapabilities != nil {
		capabilities = *defaultCapabilities
	}
	if capErr := model.ValidateDefaultCapabilities(capabilities); capErr != nil {
		return nil, capErr
	}

	schemeID, customRoles, schemeErr := s.resolveSpaceScheme(capabilities)
	if schemeErr != nil {
		return nil, storeAppError("CreateSpace", schemeErr)
	}
	createdCustom := customRoles != nil

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
			s.abandonBackingChannel(backingChannel.Id, "channel create failed after creation", err, schemeID, createdCustom)
		} else {
			// No channel row exists, so nothing references the scheme.
			s.cleanupCustomScheme(schemeID, createdCustom)
		}
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.backing_channel_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}

	// Only now that the backing channel points at the scheme will core admit role writes carrying
	// space permissions, so a freshly created custom scheme gets its exact permission sets here
	// rather than at create time.
	if createdCustom {
		if cfgErr := s.configureSpaceCustomScheme(customRoles, capabilities); cfgErr != nil {
			s.abandonBackingChannel(backingChannel.Id, "custom scheme role configuration failed", cfgErr, schemeID, createdCustom)
			return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.scheme_configure_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(cfgErr)
		}
	}

	if _, addErr := s.client.Channel.AddMember(backingChannel.Id, userID); addErr != nil {
		// A space whose creator is not a member of its backing channel is a dead-end once per-space
		// membership gating lands (unreachable to everyone, creator included), so fail the create
		// and archive the orphan channel rather than continuing.
		s.abandonBackingChannel(backingChannel.Id, "creator member-add failed", addErr, schemeID, createdCustom)
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.add_member_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(addErr)
	}

	// The creator is added as SchemeAdmin — both the scheme's resolved generated role names, never
	// literals: on a scheme-backed channel core rejects the literal channel_user/channel_admin
	// tokens. The base user-role token is required, not optional (core resets all scheme flags and
	// rejects a string that leaves SchemeUser unset).
	resolvedRoles, rolesErr := s.getSchemeRolesForChannel(backingChannel.Id)
	if rolesErr != nil {
		s.abandonBackingChannel(backingChannel.Id, "scheme role lookup failed", rolesErr, schemeID, createdCustom)
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.scheme_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(rolesErr)
	}
	if _, roleErr := s.client.Channel.UpdateChannelMemberRoles(backingChannel.Id, userID, resolvedRoles.UserRoleName+" "+resolvedRoles.AdminRoleName); roleErr != nil {
		s.abandonBackingChannel(backingChannel.Id, "creator admin role assignment failed", roleErr, schemeID, createdCustom)
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.admin_role_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(roleErr)
	}

	space.ChannelId = backingChannel.Id

	saved, err := s.store.CreateSpace(space)
	if err != nil {
		s.abandonBackingChannel(backingChannel.Id, "row save failed", err, schemeID, createdCustom)
		return nil, storeAppError("CreateSpace", err)
	}

	s.publishToChannels(wsEventSpaceCreated, map[string]any{"space_id": saved.Id}, saved.ChannelId)

	return saved, nil
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

// spaceDefaultCapabilities returns space's current default capability set in wire form
// (read_page-free): the generated user role's stored permission set projected onto the capability
// vocabulary. The projection covers presets and space-private custom schemes alike, since a
// preset's generated user role carries exactly that preset's capabilities.
func (s *Service) spaceDefaultCapabilities(space *model.Space) ([]string, error) {
	roles, err := s.getSchemeRolesForChannel(space.ChannelId)
	if err != nil {
		return nil, err
	}
	return s.defaultCapabilitiesForRoles(roles)
}

// spaceDefaultCapabilitiesFromChannel is spaceDefaultCapabilities for a caller that already holds
// the backing channel.
func (s *Service) spaceDefaultCapabilitiesFromChannel(channelID string, channel *mmmodel.Channel) ([]string, error) {
	roles, err := s.schemeRolesFromChannel(channelID, channel)
	if err != nil {
		return nil, err
	}
	return s.defaultCapabilitiesForRoles(roles)
}

// defaultCapabilitiesForRoles is spaceDefaultCapabilities for a caller that already holds the
// backing channel's scheme roles.
func (s *Service) defaultCapabilitiesForRoles(roles *schemeRoles) ([]string, error) {
	perms, err := s.getRolePermissionsByName(roles.UserRoleName)
	if err != nil {
		return nil, err
	}
	return model.DefaultCapabilitiesFromPermissions(perms), nil
}

// BuildSpaceWithAccess resolves the GET /spaces/{id} response wrapper: the space's default
// capability set plus the caller's own truthful, current effective capabilities — never a
// hypothetical post-join grant. A denied read yields the shared existence-hiding 403.
func (s *Service) BuildSpaceWithAccess(space *model.Space, userID string) (*model.SpaceWithAccess, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("BuildSpaceWithAccess", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("BuildSpaceWithAccess", "space_id", space.Id, "user_id", userID); appErr != nil {
		return nil, appErr
	}
	// The read gate resolves first: a denied caller must get the existence-hiding 403 rather than
	// whatever the default-capability lookup below would surface for a space it cannot see.
	resolution, resErr := s.ResolveSpaceRead("BuildSpaceWithAccess", space, userID)
	if resErr != nil {
		return nil, resErr
	}
	if resolution == ReadDenied {
		return nil, existenceHidingForbidden("BuildSpaceWithAccess")
	}
	defaultCapabilities, err := s.spaceDefaultCapabilities(space)
	if err != nil {
		return nil, storeAppError("BuildSpaceWithAccess", err)
	}

	var capabilities []string
	switch resolution {
	case ReadViaSysadmin:
		capabilities = model.AdminEffectiveCapabilities()
	case ReadViaMember:
		member, memErr := s.client.Channel.GetMember(space.ChannelId, userID)
		if memErr != nil {
			return nil, mmmodel.NewAppError("BuildSpaceWithAccess", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memErr)
		}
		capabilities = model.CapabilitiesFromMember(member.ExplicitRoles, member.SchemeAdmin, member.SchemeGuest, defaultCapabilities).Effective
	case ReadViaOpenFallthrough:
		capabilities = []string{model.CapabilityReadPage}
	default:
		return nil, existenceHidingForbidden("BuildSpaceWithAccess")
	}

	wrapper := &model.SpaceWithAccess{Space: *space, DefaultCapabilities: defaultCapabilities, Capabilities: capabilities}
	wrapper.EnsureCapabilities()
	return wrapper, nil
}

// SetSpaceDefaultCapabilities changes space's default capability set: a set matching a seeded
// preset repoints the backing channel at the shared preset scheme; any other set creates a new
// immutable space-private custom scheme and repoints, retiring the previous custom scheme once
// unreferenced. The repoint goes through pluginapi Channel.Update (not a store-direct write) so
// core's member-cache invalidation runs and the new scheme takes effect on the next permission
// check, rather than when the cache expires.
func (s *Service) SetSpaceDefaultCapabilities(space *model.Space, capabilities []string, actingUserID string) (*model.SpaceWithAccess, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("SetSpaceDefaultCapabilities", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := model.ValidateDefaultCapabilities(capabilities); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("SetSpaceDefaultCapabilities", "space_id", space.Id); appErr != nil {
		return nil, appErr
	}

	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		channel, chanErr := s.client.Channel.GetChannelOfType(space.ChannelId, mmmodel.ChannelTypeSpace)
		if chanErr != nil {
			return mmmodel.NewAppError("SetSpaceDefaultCapabilities", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(chanErr)
		}
		if channel == nil || channel.SchemeId == nil {
			return mmmodel.NewAppError("SetSpaceDefaultCapabilities", "app.space.default_capabilities.channel_scheme_missing.app_error", nil, "", http.StatusInternalServerError)
		}
		currentSchemeID := *channel.SchemeId
		// Project the superseded scheme's capability set from the channel already in hand, before
		// the repoint below rewrites which scheme the channel points at. A failure here only costs
		// the no-op shortcut, so it is carried as a value rather than failing the whole operation.
		liveCapabilities, liveCapabilitiesErr := s.spaceDefaultCapabilitiesFromChannel(space.ChannelId, channel)

		requested := model.NormalizeCapabilitySet(capabilities)
		_, requestedIsPreset := model.SchemeNameForDefaultCapabilities(requested)

		// A non-preset set always mints a fresh scheme, so the id comparison below could never
		// recognize an unchanged custom set; compare the projected capabilities instead. A preset
		// request is left to that id comparison, which settles it by scheme identity — the
		// projection cannot, because a custom scheme whose roles were never configured projects to
		// the same empty set as the read-only preset, and shortcutting there would strand the space
		// on that unconfigured scheme with no way to move off it.
		if !requestedIsPreset && liveCapabilitiesErr == nil && slices.Equal(liveCapabilities, requested) {
			return nil
		}

		targetSchemeID, customRoles, schemeErr := s.resolveSpaceScheme(capabilities)
		if schemeErr != nil {
			return storeAppError("SetSpaceDefaultCapabilities", schemeErr)
		}
		createdCustom := customRoles != nil
		if targetSchemeID == currentSchemeID {
			// No-op: requested set already matches the live default.
			return nil
		}

		channel.SchemeId = &targetSchemeID
		if updErr := s.client.Channel.Update(channel); updErr != nil {
			s.cleanupCustomScheme(targetSchemeID, createdCustom)
			return mmmodel.NewAppError("SetSpaceDefaultCapabilities", "app.space.default_capabilities.repoint_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updErr)
		}

		// The repoint above is what lets core admit role writes carrying space permissions, so a
		// freshly created custom scheme is configured only now. A failure leaves the space on a
		// scheme whose roles still hold core's default channel baseline, so the repoint is undone
		// before the unusable scheme is retired.
		if createdCustom {
			if cfgErr := s.configureSpaceCustomScheme(customRoles, capabilities); cfgErr != nil {
				channel.SchemeId = &currentSchemeID
				if rollbackErr := s.client.Channel.Update(channel); rollbackErr != nil {
					s.log.Error("failed to restore the previous space scheme after a failed custom-scheme configuration; the space is left on an unconfigured scheme", "channel_id", space.ChannelId, "scheme_id", targetSchemeID, "previous_scheme_id", currentSchemeID, "err", rollbackErr)
				} else {
					s.cleanupCustomScheme(targetSchemeID, createdCustom)
				}
				return mmmodel.NewAppError("SetSpaceDefaultCapabilities", "app.space.default_capabilities.scheme_configure_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(cfgErr)
			}
		}

		// Retire the superseded scheme, but only inside this lock (a concurrent repoint between an
		// outside-the-lock read and the retire call could otherwise delete a scheme a racing
		// SetSpaceDefaultCapabilities just repointed away from) and only when it was a
		// space-private custom one — a routine preset-to-preset switch must neither call the
		// retire path nor log a spurious failure. Preset membership is settled by scheme id rather
		// than by the projected capability set, which cannot tell a preset apart from a custom
		// scheme whose roles were never given their permission sets.
		supersededIsPreset, presetErr := s.isPresetSchemeID(currentSchemeID)
		switch {
		case presetErr != nil:
			s.log.Warn("failed to determine whether the superseded space scheme is a preset; skipping custom-scheme retirement", "scheme_id", currentSchemeID, "err", presetErr)
		case !supersededIsPreset:
			if delErr := s.retireSpaceCustomScheme(currentSchemeID); delErr != nil {
				s.log.Error("failed to retire space custom scheme after a default-capabilities change; it must be deleted manually", "scheme_id", currentSchemeID, "err", delErr)
			}
		}
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("SetSpaceDefaultCapabilities", lockErr)
	}

	fresh, getErr := s.GetSpace(space.Id)
	if getErr != nil {
		// The scheme repoint already committed, so re-reporting this as a failure would misreport
		// success as an error; project the response from the requested set and the pre-update
		// space instead, still firing the WS event.
		s.log.Warn("SetSpaceDefaultCapabilities: post-commit re-read failed; responding from the requested set", "space_id", space.Id, "err", getErr)
		s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": space.Id}, space.ChannelId)
		wrapper, buildErr := s.BuildSpaceWithAccess(space, actingUserID)
		if buildErr != nil {
			return nil, buildErr
		}
		wrapper.DefaultCapabilities = model.NormalizeCapabilitySet(capabilities)
		return wrapper, nil
	}
	s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": fresh.Id}, fresh.ChannelId)
	return s.BuildSpaceWithAccess(fresh, actingUserID)
}

// GetSpaceMembers, AddSpaceMember, SetSpaceMemberCapabilities, and RemoveSpaceMember live in
// space_members.go alongside the escalation and last-admin guards.

// GetSpacesForTeam returns one page of a team's live spaces, plus whether more exist beyond it.
// userID must be an active team member holding team read_space (the list-entry gate; every
// team_user holds it by default). The result is the union of spaces the caller is a
// backing-channel member of and open spaces the caller can reach via the same team
// read_public_channel/compliance-mode conjunct as single-space read.
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
	active, memberErr := s.isActiveTeamMember(teamID, userID)
	if memberErr != nil {
		return nil, false, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
	}
	if !active {
		return nil, false, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.not_team_member.app_error", nil, "", http.StatusForbidden)
	}
	if !s.client.User.HasPermissionTo(userID, mmmodel.PermissionManageSystem) &&
		!s.client.User.HasPermissionToTeam(userID, teamID, mmmodel.PermissionReadSpace) {
		return nil, false, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.forbidden.app_error", nil, "", http.StatusForbidden)
	}
	callerHasOpenFallthrough := active && s.hasOpenTeamFallthrough(userID, teamID)
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
	if patch.ViewAccess != nil && *patch.ViewAccess != model.ViewAccessOpen && *patch.ViewAccess != model.ViewAccessPrivate {
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
// Any patch on a space with a backing channel runs under the space's membership advisory lock (the
// same lock AutoJoinIfDefaultGranted and the last-admin/last-member guards use), because it also
// drives the channel-metadata sync below, which must not race a concurrent membership change.
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

	apply := func() (*model.Space, *mmmodel.AppError) {
		var live *model.Space
		if patch.ViewAccess != nil {
			gotLive, liveErr := s.store.GetSpace(space.Id, false)
			if liveErr != nil {
				return nil, storeAppError("UpdateSpace", liveErr)
			}
			live = gotLive
			if *patch.ViewAccess != live.ViewAccess {
				if appErr := s.RequireSpaceAdminOrSysadmin("UpdateSpace", live, actingUserID); appErr != nil {
					return nil, appErr
				}
			}
		}
		updated, err := s.store.UpdateSpace(space.Id, patch, mmmodel.SafeDereference(expectedUpdateAt), force)
		if err != nil {
			return nil, storeAppError("UpdateSpace", err)
		}
		// A private->private no-op (the patch re-asserts the current value) must not attach
		// member_count: only a genuine open->private transition sheds no members but exposes an
		// admin to a count they should act on. Compare against the live row's prior value, not
		// just the patch value.
		flippedToPrivate := patch.ViewAccess != nil && *patch.ViewAccess == model.ViewAccessPrivate &&
			live != nil && live.ViewAccess == model.ViewAccessOpen
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
		payload := map[string]any{"space_id": updated.Id}
		if flippedToPrivate {
			// Privatizing does not shed members: every existing member (including anyone
			// auto-joined earlier) stays. Surface the current count so the admin is prompted to
			// prune via RemoveSpaceMember.
			stats, statsErr := s.client.Channel.GetChannelStats(updated.ChannelId)
			if statsErr != nil {
				// The update itself committed, so this only affects the prompt: the event ships
				// without member_count rather than failing a successful write.
				s.log.Warn("UpdateSpace: member-count lookup failed; the view_access event omits member_count", "channel_id", updated.ChannelId, "space_id", updated.Id, "err", statsErr)
			} else {
				payload["member_count"] = stats.MemberCount
				s.log.Debug("space view_access flipped to private", "space_id", updated.Id, "member_count", stats.MemberCount)
			}
		}
		s.publishToChannels(wsEventSpaceUpdated, payload, updated.ChannelId)
		return updated, nil
	}

	if space.ChannelId == "" {
		return apply()
	}
	var result *model.Space
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		r, appErr := apply()
		if appErr != nil {
			return appErr
		}
		result = r
		return nil
	})
	if lockErr != nil {
		return nil, membershipLockAppError("UpdateSpace", lockErr)
	}
	return result, nil
}

// syncSpaceChannelMetadata projects the space's current Title and Description onto its backing
// channel's display name and header. Called after UpdateSpace commits; errors are logged and
// suppressed by the caller since the space row is the source of truth. The space row is re-read
// here rather than projected from the caller's just-committed value: two updates can commit in
// one order and reach this sync in the other, and projecting each caller's own snapshot would
// let the earlier title win the channel write; projecting the latest committed row makes the
// last sync converge on the newest values. A space deleted in the interim is a no-op — the
// delete path archives the channel itself.
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

// snapshotSpaceMemberIDs returns the user IDs of every backing-channel member of space. A nil
// client or a space with no backing channel yields no members and no error.
func (s *Service) snapshotSpaceMemberIDs(space *model.Space) ([]string, error) {
	if s.client == nil || space.ChannelId == "" {
		return nil, nil
	}
	var ids []string
	err := s.forEachChannelMember(space.ChannelId, func(cm *mmmodel.ChannelMember) (bool, error) {
		ids = append(ids, cm.UserId)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
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
