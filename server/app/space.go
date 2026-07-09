// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

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

// GetSpaceForChannel returns the active space for the given backing channel.
func (s *Service) GetSpaceForChannel(channelID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(channelID) {
		return nil, mmmodel.NewAppError("GetSpaceForChannel", "app.space.get_for_channel.invalid_channel_id.app_error", nil, "", http.StatusBadRequest)
	}
	space, err := s.store.GetSpaceForChannel(channelID)
	if err != nil {
		return nil, storeAppError("GetSpaceForChannel", err)
	}
	return space, nil
}

// GetSpacesForTeam returns paginated live spaces for a team. perPage <= 0 defaults to
// PerPageDefault; larger values are capped at PerPageMaximum.
func (s *Service) GetSpacesForTeam(teamID string, page, perPage int) ([]*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(teamID) {
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	spaces, err := s.store.GetSpacesForTeam(teamID, offset, limit)
	if err != nil {
		return nil, storeAppError("GetSpacesForTeam", err)
	}
	return spaces, nil
}

// UpdateSpace replaces a space's mutable fields (Title, Description, Icon, Props) —
// full replacement, not partial merge. Callers must pass a complete space (typically
// from GetSpace) with only the intended fields changed; zero values clear stored values.
// Optimistic-locked on space.UpdateAt; force overrides with last-write-wins.
//
// This is intentionally PUT-style, unlike UpdatePage's PATCH-style *PagePatch (nil field =
// unchanged): Space has few, always-together mutable fields, so full-replacement keeps the
// call simple; Page's larger field set (and independent Body/SearchText/Props updates) needs
// per-field nil-vs-set discrimination. If Space grows more independently-updatable fields,
// revisit with a SpacePatch mirroring PagePatch.
func (s *Service) UpdateSpace(space *model.Space, force bool) (*model.Space, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.nil_input.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(space.Id) {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	// Title required/length and description/icon size caps are enforced by Space.IsValid at
	// the store boundary; this only normalizes the title.
	space.Title = normalizeTitle(space.Title)

	s.logDebug("Updating space", "space_id", space.Id)

	updated, err := s.store.UpdateSpace(space, force)
	if err != nil {
		return nil, storeAppError("UpdateSpace", err)
	}

	return updated, nil
}

// DeleteSpace soft-deletes a space by ID. Backing-channel cleanup is not wired yet. Unlike
// Page, Space has no LastModifiedBy field, so neither this nor RestoreSpace can record an
// actor even if one were passed in — add the field (and a migration) before actor tracking
// is needed here.
func (s *Service) DeleteSpace(spaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeleteSpace", "app.space.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Deleting space", "space_id", spaceID)
	if err := s.store.DeleteSpace(spaceID); err != nil {
		return storeAppError("DeleteSpace", err)
	}
	return nil
}

// RestoreSpace un-deletes a soft-deleted space by ID. Fails with a conflict error if another
// live space already owns the backing channel. Backing-channel un-archive is not wired yet
// (mirrors DeleteSpace).
func (s *Service) RestoreSpace(spaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("RestoreSpace", "app.space.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Restoring space", "space_id", spaceID)
	if err := s.store.RestoreSpace(spaceID); err != nil {
		if appErr := restoreReasonAppError("RestoreSpace", err, map[string]string{
			store.ReasonNotDeleted: "app.space.restore.not_deleted.app_error",
		}); appErr != nil {
			return appErr
		}
		return storeAppError("RestoreSpace", err)
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
