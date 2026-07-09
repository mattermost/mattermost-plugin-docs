// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetPageChildren fetches a page of direct live children of a page. perPage <= 0 defaults to
// PerPageDefault; larger values are capped at PerPageMaximum.
func (s *Service) GetPageChildren(pageID string, page, perPage int) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageChildren", "app.page.get_children.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetPageChildren(pageID, offset, limit)
	if storeErr != nil {
		return nil, storeAppError("GetPageChildren", storeErr)
	}
	return pages, nil
}

// GetPageAncestors fetches all live ancestors of a page up to the root; this can fail with an
// error instead of returning a partial chain.
func (s *Service) GetPageAncestors(pageID string) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageAncestors", "app.page.get_ancestors.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	pages, err := s.store.GetPageAncestors(pageID)
	if err != nil {
		return nil, storeAppError("GetPageAncestors", err)
	}
	return pages, nil
}

// GetPageDescendants fetches all live descendants of a page (entire subtree). Returns an error
// when the subtree exceeds the store's row-count or depth limit, rather than truncating.
func (s *Service) GetPageDescendants(pageID string) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageDescendants", "app.page.get_descendants.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	pages, err := s.store.GetPageDescendants(pageID)
	if err != nil {
		return nil, storeAppError("GetPageDescendants", err)
	}
	return pages, nil
}
