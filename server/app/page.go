// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"maps"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// MaxPageDepth is the page hierarchy depth limit (root is depth 1).
// store.MaxPageHierarchyDepth (50) is a separate, larger bound used by descendant/ancestor reads.
const MaxPageDepth = 10

// CreatePage creates a new page in spaceID. ChannelId is derived from the space, not supplied by the caller.
// The page ID is always server-generated; callers must not supply one.
func (s *Service) CreatePage(spaceID, parentID, title, body, searchText, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if parentID != "" && !mmmodel.IsValidId(parentID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_parent_id.app_error", nil, "", http.StatusBadRequest)
	}
	normalizedTitle, titleErr := validateTitle("CreatePage", title, model.PageTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	title = normalizedTitle
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
		// Optimistic fast-fail; a concurrent move could invalidate this before the insert.
		if newDepth > MaxPageDepth {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
		}
	}

	// Body is stored as-is (TipTap validation/normalization and SearchText deferred).
	page := &model.Page{
		SpaceId:        spaceID,
		ParentId:       parentID,
		Type:           model.PageTypePage,
		Title:          title,
		Body:           body,
		SearchText:     searchText,
		UserId:         userID,
		LastModifiedBy: userID,
	}

	s.log.Debug("Creating page", "space_id", spaceID, "parent_id", parentID, "user_id", userID)

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
		if store.IsErrLimitExceeded(storeErr) {
			return nil, storeAppError("CreatePage", storeErr)
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

// UpdatePage patches a page, optimistic-locked on baseEditAt. spaceID scopes the write: a page
// moved to another space since the caller's last check returns not-found instead of updating the wrong copy.
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
	if validErr := normalizeAndValidatePagePatch("UpdatePage", patch); validErr != nil {
		return nil, validErr
	}

	s.log.Debug("Updating page", "page_id", pageID, "user_id", userID)

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
		// Re-fetch for accurate EditAt/LastModifiedBy in the conflict response. If the re-read
		// fails, return the conflict with no metadata rather than leaking a 404.
		fresh, freshErr := s.GetPageInSpace("UpdatePage", pageID, spaceID, false)
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

// GetPageWithDeleted returns a page by ID even when soft-deleted (DeleteAt != 0). Version snapshots are excluded.
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

// DeletePage soft-deletes a page. spaceID prevents the delete from targeting a page that has
// since moved to a different space.
func (s *Service) DeletePage(pageID, spaceID, userID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return mmmodel.NewAppError("DeletePage", "app.page.delete.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Deleting page", "page_id", pageID, "user_id", userID)
	if delErr := s.store.DeletePage(pageID, spaceID, userID); delErr != nil {
		return storeAppError("DeletePage", delErr)
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page and returns it. Rejects snapshots (this is not a
// version revert). Enforces not-found/not-restorable/already-live atomically, so there is no
// pre-fetch here. spaceID prevents the restore from targeting a page that has since moved to
// a different space.
func (s *Service) RestorePage(pageID, spaceID, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("RestorePage", "app.page.restore.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Restoring page", "page_id", pageID, "user_id", userID)
	if restoreErr := s.store.RestorePage(pageID, spaceID, userID, MaxPageDepth); restoreErr != nil {
		if appErr := restoreReasonAppError(restoreErr, map[string]*mmmodel.AppError{
			store.ReasonNotRestorable: mmmodel.NewAppError("RestorePage", "app.page.restore.not_restorable.app_error", nil, "", http.StatusBadRequest),
			store.ReasonNotDeleted:    mmmodel.NewAppError("RestorePage", "app.page.restore.not_deleted.app_error", nil, "", http.StatusConflict),
		}); appErr != nil {
			return nil, appErr
		}
		return nil, storeAppError("RestorePage", restoreErr)
	}
	restored, getErr := s.GetPageInSpace("RestorePage", pageID, spaceID, false)
	if getErr != nil {
		// The restore committed successfully; retry once in case of a transient read error, as
		// a permanent 500 here would cause retries to hit "not deleted" (400) instead.
		restored, getErr = s.GetPageInSpace("RestorePage", pageID, spaceID, false)
		if getErr != nil {
			return nil, getErr
		}
	}
	return restored, nil
}

// DuplicatePage copies a page into targetSpaceID (empty = source's space; non-empty must be same
// team). The root copy is titled "Copy of <title>"; descendants keep their original titles.
// sourceSpaceID scopes the source read. targetParentID nil defaults to the source's parent (same
// space) or the target root (cross-space); a non-nil "" always means the target root.
// Rejects depth cap breaches; a concurrent race past this check is still caught before committing.
// includeChildren copies the whole live subtree atomically, so a partial failure cannot leave a partial tree.
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
	if targetSpaceID != "" && !mmmodel.IsValidId(targetSpaceID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_target_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if targetParentID != nil && *targetParentID != "" && !mmmodel.IsValidId(*targetParentID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_parent_id.app_error", nil, "", http.StatusBadRequest)
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
			if crossErr.StatusCode == http.StatusNotFound {
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

	// Validate depth before creating anything. Runs unconditionally: a single-page copy is the same
	// operation as CreatePage and held to the same cap. MaxDepthOfPreOrderedPages returns 0 when
	// descendants is nil (includeChildren false), so only the placement depth is checked in that case.
	destinationDepth := 1
	if destParentID != "" {
		// Reject a destination parent that doesn't exist or lives outside destSpaceID before computing
		// anything from it, so a cross-space/missing parent gets a clear error instead of a misleading
		// depth-cap rejection.
		// destParentID equal to the source is a legitimate case (nesting the copy under the original), not a cycle.
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
	subtreeMax := model.MaxDepthOfPreOrderedPages(descendants, pageID)
	if destinationDepth > MaxPageDepth {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
	}
	if destinationDepth+subtreeMax > MaxPageDepth {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
	}

	// Pre-generate IDs before the bulk insert so each descendant's new ParentId can be resolved.
	// descendants is pre-ordered (parent always before children), so idMap always has the new
	// parent ID ready when it is needed.
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
		Props:          maps.Clone(source.Props),
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
			Props:          maps.Clone(d.Props),
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
		if store.IsErrLimitExceeded(createErr) {
			var limErr *store.ErrLimitExceeded
			errors.As(createErr, &limErr)
			switch limErr.Reason {
			case store.ReasonSubtreeMaxDepthExceeded:
				return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest).Wrap(createErr)
			case store.ReasonMaxDepthExceeded:
				return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest).Wrap(createErr)
			}
			return nil, storeAppError("DuplicatePage", createErr)
		}
		return nil, storeAppError("DuplicatePage", createErr)
	}

	return created[0], nil
}

// copyTitle prefixes "Copy of " and truncates to the page-title cap so the duplicate's title
// always passes CreatePage validation.
func copyTitle(original string) string {
	title, _ := mmmodel.LimitRunes("Copy of "+original, model.PageTitleMaxRunes)
	return title
}
