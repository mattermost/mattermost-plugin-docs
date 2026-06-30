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

// CreatePage creates a new page in the space identified by spaceID. ChannelId is
// derived from the space (which has exactly one backing channel), not supplied by the caller.
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
	title, titleErr := validateTitle("CreatePage", title, model.PageTitleMaxRunes)
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
			return nil, storeAppError("CreatePage", ancErr)
		}
		// Derive the new child's absolute depth from the tree root (a root page is depth 1; this is
		// distinct from the subtree-relative depth the descendants CTE reports, where the queried
		// node is 0). ancestorDepth counts the parent's ancestors but not the parent itself, so the
		// parent is at ancestorDepth + 1, and its new child is one deeper: +1 (parent) +1 (child) = ancestorDepth + 2.
		newDepth := ancestorDepth + 2
		// This runs outside the create transaction, so a concurrent insert can briefly push depth
		// past MaxPageDepth; the store CTE's recursion cap is the hard backstop against runaway nesting.
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
			return nil, invalidInputAppError("CreatePage", storeErr)
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
		return nil, storeAppError("GetPage", err)
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

	updatedPage, storeErr := s.store.UpdatePage(pageID, patch, baseEditAt, force, userID)
	if storeErr == nil {
		return updatedPage, nil
	}
	if store.IsErrNotFound(storeErr) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.not_found.app_error", nil, "", http.StatusNotFound).Wrap(storeErr)
	}
	if store.IsErrInvalidInput(storeErr) {
		return nil, invalidInputAppError("UpdatePage", storeErr)
	}
	if store.IsErrConflict(storeErr) {
		// The CAS conflict left us without an updated page; re-fetch it for accurate conflict
		// metadata. If the re-read races with a delete, still report the original conflict rather than
		// the follow-up error, so the client never sees a 404 for a stale update.
		fresh, freshErr := s.GetPage(pageID)
		if freshErr != nil {
			return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.conflict.app_error",
				nil, "conflict", http.StatusConflict).Wrap(storeErr)
		}
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.conflict.app_error",
			map[string]any{"ModifiedBy": modifierID(fresh), "ModifiedAt": fresh.EditAt},
			"conflict", http.StatusConflict).Wrap(storeErr)
	}
	return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.store_error.app_error", nil, "", http.StatusInternalServerError).Wrap(storeErr)
}

// GetPageWithDeleted returns a page by ID even when soft-deleted (DeleteAt != 0),
// unlike GetPage which returns only live pages. Version snapshots (OriginalId set)
// are excluded and return not-found regardless of deletion state.
func (s *Service) GetPageWithDeleted(pageID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, err := s.store.GetPage(pageID, true)
	if err != nil {
		return nil, storeAppError("GetPageWithDeleted", err)
	}
	// includeDeleted would also surface version snapshots (always soft-deleted, OriginalId set);
	// exclude them so an ID resolves to its current page, never a historical version.
	if page.OriginalId != "" {
		return nil, mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return page, nil
}

// DeletePage soft-deletes a page by ID.
func (s *Service) DeletePage(pageID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if delErr := s.store.DeletePage(pageID); delErr != nil {
		return storeAppError("DeletePage", delErr)
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page by ID. Version snapshots cannot be un-deleted.
func (s *Service) RestorePage(pageID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, err := s.store.GetPage(pageID, true)
	if err != nil {
		return storeAppError("RestorePage", err)
	}
	// Version snapshots (OriginalId != "") are soft-deleted by design and cannot be un-deleted;
	// reject them explicitly so a snapshot returns a clear error instead of a generic not-found.
	if page.OriginalId != "" {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.not_restorable.app_error", nil, "", http.StatusBadRequest)
	}
	if page.DeleteAt == 0 {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.not_deleted.app_error", nil, "", http.StatusBadRequest)
	}
	if restoreErr := s.store.RestorePage(pageID); restoreErr != nil {
		return storeAppError("RestorePage", restoreErr)
	}
	return nil
}
