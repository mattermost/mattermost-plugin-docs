// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// MaxPageDepth is the maximum depth for page hierarchies. The app layer owns the limit's value
// and passes it to Store.CreatePage, which enforces it atomically against the locked parent.
// store.MaxPageHierarchyDepth (= 50) is a separate, larger bound on how deep the read-side
// ancestor/descendant CTEs recurse.
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
		// This is a fast-fail read before CreatePage's insert transaction starts, so it is not
		// itself atomic with the insert (a concurrent move could change ancestorDepth after this
		// read). CreatePage re-derives and enforces the same cap against the locked parent inside
		// its transaction, which is the authoritative check.
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

	created, storeErr := s.store.CreatePage(page, MaxPageDepth)
	if storeErr != nil {
		if store.IsErrNotFound(storeErr) {
			// The space was soft-deleted between the check above and the insert.
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.space_not_found.app_error", nil, "", http.StatusNotFound).Wrap(storeErr)
		}
		if store.IsErrInvalidInput(storeErr) {
			var invErr *store.ErrInvalidInput
			if errors.As(storeErr, &invErr) && invErr.Reason == store.ReasonMaxDepthExceeded {
				return nil, mmmodel.NewAppError("CreatePage", "app.page.create.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest).Wrap(storeErr)
			}
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

// UpdatePage applies a partial patch to a page with first-one-wins concurrency control. baseEditAt
// is the EditAt the client last saw. spaceID scopes the mutation to the page's expected space: a
// page relocated out of it by a concurrent move-to-space is not found here, closing the race window
// between a caller's page-in-space check and this write.
func (s *Service) UpdatePage(pageID, spaceID string, patch *model.PagePatch, baseEditAt int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if validErr := normalizeAndValidatePagePatch(patch); validErr != nil {
		return nil, validErr
	}

	s.logDebug("Updating page", "page_id", pageID, "user_id", userID)

	updatedPage, storeErr := s.store.UpdatePage(pageID, spaceID, patch, baseEditAt, force, userID)
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

// DeletePage soft-deletes a page by ID. It takes no actor param, so LastModifiedBy is not updated to
// record who deleted the page. spaceID scopes the delete to the page's expected space: a page
// relocated out of it by a concurrent move-to-space is not found here, closing the race window
// between a caller's page-in-space check and this write.
func (s *Service) DeletePage(pageID, spaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Deleting page", "page_id", pageID)
	if delErr := s.store.DeletePage(pageID, spaceID); delErr != nil {
		return storeAppError("DeletePage", delErr)
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page by ID and returns it, matching the other mutation
// endpoints (Move/MoveToSpace/Duplicate) rather than requiring a follow-up GET; like DeletePage, it
// records no actor. This is delete's
// complement, not a version revert: version snapshots were never live pages that got deleted, so
// they are rejected. Not-found, not-restorable (snapshot), and already-live are all decided
// atomically by the store under its row lock (see store.RestorePage), so there is no separate
// pre-fetch here. spaceID scopes the restore to the page's expected space: a page relocated out of
// it by a concurrent move-to-space is not found here, closing the race window between a caller's
// page-in-space check and this write.
func (s *Service) RestorePage(pageID, spaceID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.logDebug("Restoring page", "page_id", pageID)
	if restoreErr := s.store.RestorePage(pageID, spaceID, MaxPageDepth); restoreErr != nil {
		if appErr := restoreReasonAppError("RestorePage", restoreErr, map[string]string{
			store.ReasonNotRestorable: "app.page.restore.not_restorable.app_error",
			store.ReasonNotDeleted:    "app.page.restore.not_deleted.app_error",
		}); appErr != nil {
			return nil, appErr
		}
		return nil, storeAppError("RestorePage", restoreErr)
	}
	restored, getErr := s.store.GetPage(pageID, false)
	if getErr != nil {
		return nil, storeAppError("RestorePage", getErr)
	}
	return restored, nil
}

// DuplicatePage copies the source page, titled "Copy of <title>", into targetSpaceID (empty means
// the source's own space, which must be on the same team as a non-empty targetSpaceID — mirroring
// MovePageToSpace's cross-team rejection). sourceSpaceID scopes the read to the caller's expected
// space (see GetPageForDuplicate for the read-consistency guarantee).
//
// targetParentID selects the destination parent: nil defaults to the source's own parent when the
// destination is the source's space, or the destination space's root otherwise; a non-nil pointer
// is used as-is ("" means the destination root). A non-root destination parent is validated the
// same way MovePage/MovePageToSpace validate theirs (must exist, must be live, must belong to the
// destination space) before anything is computed from it.
//
// The destination depth is validated regardless of includeChildren, and re-validated by the store,
// which catches a concurrent insert that raced past this check. When includeChildren is set, the
// source's whole live subtree is copied underneath the new root in one transaction via
// CreatePageSubtree, keeping each descendant's original title (only the root gets the "Copy of "
// prefix) and relative structure; a failure partway through cannot leave a partial subtree behind.
func (s *Service) DuplicatePage(pageID, sourceSpaceID, userID string, includeChildren bool, targetSpaceID string, targetParentID *string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(sourceSpaceID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	source, descendants, getErr := s.store.GetPageForDuplicate(pageID, sourceSpaceID, includeChildren)
	if getErr != nil {
		if store.IsErrNotFound(getErr) {
			return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.not_found.app_error", nil, "", http.StatusNotFound).Wrap(getErr)
		}
		return nil, storeAppError("DuplicatePage", getErr)
	}

	destSpaceID := targetSpaceID
	if destSpaceID == "" {
		destSpaceID = source.SpaceId
	}

	if destSpaceID != source.SpaceId {
		sameTeam, crossErr := s.sameTeamSpaces(source.SpaceId, destSpaceID)
		if crossErr != nil {
			// source.SpaceId is already known-valid (source was just read from it above), so a
			// not-found here can only be the destination space — remap to the specific id the
			// other destination-missing paths below already use, instead of leaking the generic
			// app.store.not_found.app_error id for the same failure class.
			if crossErr.Id == "app.store.not_found.app_error" {
				return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.dest_not_found.app_error", nil, "", http.StatusNotFound).Wrap(crossErr)
			}
			return nil, crossErr
		}
		if !sameTeam {
			return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.cross_team.app_error", nil, "", http.StatusBadRequest)
		}
	}

	var destParentID string
	switch {
	case targetParentID != nil:
		destParentID = *targetParentID
	case destSpaceID == source.SpaceId:
		destParentID = source.ParentId
	}

	// Validate the destination depth before creating anything, so a copy doomed to breach the cap
	// fails atomically instead of leaving a partial tree behind. This runs unconditionally, not only
	// when includeChildren is set: a single-page copy is the same "insert one page under a parent"
	// operation CreatePage guards, held to the same cap.
	// MaxDepthOfPreOrderedPages is 0 when descendants is nil (includeChildren false), so checkDepthCap
	// below validates only the placement depth in that case.
	destinationDepth := 1
	if destParentID != "" {
		// Mirrors MovePage/MovePageToSpace: reject a destination parent that doesn't exist or lives
		// outside destSpaceID before computing anything from it, so a cross-space/missing parent gets
		// the same clear error those siblings give instead of a misleading depth-cap rejection.
		// Unlike Move/MoveToSpace, pageID here is the source being copied, not the copy's own
		// (as-yet ungenerated) id, so validateParentExists is used instead of validateDestinationParent:
		// a destParentID equal to the source is a legitimate "nest the copy under the original", not a
		// cycle.
		if destErr := s.validateParentExists("DuplicatePage", destParentID, destSpaceID); destErr != nil {
			return nil, destErr
		}
		ancestorDepth, ancErr := s.store.GetPageAncestorDepth(destParentID)
		if ancErr != nil {
			return nil, storeAppError("DuplicatePage", ancErr)
		}
		// ancestorDepth excludes destParentID itself, so the destination parent is at ancestorDepth + 1
		// and the copy one level deeper, at ancestorDepth + 2. Root pages have depth 1.
		destinationDepth = ancestorDepth + 2
	}
	if capErr := checkDepthCap("DuplicatePage", destinationDepth, model.MaxDepthOfPreOrderedPages(descendants, pageID)); capErr != nil {
		return nil, capErr
	}

	// Pre-generate every new id so descendants' ParentId can be resolved to the copy's new tree
	// shape before the single bulk insert. descendants is a pre-order walk (same CTE as
	// GetPageDescendants), so a node's parent is always visited before the node itself; idMap
	// therefore always has the new parent id by the time it's needed.
	rootID := mmmodel.NewId()
	idMap := map[string]string{pageID: rootID}
	pages := make([]*model.Page, 0, 1+len(descendants))
	pages = append(pages, &model.Page{
		Id:             rootID,
		SpaceId:        destSpaceID,
		ParentId:       destParentID,
		Type:           source.Type,
		Title:          copyTitle(source.Title),
		Body:           source.Body,
		SearchText:     source.SearchText,
		Props:          model.DeepCloneStringInterface(source.Props),
		UserId:         userID,
		LastModifiedBy: userID,
	})
	for _, d := range descendants {
		newID := mmmodel.NewId()
		pages = append(pages, &model.Page{
			Id:             newID,
			SpaceId:        destSpaceID,
			ParentId:       idMap[d.ParentId],
			Type:           d.Type,
			Title:          d.Title,
			Body:           d.Body,
			SearchText:     d.SearchText,
			Props:          model.DeepCloneStringInterface(d.Props),
			UserId:         userID,
			LastModifiedBy: userID,
		})
		idMap[d.Id] = newID
	}

	created, createErr := s.store.CreatePageSubtree(pages, MaxPageDepth)
	if createErr != nil {
		if store.IsErrNotFound(createErr) {
			return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.dest_not_found.app_error", nil, "", http.StatusNotFound).Wrap(createErr)
		}
		if store.IsErrInvalidInput(createErr) {
			return nil, invalidInputAppError("DuplicatePage", createErr)
		}
		return nil, storeAppError("DuplicatePage", createErr)
	}

	return created[0], nil
}

// copyTitle prefixes "Copy of " and truncates to the page-title cap so the duplicate's title
// always passes CreatePage validation.
func copyTitle(original string) string {
	return truncateToRunes("Copy of "+original, model.PageTitleMaxRunes)
}
