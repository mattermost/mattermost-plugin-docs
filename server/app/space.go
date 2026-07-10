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

// archiveOrphanChannel archives a backing channel when a later step in space creation fails,
// to avoid an orphaned channel. reason describes the step that failed; cause is its error.
func (s *Service) archiveOrphanChannel(channelID, reason string, cause error) {
	if s.client == nil {
		return
	}
	if delErr := s.client.Channel.Delete(channelID); delErr != nil {
		s.log.Warn("compensating channel archive failed; channel may be orphaned", "channel_id", channelID, "failure_reason", reason, "cause_err", cause, "delete_err", delErr)
	}
}

// CreateSpace creates a ChannelTypeSpace ("S") backing channel via pluginapi, saves the
// space row pointing at it, and adds the creator as a member. space.ChannelId must be empty —
// it is set from the created channel. If the row save fails, the backing channel is archived
// to avoid an orphan.
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
	// Reject a malformed acting user before any channel I/O, mirroring CreatePage. An empty userID is
	// allowed (system caller); the creator member-add below is skipped for it.
	if userID != "" && !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	// The backing channel must exist before the space row is saved; a nil client is a
	// hard precondition failure, not a recoverable skip.
	if s.client == nil {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.no_client.app_error", nil, "", http.StatusInternalServerError)
	}
	// Validate all in-memory fields before the first I/O call, mirroring CreatePage.
	title, titleErr := validateTitle("CreateSpace", space.Title, model.SpaceTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	space.Title = title
	// Validate Description and Icon before creating the backing channel, mirroring replaceSpace.
	if fieldErr := validateSpaceMutableFields("CreateSpace", space.Description, space.Icon); fieldErr != nil {
		return nil, fieldErr
	}
	// Reject a creator who isn't a member of the target team before standing up a backing channel
	// there — otherwise any authenticated user could create a real, visible channel in any team by
	// supplying its id. Skipped for userID == "" (system caller), matching the creator member-add below.
	if userID != "" {
		if _, memberErr := s.client.Team.GetMember(space.TeamId, userID); memberErr != nil {
			// pluginapi normalizes a 404 (no such membership) to ErrNotFound; anything else is a
			// transient/backend failure and must not be misreported as "not a team member".
			if errors.Is(memberErr, pluginapi.ErrNotFound) {
				return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.not_team_member.app_error", nil, "", http.StatusForbidden).Wrap(memberErr)
			}
			return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
	}
	// Sanitize before it's used as the channel Header below — Space.PreSave sanitizes it again on
	// the store.CreateSpace path, but that happens after the channel is already created.
	space.Description = mmmodel.SanitizeUnicode(space.Description)
	space.CreatorId = userID

	displayName, _ := mmmodel.LimitRunes(space.Title, mmmodel.ChannelDisplayNameMaxRunes)

	backingChannel := &mmmodel.Channel{
		TeamId:      space.TeamId,
		Type:        mmmodel.ChannelTypeSpace,
		DisplayName: displayName,
		Name:        "space-" + mmmodel.NewId()[:20],
		Header:      space.Description,
		CreatorId:   userID,
	}
	if err := s.client.Channel.Create(backingChannel); err != nil {
		return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.backing_channel_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}

	if userID != "" {
		if _, addErr := s.client.Channel.AddMember(backingChannel.Id, userID); addErr != nil {
			// A space whose creator is not a member of its backing channel is a dead-end once per-space
			// membership gating lands (unreachable to everyone, creator included), so fail the create
			// and archive the orphan channel rather than continuing.
			s.archiveOrphanChannel(backingChannel.Id, "creator member-add failed", addErr)
			return nil, mmmodel.NewAppError("CreateSpace", "app.space.create.add_member_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(addErr)
		}
	}

	space.ChannelId = backingChannel.Id

	saved, err := s.store.CreateSpace(space)
	if err != nil {
		s.archiveOrphanChannel(backingChannel.Id, "row save failed", err)
		return nil, storeAppError("CreateSpace", err)
	}

	return saved, nil
}

// GetSpace returns the live space with the given ID.
func (s *Service) GetSpace(spaceID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetSpace", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	space, err := s.store.GetSpace(spaceID)
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
	space, err := s.store.GetSpaceWithDeleted(spaceID)
	if err != nil {
		return nil, storeAppError("GetSpaceWithDeleted", err)
	}
	return space, nil
}

// CheckSpaceMembership verifies that userID is a member of the space's backing channel and
// returns the fetched space on success so callers can avoid a redundant read. When
// includeDeleted is true the space row is fetched regardless of its DeleteAt state, which is
// required for operations that run against a soft-deleted space (e.g. restore). Returns
// (nil, nil) for system callers (userID == ""). Non-members and non-existent spaces both yield
// 403 to prevent callers from probing space existence via the error code.
func (s *Service) CheckSpaceMembership(spaceID, userID string, includeDeleted bool) (*model.Space, *mmmodel.AppError) {
	if userID == "" {
		return nil, nil
	}
	if s.client == nil {
		s.log.Warn("CheckSpaceMembership: pluginapi client not wired for authenticated request; denying access", "space_id", spaceID, "user_id", userID)
		return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.client_not_wired.app_error", nil, "", http.StatusInternalServerError)
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
	if space.ChannelId == "" {
		if _, err := s.client.Team.GetMember(space.TeamId, userID); err != nil {
			if errors.Is(err, pluginapi.ErrNotFound) {
				return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden).Wrap(err)
			}
			return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
		return space, nil
	}
	if _, err := s.client.Channel.GetMember(space.ChannelId, userID); err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden).Wrap(err)
		}
		return nil, mmmodel.NewAppError("CheckSpaceMembership", "app.space.access.channel_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return space, nil
}

// GetSpaceForUser returns the space for an authenticated or system caller. For authenticated
// callers it verifies channel membership via CheckSpaceMembership and returns the fetched space;
// for system callers (empty userID) it falls back to a direct GetSpace read.
func (s *Service) GetSpaceForUser(spaceID, userID string) (*model.Space, *mmmodel.AppError) {
	space, appErr := s.CheckSpaceMembership(spaceID, userID, false)
	if appErr != nil || space != nil {
		return space, appErr
	}
	return s.GetSpace(spaceID)
}

// GetSpacesForTeam returns paginated live spaces for a team. A non-empty userID is verified to be
// a team member and the result is filtered to spaces whose backing channel the caller belongs to
// (skipped for system callers with userID == "").
func (s *Service) GetSpacesForTeam(teamID, userID string, page, perPage int) ([]*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(teamID) {
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	if userID != "" && !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	if userID != "" && s.client == nil {
		s.log.Warn("GetSpacesForTeam: pluginapi client not wired for authenticated request; denying access", "team_id", teamID, "user_id", userID)
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.client_not_wired.app_error", nil, "", http.StatusInternalServerError)
	}
	if userID != "" {
		if _, memberErr := s.client.Team.GetMember(teamID, userID); memberErr != nil {
			if errors.Is(memberErr, pluginapi.ErrNotFound) {
				return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.not_team_member.app_error", nil, "", http.StatusForbidden).Wrap(memberErr)
			}
			return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
		channels, chanErr := s.client.Channel.ListForTeamForUser(teamID, userID, false)
		if chanErr != nil {
			return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.channel_list_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(chanErr)
		}
		channelIDs := make([]string, len(channels))
		for i, ch := range channels {
			channelIDs[i] = ch.Id
		}
		spaces, err := s.store.GetSpacesForTeamVisibleTo(teamID, channelIDs, offset, limit)
		if err != nil {
			return nil, storeAppError("GetSpacesForTeam", err)
		}
		return spaces, nil
	}
	spaces, err := s.store.GetSpacesForTeam(teamID, offset, limit)
	if err != nil {
		return nil, storeAppError("GetSpacesForTeam", err)
	}
	return spaces, nil
}

// UpdateSpace applies the non-nil fields of patch onto the existing space and saves it. A non-nil
// field (including an empty string) overwrites the current value, so a field can be cleared.
// Optimistic-locked on expectedUpdateAt: the caller passes the UpdateAt it last read, and a stale
// baseline yields a conflict unless force overrides it with last-write-wins.
func (s *Service) UpdateSpace(spaceID string, patch *model.SpacePatch, expectedUpdateAt int64, force bool) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := patch.IsValid(); appErr != nil {
		return nil, appErr
	}
	existing, appErr := s.GetSpace(spaceID)
	if appErr != nil {
		return nil, appErr
	}
	existing.Patch(patch)
	// Carry the caller-supplied baseline so the optimistic-lock CAS compares against what the
	// client read, not the row we just fetched (which would always match and defeat the lock).
	existing.UpdateAt = expectedUpdateAt

	title, titleErr := validateTitle("UpdateSpace", existing.Title, model.SpaceTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	existing.Title = title

	if fieldErr := validateSpaceMutableFields("UpdateSpace", existing.Description, existing.Icon); fieldErr != nil {
		return nil, fieldErr
	}

	updated, err := s.store.UpdateSpace(existing, force)
	if err != nil {
		return nil, storeAppError("UpdateSpace", err)
	}
	if updated.ChannelId != "" && s.client != nil {
		if chanErr := s.syncSpaceChannelMetadata(updated); chanErr != nil {
			s.log.Warn("UpdateSpace: failed to sync backing channel metadata; display name/header may be stale", "channel_id", updated.ChannelId, "space_id", spaceID, "err", chanErr)
		}
	}
	return updated, nil
}

// syncSpaceChannelMetadata updates the backing channel's display name and header to match the
// space's current Title and Description. Called after UpdateSpace succeeds; errors are logged
// and suppressed by the caller since the space row is the source of truth.
func (s *Service) syncSpaceChannelMetadata(space *model.Space) error {
	channel, err := s.client.Channel.GetSpaceBackingChannel(space.ChannelId)
	if err != nil {
		return err
	}
	if channel == nil {
		return nil
	}
	channel.DisplayName, _ = mmmodel.LimitRunes(space.Title, mmmodel.ChannelDisplayNameMaxRunes)
	channel.Header = space.Description
	return s.client.Channel.Update(channel)
}

// DeleteSpace soft-deletes a space and its pages (reversible via RestoreSpace), then archives the
// backing channel best-effort; RestoreSpace un-archives it on restore. The channel archive runs
// with elevated plugin permissions, independently of the requesting user.
func (s *Service) DeleteSpace(spaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeleteSpace", "app.space.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Deleting space", "space_id", spaceID)
	// Resolve the backing channel id before the soft-delete so it can be archived afterward.
	space, getErr := s.GetSpace(spaceID)
	if getErr != nil {
		return getErr
	}
	if err := s.store.DeleteSpace(spaceID); err != nil {
		return storeAppError("DeleteSpace", err)
	}
	// Archive the backing channel best-effort. pluginapi.Channel.Delete soft-deletes the channel
	// (sets DeleteAt). Guarded with a client nil-check so store-only tests (which seed spaces
	// directly and never wire a client) don't panic.
	if space.ChannelId != "" && s.client != nil {
		if err := s.client.Channel.Delete(space.ChannelId); err != nil {
			s.log.Warn("DeleteSpace: failed to archive backing channel; channel may require manual cleanup", "channel_id", space.ChannelId, "space_id", spaceID, "err", err)
		}
	}
	return nil
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
			if space, appErr, retried := s.retryStuckChannelRestore(spaceID); retried {
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
	space, getErr := s.GetSpace(spaceID)
	if getErr != nil {
		// The restore committed successfully; retry once in case of a transient read error.
		space, getErr = s.GetSpace(spaceID)
		if getErr != nil {
			return nil, getErr
		}
	}
	if appErr := s.restoreSpaceChannel(space); appErr != nil {
		return nil, appErr
	}
	return space, nil
}

// retryStuckChannelRestore checks whether spaceID's backing channel is still archived despite the
// space row already being live — the signature of a prior RestoreSpace call that completed the DB
// half but failed on the channel un-archive (restoreSpaceChannel below). If so, it completes the
// channel restore now and reports retried=true. If the channel is already live, reports retried=false;
// the caller then handles the original not_deleted error normally.
func (s *Service) retryStuckChannelRestore(spaceID string) (space *model.Space, appErr *mmmodel.AppError, retried bool) {
	got, getErr := s.GetSpace(spaceID)
	if getErr != nil {
		return nil, getErr, true
	}
	if s.client == nil || got.ChannelId == "" {
		return nil, nil, false
	}
	archived, getChanErr := s.backingChannelArchived(got.ChannelId)
	if getChanErr != nil {
		return nil, mmmodel.NewAppError("RestoreSpace", "app.space.restore.channel_restore_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(getChanErr), true
	}
	if !archived {
		return nil, nil, false
	}
	if appErr := s.restoreSpaceChannel(got); appErr != nil {
		return nil, appErr, true
	}
	return got, nil, true
}

// backingChannelArchived reports whether channelID resolves to a channel that is currently
// archived. A channel that no longer exists reports false: there is nothing left to un-archive.
func (s *Service) backingChannelArchived(channelID string) (bool, error) {
	channel, err := s.client.Channel.GetSpaceBackingChannel(channelID)
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
	if s.client == nil || space.ChannelId == "" {
		return nil
	}
	if err := s.client.Channel.Restore(space.ChannelId); err != nil {
		// Distinguish "nothing to un-archive" from a genuine failure. Only a live (or absent)
		// channel is benign; if it is still archived the un-archive really did fail.
		archived, checkErr := s.backingChannelArchived(space.ChannelId)
		if checkErr == nil && !archived {
			s.log.Warn("RestoreSpace: backing channel was already live; treating un-archive as a no-op", "channel_id", space.ChannelId, "space_id", space.Id, "err", err)
			return nil
		}
		return mmmodel.NewAppError("RestoreSpace", "app.space.restore.channel_restore_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return nil
}

// GetSpacePages returns paginated live pages for a space.
func (s *Service) GetSpacePages(spaceID string, page, perPage int) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetSpacePages", "app.space.get_pages.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetSpace(spaceID); err != nil {
		return nil, err
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetSpacePages(spaceID, offset, limit)
	if storeErr != nil {
		return nil, storeAppError("GetSpacePages", storeErr)
	}
	return pages, nil
}
