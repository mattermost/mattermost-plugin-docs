// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetPageChildren fetches direct live children of a page.
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
		return nil, storeAppError("GetPageChildren", "app.page.get_children", storeErr)
	}
	return pages, nil
}

// GetPageAncestors fetches all ancestors of a page up to the root.
func (s *Service) GetPageAncestors(pageID string) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageAncestors", "app.page.get_ancestors.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	pages, err := s.store.GetPageAncestors(pageID)
	if err != nil {
		return nil, storeAppError("GetPageAncestors", "app.page.get_ancestors", err)
	}
	return pages, nil
}

// GetPageDescendants fetches all descendants of a page (entire subtree).
func (s *Service) GetPageDescendants(pageID string) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageDescendants", "app.page.get_descendants.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	pages, err := s.store.GetPageDescendants(pageID)
	if err != nil {
		return nil, storeAppError("GetPageDescendants", "app.page.get_descendants", err)
	}
	return pages, nil
}
