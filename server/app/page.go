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

// MaxPageDepth and store.DraftCycleCheckMaxDepth must stay equal: a draft chain publishes into a
// page chain of the same depth. The two lines below fail to compile if the values ever diverge —
// whichever subtraction goes negative overflows when converted to uint, which Go rejects in a
// constant expression. Both directions are checked so drift is caught whichever constant grew.
const (
	_ = uint(MaxPageDepth - store.DraftCycleCheckMaxDepth)
	_ = uint(store.DraftCycleCheckMaxDepth - MaxPageDepth)
)

// CreatePage creates a new page in spaceID. ChannelId is derived from the space, not supplied by the caller.
// The page ID is always server-generated; callers must not supply one.
func (s *Service) CreatePage(spaceID, parentID, title, body, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CreatePage", "app.page.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	title, titleErr := validateTitle("CreatePage", title, model.PageTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}
	// Validate and normalize the TipTap body and derive SearchText from it. SearchText is the body's
	// server-derived projection, so it is never taken from the caller.
	normBody, normSearch, contentErr := normalizePageContent("CreatePage", body)
	if contentErr != nil {
		return nil, contentErr
	}

	// Space existence and liveness are validated by store.CreatePage itself (surfaced as
	// not-found below), so there is no space pre-check here.
	//
	// The parent pre-check keeps its more specific invalid_parent rejection (a malformed,
	// missing, or cross-space parent all read identically, so the error can't be used to
	// probe page ids in spaces the caller isn't a member of).
	if parentID != "" {
		if destErr := s.validateParentExists("CreatePage", parentID, spaceID); destErr != nil {
			return nil, destErr
		}
	}

	page := &model.Page{
		SpaceId:        spaceID,
		ParentId:       parentID,
		Type:           model.PageTypePage,
		Title:          title,
		Body:           normBody,
		SearchText:     normSearch,
		UserId:         userID,
		LastModifiedBy: userID,
	}

	s.log.Debug("Creating page", "space_id", spaceID, "parent_id", parentID, "user_id", userID)

	created, storeErr := s.store.CreatePage(page, MaxPageDepth)
	if storeErr != nil {
		if store.IsErrNotFound(storeErr) {
			// The space is missing or soft-deleted.
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.space_not_found.app_error", nil, "", http.StatusNotFound).Wrap(storeErr)
		}
		if store.IsErrConflict(storeErr) {
			return nil, mmmodel.NewAppError("CreatePage", "app.page.create.conflict.app_error", nil, "", http.StatusConflict).Wrap(storeErr)
		}
		return nil, storeAppError("CreatePage", storeErr)
	}

	// Notifications and mention parsing are not wired yet.

	// parent_id is included because clients have never seen this page: without it a tree view
	// cannot place the new node and would need a follow-up fetch.
	s.publishToChannels(wsEventPageCreated, map[string]any{"page_id": created.Id, "space_id": created.SpaceId, "parent_id": created.ParentId}, created.ChannelId)

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

// UpdatePage patches a page, optimistic-locked on baseEditAt; a nil baseEditAt without force is
// rejected. spaceID scopes the write: a page moved to another space since the caller's last
// check returns not-found instead of updating the wrong copy.
func (s *Service) UpdatePage(pageID, spaceID string, patch *model.PagePatch, baseEditAt *int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("UpdatePage", "app.page.update.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := requireBaseline("UpdatePage", "base_edit_at", baseEditAt, force); appErr != nil {
		return nil, appErr
	}
	// Validate/normalize the TipTap body (and recompute SearchText) before patch validation, so a
	// body-only patch is valid and a direct edit is sanitized on the same content path as publish.
	if contentErr := normalizePatchContent("UpdatePage", patch); contentErr != nil {
		return nil, contentErr
	}
	if validErr := normalizeAndValidatePagePatch("UpdatePage", patch); validErr != nil {
		return nil, validErr
	}

	s.log.Debug("Updating page", "page_id", pageID, "user_id", userID)

	updatedPage, storeErr := s.store.UpdatePage(pageID, spaceID, patch, mmmodel.SafeDereference(baseEditAt), force, userID)
	if storeErr == nil {
		s.publishToChannels(wsEventPageUpdated, map[string]any{"page_id": updatedPage.Id, "space_id": updatedPage.SpaceId}, updatedPage.ChannelId)
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
	channelID, delErr := s.store.DeletePage(pageID, spaceID, userID)
	if delErr != nil {
		return storeAppError("DeletePage", delErr)
	}
	s.publishToChannels(wsEventPageDeleted, map[string]any{"page_id": pageID, "space_id": spaceID}, channelID)
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
	restored, restoreErr := s.store.RestorePage(pageID, spaceID, userID, MaxPageDepth)
	if restoreErr != nil {
		if appErr := restoreReasonAppError(restoreErr, map[string]*mmmodel.AppError{
			store.ReasonNotRestorable: mmmodel.NewAppError("RestorePage", "app.page.restore.not_restorable.app_error", nil, "", http.StatusBadRequest),
			store.ReasonNotDeleted:    mmmodel.NewAppError("RestorePage", "app.page.restore.not_deleted.app_error", nil, "", http.StatusConflict),
		}); appErr != nil {
			return nil, appErr
		}
		return nil, storeAppError("RestorePage", restoreErr)
	}
	s.publishToChannels(wsEventPageRestored, map[string]any{"page_id": restored.Id, "space_id": restored.SpaceId}, restored.ChannelId)
	return restored, nil
}

// DuplicatePage copies a page into targetSpace (nil = source's space; non-nil must be same
// team). The root copy is titled "Copy of <title>"; descendants keep their original titles.
// sourceSpace scopes the source read; sourceSpace and targetSpace are the caller's
// already-fetched records (from its membership gates), so no re-read happens here.
// targetParentID nil defaults to the source's parent (same space) or the target root
// (cross-space); a non-nil "" always means the target root.
// includeChildren copies the whole live subtree atomically, so a partial failure cannot leave a partial tree.
func (s *Service) DuplicatePage(pageID string, sourceSpace *model.Space, userID string, includeChildren bool, targetSpace *model.Space, targetParentID *string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if sourceSpace == nil {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	source, descendants, getErr := s.store.GetPageForDuplicate(pageID, sourceSpace.Id, includeChildren)
	if getErr != nil {
		if store.IsErrNotFound(getErr) {
			return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.not_found.app_error", nil, "", http.StatusNotFound).Wrap(getErr)
		}
		return nil, storeAppError("DuplicatePage", getErr)
	}

	dest := sourceSpace
	if targetSpace != nil {
		dest = targetSpace
	}
	destSpaceID := dest.Id

	if destSpaceID != source.SpaceId && sourceSpace.TeamId != dest.TeamId {
		return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.cross_team.app_error", nil, "", http.StatusBadRequest)
	}

	var destParentID string
	switch {
	case targetParentID != nil:
		destParentID = *targetParentID
	case destSpaceID == source.SpaceId:
		destParentID = source.ParentId
	}

	// Pre-check the destination parent's existence for its more specific invalid_parent rejection;
	// the depth cap is enforced authoritatively by store.CreatePageSubtree and surfaces
	// through the duplicate-specific message keys below.
	// destParentID equal to the source is a legitimate case (nesting the copy under the original), not a cycle.
	if destParentID != "" {
		if destErr := s.validateParentExists("DuplicatePage", destParentID, destSpaceID); destErr != nil {
			return nil, destErr
		}
	}

	pages := buildDuplicatePages(source, descendants, destSpaceID, destParentID, userID)

	s.log.Debug("Duplicating page", "page_id", pageID, "source_space_id", sourceSpace.Id, "user_id", userID)

	created, createErr := s.store.CreatePageSubtree(pages, MaxPageDepth)
	if createErr != nil {
		if store.IsErrNotFound(createErr) {
			return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.dest_not_found.app_error", nil, "", http.StatusNotFound).Wrap(createErr)
		}
		// A copied subtree that is itself too deep gets a duplicate-specific message; a
		// plain placement-depth breach uses storeAppError's operation-neutral key.
		var limErr *store.ErrLimitExceeded
		if errors.As(createErr, &limErr) && limErr.Reason == store.ReasonSubtreeMaxDepthExceeded {
			return nil, mmmodel.NewAppError("DuplicatePage", "app.page.duplicate.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest).Wrap(createErr)
		}
		return nil, storeAppError("DuplicatePage", createErr)
	}

	root := created[0]
	// parent_id is included because clients have never seen the copy: without it a tree view
	// cannot place the new node and would need a follow-up fetch.
	s.publishToChannels(wsEventPageDuplicated, map[string]any{"page_id": root.Id, "space_id": root.SpaceId, "parent_id": root.ParentId}, root.ChannelId)

	return root, nil
}

// buildDuplicatePages assembles the copies to insert: the root copy of source (retitled, placed
// under destParentID in destSpaceID) followed by a copy of each descendant. IDs are pre-generated
// before the bulk insert so each descendant's new ParentId can be resolved; descendants is
// pre-ordered (parent always before children), so idMap always has the new parent ID ready when
// it is needed.
func buildDuplicatePages(source *model.Page, descendants []*model.Page, destSpaceID, destParentID, userID string) []*model.Page {
	rootID := mmmodel.NewId()
	idMap := map[string]string{source.Id: rootID}
	pages := make([]*model.Page, 0, 1+len(descendants))
	pages = append(pages, clonePageFields(source, rootID, destSpaceID, destParentID, copyTitle(source.Title), userID))
	for _, d := range descendants {
		newID := mmmodel.NewId()
		pages = append(pages, clonePageFields(d, newID, destSpaceID, idMap[d.ParentId], d.Title, userID))
		idMap[d.Id] = newID
	}
	return pages
}

func clonePageFields(src *model.Page, id, spaceID, parentID, title, userID string) *model.Page {
	return &model.Page{
		Id:             id,
		SpaceId:        spaceID,
		ParentId:       parentID,
		Type:           src.Type,
		Title:          title,
		Body:           src.Body,
		SearchText:     src.SearchText,
		Props:          maps.Clone(src.Props),
		UserId:         userID,
		LastModifiedBy: userID,
	}
}

// copyTitle prefixes "Copy of " and truncates to the page-title cap so the duplicate's title
// always passes CreatePage validation.
func copyTitle(original string) string {
	title, _ := mmmodel.LimitRunes("Copy of "+original, model.PageTitleMaxRunes)
	return title
}
