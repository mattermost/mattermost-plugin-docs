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

// archiveOrphanChannel archives a backing channel created earlier in CreateSpace when a later step
// fails. pluginapi Channel.Delete soft-deletes (archives) the channel — the plugin API exposes no
// hard delete — so the orphan remains as an archived channel; a warning is logged if the
// compensating archive itself fails so it can be reconciled. reason describes the step that failed
// and cause is its underlying error.
func (s *Service) archiveOrphanChannel(channelID, reason string, cause error) {
	if s.client == nil {
		return
	}
	if delErr := s.client.Channel.Delete(channelID); delErr != nil {
		s.logWarn("CreateSpace: compensating channel archive also failed; channel may be orphaned", "channel_id", channelID, "failure_reason", reason, "cause_err", cause.Error(), "delete_err", delErr.Error())
	}
}

// CreateSpace creates a ChannelTypeSpace ("S") backing channel via pluginapi, saves the
// space row pointing at it, and adds the creator as a member. Callers must not supply a
// ChannelId — it is derived from the created channel. If the row save fails, the backing
// channel is archived to avoid an orphan.
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
	// DeleteSpace can complete without a client — it removes the space row regardless and
	// only archives the backing channel as a best-effort side effect. CreateSpace cannot:
	// the backing channel must exist before the space row is saved, so a nil client is a
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

	displayName := truncateToRunes(space.Title, mmmodel.ChannelDisplayNameMaxRunes)

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

// GetSpacesForTeam returns paginated live spaces for a team. perPage <= 0 defaults to
// PerPageDefault; larger values are capped at PerPageMaximum. A non-empty userID is verified to be
// a member of the team before listing (skipped for system callers with userID == "" or when the
// client is not wired).
func (s *Service) GetSpacesForTeam(teamID, userID string, page, perPage int) ([]*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(teamID) {
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	if userID != "" && !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if userID != "" && s.client != nil {
		if _, memberErr := s.client.Team.GetMember(teamID, userID); memberErr != nil {
			if errors.Is(memberErr, pluginapi.ErrNotFound) {
				return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.not_team_member.app_error", nil, "", http.StatusForbidden).Wrap(memberErr)
			}
			return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
	}
	offset, limit := paginationOffsetLimit(page, perPage)
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
			s.logWarn("UpdateSpace: failed to sync backing channel metadata; display name/header may be stale", "channel_id", updated.ChannelId, "space_id", spaceID, "err", chanErr.Error())
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
	channel.DisplayName = truncateToRunes(space.Title, mmmodel.ChannelDisplayNameMaxRunes)
	channel.Header = space.Description
	return s.client.Channel.Update(channel)
}

// DeleteSpace soft-deletes a space and its pages (reversible via RestoreSpace), then archives the
// backing channel best-effort; RestoreSpace un-archives it on restore.
//
// Authorization (interim, same known gap as initRouter): this archives/unarchives a real
// Mattermost channel under the plugin's own elevated pluginapi identity, not the caller's session
// permissions, and — like every other space/page operation — has no per-caller permission gate
// yet. Closed once space membership and per-page restriction are layered onto these routes.
func (s *Service) DeleteSpace(spaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeleteSpace", "app.space.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Deleting space", "space_id", spaceID)
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
			s.logWarn("DeleteSpace: failed to archive backing channel; channel may require manual cleanup", "channel_id", space.ChannelId, "space_id", spaceID, "err", err.Error())
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
// Authorization (interim, same known gap as initRouter/DeleteSpace): this unarchives a real
// Mattermost channel under the plugin's own elevated pluginapi identity, not the caller's session
// permissions, with no per-caller permission gate yet.
func (s *Service) RestoreSpace(spaceID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("RestoreSpace", "app.space.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Restoring space", "space_id", spaceID)
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
// channel restore now and reports retried=true. If the channel is already live too, there is
// nothing to retry (retried=false), and the caller falls back to its normal not_deleted rejection
// instead of this treating a genuinely never-deleted space as a silent success.
func (s *Service) retryStuckChannelRestore(spaceID string) (space *model.Space, appErr *mmmodel.AppError, retried bool) {
	got, getErr := s.GetSpace(spaceID)
	if getErr != nil {
		return nil, getErr, true
	}
	if s.client == nil || got.ChannelId == "" {
		return nil, nil, false
	}
	channel, getChanErr := s.client.Channel.GetSpaceBackingChannel(got.ChannelId)
	if getChanErr != nil {
		return nil, mmmodel.NewAppError("RestoreSpace", "app.space.restore.channel_restore_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(getChanErr), true
	}
	if channel == nil || channel.DeleteAt == 0 {
		return nil, nil, false
	}
	if appErr := s.restoreSpaceChannel(got); appErr != nil {
		return nil, appErr, true
	}
	return got, nil, true
}

// restoreSpaceChannel un-archives space's backing channel. Guarded with a client nil-check so
// store-only tests (which seed spaces directly) don't panic.
func (s *Service) restoreSpaceChannel(space *model.Space) *mmmodel.AppError {
	if s.client == nil || space.ChannelId == "" {
		return nil
	}
	if err := s.client.Channel.Restore(space.ChannelId); err != nil {
		return mmmodel.NewAppError("RestoreSpace", "app.space.restore.channel_restore_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return nil
}

// GetSpacePages returns paginated live pages for a space. perPage <= 0 defaults to
// PerPageDefault; larger values are capped at PerPageMaximum.
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
