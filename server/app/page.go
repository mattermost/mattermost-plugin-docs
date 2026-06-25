// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// MaxPageDepth is the maximum depth for page hierarchies (app-layer enforcement).
// The store CTE uses a separate safety bound (store.MaxPageHierarchyDepth = 50).
const MaxPageDepth = 10

// maxForceUpdateAttempts bounds how many times a forced update re-reads and retries the
// store CAS when a concurrent writer keeps winning, so force overwrites instead of 409ing.
const maxForceUpdateAttempts = 3

// modifierID returns the user who last modified the page, falling back to the
// original author when LastModifiedBy is unset.
func modifierID(p *model.Page) string {
	if p.LastModifiedBy != "" {
		return p.LastModifiedBy
	}
	return p.UserId
}

// CreatePage creates a new page in the space identified by spaceID. The backing
// channel is derived from the space (a space has exactly one), never passed in,
// so a page's ChannelId can never disagree with its space.
func (s *Service) CreatePage(spaceID, parentID, title, body, searchText, userID, pageID string) (*model.Page, *mmmodel.AppError) {
	if pageID != "" && !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	title, titleErr := validateTitle("CreatePage", "app.page.create", title, model.PageTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	if len(body) > model.PageBodyMaxBytes {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.body_too_long.app_error", map[string]any{"MaxBytes": model.PageBodyMaxBytes}, "", http.StatusBadRequest)
	}
	if len(searchText) > model.PageSearchTextMaxBytes {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.search_text_too_long.app_error", map[string]any{"MaxBytes": model.PageSearchTextMaxBytes}, "", http.StatusBadRequest)
	}
	// SearchText is the body's plain-text projection, so it makes no sense without a body
	// (matches the update path's rule).
	if searchText != "" && body == "" {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.search_text_without_content.app_error", nil, "", http.StatusBadRequest)
	}

	// Derive ChannelId from the space (one channel per space) and denormalize it onto
	// the page for the read-path advisory lock and channel-scoped listings.
	space, spaceErr := s.GetSpace(spaceID)
	if spaceErr != nil {
		return nil, spaceErr
	}
	channelID := space.ChannelId

	if parentID != "" {
		if !mmmodel.IsValidId(parentID) {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_parent_id.app_error", nil, "", http.StatusBadRequest)
		}
		parentPage, err := s.GetPage(parentID)
		if err != nil {
			// Only a missing parent is a 400; other GetPage failures carry their own status.
			if err.StatusCode == http.StatusNotFound {
				return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_parent.app_error", nil, "", http.StatusBadRequest).Wrap(err)
			}
			return nil, err
		}
		// Pin the parent to the same space: cross-space parenting would corrupt
		// SpaceId-scoped listings and hierarchy traversal.
		if parentPage.SpaceId != spaceID {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.parent_different_space.app_error", nil, "", http.StatusBadRequest)
		}
		ancestorDepth, ancErr := s.store.GetPageAncestorDepth(parentID)
		if ancErr != nil {
			return nil, storeAppError("CreatePage", "app.page.create.depth_check", ancErr)
		}
		// ancestorDepth excludes the parent itself; new page depth = ancestorDepth + 2.
		// Best-effort: read outside the create tx, so concurrent inserts could briefly
		// exceed MaxPageDepth; the store CTE's hard cap is the real backstop.
		newDepth := ancestorDepth + 2
		if newDepth > MaxPageDepth {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
		}
	}

	// Body is stored as-is (TipTap validation/normalization and SearchText deferred).
	page := &model.Page{
		Id:         pageID,
		SpaceId:    spaceID,
		ChannelId:  channelID,
		ParentId:   parentID,
		Type:       model.PageTypePage,
		Title:      title,
		Body:       body,
		SearchText: searchText,
		UserId:     userID,
	}

	created, storeErr := s.store.CreatePage(page)
	if storeErr != nil {
		if store.IsErrNotFound(storeErr) {
			// The space was soft-deleted between the check above and the insert.
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.space_not_found.app_error", nil, "", http.StatusNotFound).Wrap(storeErr)
		}
		if store.IsErrInvalidInput(storeErr) {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_input.app_error", nil, "", http.StatusBadRequest).Wrap(storeErr)
		}
		if store.IsErrConflict(storeErr) {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.conflict.app_error", nil, "", http.StatusConflict).Wrap(storeErr)
		}
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.store_error.app_error", nil, "", http.StatusInternalServerError).Wrap(storeErr)
	}

	// Notifications and mention parsing are not wired yet.

	return created, nil
}

// GetPage fetches a live page by ID.
func (s *Service) GetPage(pageID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPage", "app.page.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, err := s.store.GetPage(pageID, false)
	if err != nil {
		if store.IsErrNotFound(err) {
			return nil, mmmodel.NewAppError("GetPage", "app.page.get.not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		if store.IsErrInvalidInput(err) {
			return nil, mmmodel.NewAppError("GetPage", "app.page.get.invalid_input.app_error", nil, "", http.StatusBadRequest).Wrap(err)
		}
		return nil, mmmodel.NewAppError("GetPage", "app.page.get.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return page, nil
}

// UpdatePageWithOptimisticLocking applies a partial patch to a page with first-one-wins
// concurrency control. baseEditAt is the EditAt the client last saw. Returns 409 on conflict.
func (s *Service) UpdatePageWithOptimisticLocking(pageID string, patch *model.PagePatch, baseEditAt int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if validErr := validatePagePatch("UpdatePageWithOptimisticLocking", patch); validErr != nil {
		return nil, validErr
	}
	currentPage, err := s.GetPage(pageID)
	if err != nil {
		return nil, err
	}

	if !force && currentPage.EditAt != baseEditAt {
		return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.conflict.app_error",
			map[string]any{"ModifiedBy": modifierID(currentPage), "ModifiedAt": currentPage.EditAt},
			"conflict", http.StatusConflict)
	}

	// store.UpdatePage CAS on EditAt; a concurrent writer between the read and the CAS
	// returns ErrConflict. For force updates, re-read and retry rather than failing.
	for attempt := 0; ; attempt++ {
		updated := currentPage.Clone()
		updated.Patch(patch)
		updated.LastModifiedBy = userID

		updatedPage, storeErr := s.store.UpdatePage(updated)
		if storeErr == nil {
			return updatedPage, nil
		}
		if store.IsErrNotFound(storeErr) {
			return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.not_found.app_error", nil, "", http.StatusNotFound).Wrap(storeErr)
		}
		if store.IsErrInvalidInput(storeErr) {
			return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.invalid_content.app_error", nil, "", http.StatusBadRequest).Wrap(storeErr)
		}
		if store.IsErrConflict(storeErr) {
			// currentPage is stale after a lost CAS; re-fetch for accurate retry / conflict metadata.
			fresh, freshErr := s.GetPage(pageID)
			if freshErr != nil {
				// Re-fetch failed (deleted mid-retry or DB error); surface directly.
				return nil, freshErr
			}
			if force && attempt < maxForceUpdateAttempts-1 {
				currentPage = fresh
				continue
			}
			return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.conflict.app_error",
				map[string]any{"ModifiedBy": modifierID(fresh), "ModifiedAt": fresh.EditAt},
				"conflict", http.StatusConflict).Wrap(storeErr)
		}
		return nil, mmmodel.NewAppError("UpdatePageWithOptimisticLocking", "app.page.update.store_error.app_error", nil, "", http.StatusInternalServerError).Wrap(storeErr)
	}
}
