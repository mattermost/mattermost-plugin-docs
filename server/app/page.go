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
	if parentID != "" && !mmmodel.IsValidId(parentID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_parent_id.app_error", nil, "", http.StatusBadRequest)
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

	// Guard against a missing or soft-deleted space here for a clean 404 (a parent in a
	// gone space would otherwise mis-report as parent_different_space). The store re-checks
	// under lock and is the source of truth for the page's ChannelId.
	if _, spaceErr := s.GetSpace(spaceID); spaceErr != nil {
		return nil, spaceErr
	}

	if parentID != "" {
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
			return nil, invalidInputAppError("CreatePage", "app.page.create.invalid_input.app_error", storeErr)
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
		return nil, storeAppError("GetPage", "app.page.get", err)
	}
	return page, nil
}

// UpdatePage applies a partial patch to a page with first-one-wins
// concurrency control. baseEditAt is the EditAt the client last saw. Returns 409 on conflict.
func (s *Service) UpdatePage(pageID string, patch *model.PagePatch, baseEditAt int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if validErr := normalizeAndValidatePagePatch("UpdatePage", patch); validErr != nil {
		return nil, validErr
	}

	// The store merges the patch into the row under a FOR UPDATE lock: on the CAS path it
	// rejects a stale baseEditAt with ErrConflict; on the force path it still merges only the
	// patched fields, so a concurrent edit to untouched fields survives.
	updatedPage, storeErr := s.store.UpdatePage(pageID, patch, baseEditAt, force, userID)
	if storeErr == nil {
		return updatedPage, nil
	}
	if store.IsErrNotFound(storeErr) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.not_found.app_error", nil, "", http.StatusNotFound).Wrap(storeErr)
	}
	if store.IsErrInvalidInput(storeErr) {
		return nil, invalidInputAppError("UpdatePage", "app.page.update.invalid_content.app_error", storeErr)
	}
	if store.IsErrConflict(storeErr) {
		// currentPage is stale after the lost CAS; re-fetch for accurate conflict metadata.
		fresh, freshErr := s.GetPage(pageID)
		if freshErr != nil {
			return nil, freshErr
		}
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.conflict.app_error",
			map[string]any{"ModifiedBy": modifierID(fresh), "ModifiedAt": fresh.EditAt},
			"conflict", http.StatusConflict).Wrap(storeErr)
	}
	return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.store_error.app_error", nil, "", http.StatusInternalServerError).Wrap(storeErr)
}

// GetPageWithDeleted fetches a page including soft-deleted rows, for restore flows.
func (s *Service) GetPageWithDeleted(pageID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, err := s.store.GetPage(pageID, true)
	if err != nil {
		return nil, storeAppError("GetPageWithDeleted", "app.page.get", err)
	}
	// Version snapshots (OriginalId != "") are soft-deleted but not restorable; treat as not found.
	if page.OriginalId != "" {
		return nil, mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return page, nil
}

// DeletePage soft-deletes a page; the store promotes its live children to the page's parent
// (not undone on restore, matching Confluence).
func (s *Service) DeletePage(pageID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if delErr := s.store.DeletePage(pageID); delErr != nil {
		return storeAppError("DeletePage", "app.page.delete", delErr)
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page; promoted children stay put (matching
// Confluence), and the page returns under its original parent or the space root if it's gone.
func (s *Service) RestorePage(pageID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, err := s.store.GetPage(pageID, true)
	if err != nil {
		return storeAppError("RestorePage", "app.page.restore", err)
	}
	// Version snapshots (OriginalId != "") are soft-deleted by design and are not restorable;
	// reject them explicitly so the store's OriginalId="" filter doesn't surface as a 404.
	if page.OriginalId != "" {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.not_restorable.app_error", nil, "", http.StatusBadRequest)
	}
	if page.DeleteAt == 0 {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.not_deleted.app_error", nil, "", http.StatusBadRequest)
	}
	if restoreErr := s.store.RestorePage(pageID); restoreErr != nil {
		return storeAppError("RestorePage", "app.page.restore", restoreErr)
	}
	return nil
}
