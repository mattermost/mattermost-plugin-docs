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
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	pages, err := s.store.GetPageDescendants(pageID)
	if err != nil {
		return nil, storeAppError("GetPageDescendants", "app.page.get_descendants", err)
	}
	return pages, nil
}

// GetSpaceIdForPage returns a page's space ID. An empty SpaceId means corrupt data, not a
// recoverable case: resolving via the channel could return the wrong (current) space when
// the page predates a soft-deleted-and-recreated space on that channel.
func (s *Service) GetSpaceIdForPage(pageID string) (string, *mmmodel.AppError) {
	page, err := s.GetPage(pageID)
	if err != nil {
		return "", err
	}
	if page.SpaceId == "" {
		return "", mmmodel.NewAppError("GetSpaceIdForPage", "app.page.get_space_id.missing_space_id.app_error", nil, "page_id="+pageID, http.StatusInternalServerError)
	}
	return page.SpaceId, nil
}
