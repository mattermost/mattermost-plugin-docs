// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetSpace returns the space with the given ID.
func (s *Service) GetSpace(spaceID string) (*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetSpace", "app.space.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	space, err := s.store.GetSpace(spaceID)
	if err != nil {
		return nil, storeAppError("GetSpace", "app.space.get", err)
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
		return nil, storeAppError("GetSpaceForChannel", "app.space.get_for_channel", err)
	}
	return space, nil
}

// GetSpacesForTeam returns paginated spaces for a team. perPage <= 0 returns all spaces.
func (s *Service) GetSpacesForTeam(teamID string, page, perPage int) ([]*model.Space, *mmmodel.AppError) {
	if !mmmodel.IsValidId(teamID) {
		return nil, mmmodel.NewAppError("GetSpacesForTeam", "app.space.get_for_team.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	spaces, err := s.store.GetSpacesForTeam(teamID, false, offset, limit)
	if err != nil {
		return nil, storeAppError("GetSpacesForTeam", "app.space.get_for_team", err)
	}
	return spaces, nil
}

// UpdateSpace replaces a space's mutable fields (Title, Description, Icon, Props) —
// full replacement, not partial merge. Callers must pass a complete space (typically
// from GetSpace) with only the intended fields changed; zero values clear stored values.
func (s *Service) UpdateSpace(space *model.Space) (*model.Space, *mmmodel.AppError) {
	if space == nil {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.nil_input.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(space.Id) {
		return nil, mmmodel.NewAppError("UpdateSpace", "app.space.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	title, titleErr := validateTitle("UpdateSpace", "app.space.update", space.Title, model.SpaceTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	space.Title = title

	updated, err := s.store.UpdateSpace(space)
	if err != nil {
		return nil, storeAppError("UpdateSpace", "app.space.update", err)
	}

	return updated, nil
}

// DeleteSpace soft-deletes a space and cascades to its pages. Backing-channel cleanup is not wired yet.
func (s *Service) DeleteSpace(spaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeleteSpace", "app.space.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if err := s.store.DeleteSpace(spaceID); err != nil {
		return storeAppError("DeleteSpace", "app.space.delete", err)
	}
	return nil
}

// GetSpacePages returns paginated pages for a space. perPage <= 0 returns all pages.
func (s *Service) GetSpacePages(spaceID string, page, perPage int) ([]*model.Page, *mmmodel.AppError) {
	if _, err := s.GetSpace(spaceID); err != nil {
		return nil, err
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetSpacePages(spaceID, offset, limit)
	if storeErr != nil {
		return nil, storeAppError("GetSpacePages", "app.space.get_pages", storeErr)
	}
	return pages, nil
}
