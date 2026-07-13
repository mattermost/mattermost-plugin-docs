// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"
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

// hasOtherAuthorizedMember reports whether space has at least one backing-channel member other
// than excludeUserID who can still reach the space — for a team space, one who is still an
// active member of the team. Former team members keep their channel-member rows after leaving
// the team, so counting raw rows would let the last reachable member be removed and leave the
// space stranded behind members who all fail the team half of the access gate.
func (s *Service) hasOtherAuthorizedMember(space *model.Space, excludeUserID string) (bool, error) {
	found := false
	err := s.forEachChannelMember(space.ChannelId, func(cm *mmmodel.ChannelMember) (bool, error) {
		if cm.UserId == excludeUserID {
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
		found = active
		return found, nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
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

// CreateSpace creates a ChannelTypeSpace ("S") backing channel via pluginapi, saves the
// space row pointing at it, and adds the creator as a member. space.ChannelId must be empty —
// it is set from the created channel. If the row save fails, the backing channel is archived
// to avoid an orphan.
//
// The channel create and the row save are separate systems with no shared transaction: a crash
// between them leaves a real channel with no space row and no persisted marker to key a retry
// off, so that window is cleaned up only by the best-effort compensating archive below (or an
// operator, if that also fails).
func (s *Service) CreateSpace(space *model.Space, userID string) (*model.Space, *mmmodel.AppError) {
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
	// Sanitize before it's used as the channel Header below — Space.PreSave sanitizes it again on
	// the store.CreateSpace path, but that happens after the channel is already created.
	space.Description = mmmodel.SanitizeUnicode(space.Description)
	space.CreatorId = userID

	s.log.Debug("Creating space", "team_id", space.TeamId, "user_id", userID)

	backingChannel := &mmmodel.Channel{
		TeamId:    space.TeamId,
		Type:      mmmodel.ChannelTypeSpace,
		Name:      "space-" + mmmodel.NewId()[:20],
		CreatorId: userID,
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

	space.ChannelId = backingChannel.Id

	saved, err := s.store.CreateSpace(space)
	if err != nil {
		s.archiveOrphanChannel(backingChannel.Id, "row save failed", err)
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

// CheckSpaceMembership verifies that userID is a member of the space's backing channel — and,
// when the space belongs to a team, still an active member of that team — and returns the
// fetched space on success so callers can avoid a redundant read. The team check exists because
// leaving a team does not remove a user from the team's space channels (core's team-leave sweep
// covers only regular message channels), so channel membership alone would let a former team
// member keep using known space and page IDs. When includeDeleted is true the space row is
// fetched regardless of its DeleteAt state, which is required for operations that run against a
// soft-deleted space (e.g. restore). Non-members, former team members, and non-existent spaces
// all yield the same 403 to prevent callers from probing space existence via the error code. A
// missing or malformed userID is rejected, never treated as a trusted caller; callers that
// legitimately act without a user must read the space directly instead.
func (s *Service) CheckSpaceMembership(spaceID, userID string, includeDeleted bool) (*model.Space, *mmmodel.AppError) {
	if appErr := s.requireClient("CheckSpaceMembership", "space_id", spaceID, "user_id", userID); appErr != nil {
		return nil, appErr
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	var space *model.Space
	var getErr *mmmodel.AppError
	if includeDeleted {
		space, getErr = s.GetSpaceWithDeleted(spaceID)
	} else {
		space, getErr = s.GetSpace(spaceID)
	}
	if getErr != nil {
		if getErr.StatusCode == http.StatusNotFound {
			return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden).Wrap(getErr)
		}
		return nil, getErr
	}
	if space.TeamId != "" {
		active, teamErr := s.isActiveTeamMember(space.TeamId, userID)
		if teamErr != nil {
			return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(teamErr)
		}
		if !active {
			return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden)
		}
	}
	if _, err := s.client.Channel.GetMember(space.ChannelId, userID); err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden).Wrap(err)
		}
		return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return space, nil
}

// ListSpaceMembers returns one page of space's members plus whether more members exist beyond
// it. page/perPage are normalized like every other paginated method (page and perPage both
// clamped). The pluginapi member listing is page-indexed rather than offset-based, so when the
// requested page comes back full a one-row probe at the next page's first slot decides has-more.
// space is the caller's already-fetched record (from its membership gate), so no re-read here.
func (s *Service) ListSpaceMembers(space *model.Space, page, perPage int) ([]*model.SpaceMember, bool, *mmmodel.AppError) {
	if space == nil {
		return nil, false, mmmodel.NewAppError("ListSpaceMembers", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("ListSpaceMembers", "space_id", space.Id); appErr != nil {
		return nil, false, appErr
	}
	page = ClampPage(page)
	perPage = ClampPerPage(perPage)
	channelMembers, err := s.client.Channel.ListMembers(space.ChannelId, page, perPage)
	if err != nil {
		return nil, false, mmmodel.NewAppError("ListSpaceMembers", "app.space.list_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	members := make([]*model.SpaceMember, 0, len(channelMembers))
	for _, cm := range channelMembers {
		members = append(members, &model.SpaceMember{UserId: cm.UserId})
	}
	hasMore := false
	if len(channelMembers) == perPage {
		// A page of size 1 holds exactly one element, so its page index equals that element's
		// offset: requesting page (page+1)*perPage at size 1 fetches precisely the first member
		// beyond the current window.
		probe, probeErr := s.client.Channel.ListMembers(space.ChannelId, (page+1)*perPage, 1)
		if probeErr != nil {
			return nil, false, mmmodel.NewAppError("ListSpaceMembers", "app.space.list_members.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(probeErr)
		}
		hasMore = len(probe) > 0
	}
	return members, hasMore, nil
}

// AddSpaceMember adds a user to space's backing channel. Any current space member may manage
// members (flat model; no per-space admin role yet). space is the caller's already-fetched
// record (from its membership gate), so no re-read here.
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
	// space member can pass the team half of the access gate — which the last-member guard in
	// RemoveSpaceMember relies on when deciding who can still reach the space.
	if space.TeamId != "" {
		active, memberErr := s.isActiveTeamMember(space.TeamId, userID)
		if memberErr != nil {
			return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
		if !active {
			return nil, mmmodel.NewAppError("AddSpaceMember", "app.space.member.not_team_member.app_error", nil, "", http.StatusForbidden)
		}
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
	return &model.SpaceMember{UserId: member.UserId}, nil
}

// RemoveSpaceMember removes a user from space's backing channel. The last member who can still
// reach the space cannot be removed: membership is the only gate on every space and page route,
// so a space with no reachable member — and every page in it — would be permanently unreachable
// through the plugin API (there is no admin bypass, and adding a member back requires the caller
// to already be one). Reachable means passing the full access gate, so for a team space a member
// who has since left the team does not count — see hasOtherAuthorizedMember. space is the
// caller's already-fetched record (from its membership gate), so no re-read here.
func (s *Service) RemoveSpaceMember(space *model.Space, userID string) *mmmodel.AppError {
	if space == nil {
		return mmmodel.NewAppError("RemoveSpaceMember", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := s.requireClient("RemoveSpaceMember", "space_id", space.Id, "user_id", userID); appErr != nil {
		return appErr
	}
	// The member-list read and the removal below are separate calls, so on their own two
	// concurrent removals of a two-member space's remaining members could each pass the
	// last-member guard and leave the space memberless — unreachable through the plugin API.
	// The space-scoped advisory lock serializes the guard and the removal as one unit.
	lockErr := s.store.WithSpaceMembershipLock(space.Id, func() error {
		// Removing a non-member alongside a sole reachable member falls through the guard: the
		// DeleteMember call below reports that failure.
		hasOther, guardErr := s.hasOtherAuthorizedMember(space, userID)
		if guardErr != nil {
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(guardErr)
		}
		if !hasOther {
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.last_member.app_error", nil, "", http.StatusConflict)
		}
		if err := s.client.Channel.DeleteMember(space.ChannelId, userID); err != nil {
			// A target user who isn't a member (or doesn't exist) is the caller's mistake,
			// not a server fault.
			if errors.Is(err, pluginapi.ErrNotFound) {
				return mmmodel.NewAppError("RemoveSpaceMember", "app.space.member.user_not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
			}
			return mmmodel.NewAppError("RemoveSpaceMember", "app.space.remove_member.failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
		return nil
	})
	if lockErr != nil {
		var appErr *mmmodel.AppError
		if errors.As(lockErr, &appErr) {
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

// GetSpacesForTeam returns one page of a team's live spaces, plus whether more exist beyond
// it. userID is verified to be a team member and the result is filtered to spaces whose
// backing channel the caller belongs to (resolved in the store via ChannelMembers), matching
// the membership gate on single-space reads.
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
	spaces, err := s.store.GetSpacesForTeam(teamID, userID, offset, limit)
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
// baseline yields a conflict unless force overrides it with last-write-wins; a nil
// expectedUpdateAt without force is rejected. The store merges the patch into the row it reads
// under lock, so a forced update overwrites only the fields the patch supplies — concurrent
// changes to other fields survive. space is the caller's already-fetched record (from its
// membership gate); only its Id is used here.
func (s *Service) UpdateSpace(space *model.Space, patch *model.SpacePatch, expectedUpdateAt *int64, force bool) (*model.Space, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := requireBaseline("UpdateSpace", "expected_update_at", expectedUpdateAt, force); appErr != nil {
		return nil, appErr
	}
	if appErr := normalizeAndValidateSpacePatch("UpdateSpace", patch); appErr != nil {
		return nil, appErr
	}

	s.log.Debug("Updating space", "space_id", space.Id)

	updated, err := s.store.UpdateSpace(space.Id, patch, mmmodel.SafeDereference(expectedUpdateAt), force)
	if err != nil {
		return nil, storeAppError("UpdateSpace", err)
	}
	if updated.ChannelId != "" && s.client != nil {
		if chanErr := s.syncSpaceChannelMetadata(updated.Id); chanErr != nil {
			// Deliberately not returned: the space row (the source of truth) committed, so failing
			// the request would misreport a successful update, and retrying it would 409 on the
			// now-stale baseline. The next successful UpdateSpace re-syncs the channel. Logged at
			// Error so the resulting name/header divergence is visible to operators.
			s.log.Error("UpdateSpace: failed to sync backing channel metadata; display name/header stale until the next update", "channel_id", updated.ChannelId, "space_id", updated.Id, "err", chanErr)
		}
	}
	s.publishToChannels(wsEventSpaceUpdated, map[string]any{"space_id": updated.Id}, updated.ChannelId)
	return updated, nil
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
		// The channel broadcast reaches the members only if the archive above also failed and left
		// the channel live; with no snapshot it is still the best remaining delivery attempt.
		s.publishToChannels(wsEventSpaceDeleted, map[string]any{"space_id": space.Id}, space.ChannelId)
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
