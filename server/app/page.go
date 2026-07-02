// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// MaxPageDepth is the maximum depth for page hierarchies (app-layer enforcement on create).
// The store has no write-time depth check; store.MaxPageHierarchyDepth (= 50) only bounds
// how deep the read-side ancestor/descendant CTEs recurse.
const MaxPageDepth = 10

// CreatePage creates a new page in spaceID. ChannelId is derived from the space
// (which has exactly one backing channel), not supplied by the caller.
//
// All seven parameters are plain strings with no compiler-enforced ordering, so a
// caller binding an HTTP request body to this call is one transposed argument away from a
// silent bug (e.g. swapping parentID and userID). Revisit as an input struct (mirroring
// PagePatch) before a REST handler binds a request body to this signature.
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
	// Title required/length and body/searchText size caps are enforced by Page.IsValid at the
	// store boundary; this only normalizes the title.
	title = normalizeTitle(title)
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
		// ancestorDepth excludes the parent itself, so the parent is at ancestorDepth + 1
		// and the new child one level deeper, at ancestorDepth + 2. Root pages have depth 1.
		newDepth := ancestorDepth + 2
		// This depth check reads ancestorDepth before CreatePage's insert transaction starts, so it
		// is not atomic with the insert: a concurrent create under the same parent can land between
		// this check and the insert, and both inserts can pass depth validation using the same
		// pre-insert ancestorDepth, briefly pushing the tree past MaxPageDepth. The store does not
		// enforce a depth cap on insert; MaxPageHierarchyDepth only bounds the read-side CTEs
		// (GetPageAncestors/GetPageDescendants), so this race is not backstopped.
		if newDepth > MaxPageDepth {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
		}
	}

	// Body is stored as-is (TipTap validation/normalization and SearchText deferred).
	page := &model.Page{
		Id:             pageID,
		SpaceId:        spaceID,
		ParentId:       parentID,
		Type:           model.PageTypePage,
		Title:          title,
		Body:           body,
		SearchText:     searchText,
		UserId:         userID,
		LastModifiedBy: userID,
	}

	s.logDebug("Creating page", "space_id", spaceID, "parent_id", parentID, "user_id", userID)

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

// UpdatePage applies a partial patch to a page with first-one-wins concurrency control.
// baseEditAt is the EditAt of the page version the caller's patch is based on; if the page
// has been edited since baseEditAt, the update is rejected as a conflict, unless force is
// set (see store.UpdatePage).
func (s *Service) UpdatePage(pageID string, patch *model.PagePatch, baseEditAt int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if validErr := normalizeAndValidatePagePatch(patch); validErr != nil {
		return nil, validErr
	}

	s.logDebug("Updating page", "page_id", pageID, "user_id", userID)

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
			map[string]any{"ModifiedBy": fresh.LastModifiedBy, "ModifiedAt": fresh.EditAt},
			"conflict", http.StatusConflict).Wrap(storeErr)
	}
	return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.store_error.app_error", nil, "", http.StatusInternalServerError).Wrap(storeErr)
}

// GetPageWithDeleted returns a page by ID even when soft-deleted (DeleteAt != 0),
// unlike GetPage which returns only live pages. Version snapshots are excluded.
func (s *Service) GetPageWithDeleted(pageID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, err := s.store.GetPage(pageID, true)
	if err != nil {
		return nil, storeAppError("GetPageWithDeleted", err)
	}
	// includeDeleted would also surface snapshots; exclude them so an ID resolves to its
	// current page, never a historical version.
	if page.IsSnapshot() {
		return nil, mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return page, nil
}

// DeletePage soft-deletes a page by ID. Unlike CreatePage/UpdatePage, this takes no actor
// param, so who deleted the page is not recorded (Page.LastModifiedBy is left at its
// last-editor value). Add a userID param and set LastModifiedBy before this is wired to an
// audited REST endpoint.
func (s *Service) DeletePage(pageID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Deleting page", "page_id", pageID)
	if delErr := s.store.DeletePage(pageID); delErr != nil {
		return storeAppError("DeletePage", delErr)
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page by ID. Version snapshots are not restorable.
// Not-found, not-restorable (snapshot), and already-live are all decided atomically by the
// store under its row lock (see store.RestorePage), so there is no separate pre-fetch here.
// Like DeletePage, this takes no actor param — see DeletePage's note on LastModifiedBy.
func (s *Service) RestorePage(pageID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Restoring page", "page_id", pageID)
	if restoreErr := s.store.RestorePage(pageID); restoreErr != nil {
		if appErr := restoreReasonAppError("RestorePage", restoreErr, map[string]string{
			store.ReasonNotRestorable: "app.page.restore.not_restorable.app_error",
			store.ReasonNotDeleted:    "app.page.restore.not_deleted.app_error",
		}); appErr != nil {
			return appErr
		}
		return storeAppError("RestorePage", restoreErr)
	}
	return nil
}
