// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"
	"slices"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetPageChildren returns direct live children of a page, scoped to spaceID.
func (s *Service) GetPageChildren(pageID, spaceID string, page, perPage int) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageChildren", "app.page.get_children.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPageInSpace("GetPageChildren", pageID, spaceID, false); err != nil {
		return nil, err
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetPageChildren(pageID, spaceID, offset, limit)
	if storeErr != nil {
		return nil, storeAppError("GetPageChildren", storeErr)
	}
	return pages, nil
}

// GetPageInSpace fetches a page and rejects with not-found (not "wrong space") when it isn't in
// spaceID. includeDeleted surfaces soft-deleted rows. where names the calling operation for error ids.
func (s *Service) GetPageInSpace(where, pageID, spaceID string, includeDeleted bool) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError(where, "app.page.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	var page *model.Page
	var err *mmmodel.AppError
	if includeDeleted {
		page, err = s.GetPageWithDeleted(pageID)
	} else {
		page, err = s.GetPage(pageID)
	}
	if err != nil {
		return nil, err
	}
	if page.SpaceId != spaceID {
		return nil, mmmodel.NewAppError(where, "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return page, nil
}

// validateDestinationParent rejects a parent that is the page itself, doesn't exist, or lives
// outside expectedSpaceID. Subtree-cycle checks are left to the caller (they differ between
// in-space and cross-space moves).
func (s *Service) validateDestinationParent(where, pageID, destParentID, expectedSpaceID string) *mmmodel.AppError {
	if destParentID == pageID {
		return mmmodel.NewAppError(where, "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest)
	}
	return s.validateParentExists(where, destParentID, expectedSpaceID)
}

// validateParentExists rejects a parent that doesn't exist or lives outside expectedSpaceID.
// Unlike validateDestinationParent, it doesn't check for self-parenting (DuplicatePage may
// legitimately place the copy under its source).
func (s *Service) validateParentExists(where, destParentID, expectedSpaceID string) *mmmodel.AppError {
	parent, parentErr := s.GetPage(destParentID)
	if parentErr != nil {
		// A missing parent is a bad request; any other failure (e.g. a transient store error)
		// propagates unchanged rather than being masked as invalid_parent.
		if parentErr.StatusCode == http.StatusNotFound {
			return mmmodel.NewAppError(where, "app.page.move.invalid_parent.app_error", nil, "", http.StatusBadRequest).Wrap(parentErr)
		}
		return parentErr
	}
	if parent.SpaceId != expectedSpaceID {
		return mmmodel.NewAppError(where, "app.page.move.parent_different_space.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// MovePage reparents a page within its space. newParentID nil = no reparent; "" = move to root.
// newIndex nil appends; non-nil places at that index, clamped to bounds. Rejects cycles, cross-space
// targets, and depth cap breaches. Optimistic-locked on expectedUpdateAt (force overrides).
// Cycle and depth checks run only when the parent actually changes.
func (s *Service) MovePage(pageID, spaceID string, newParentID *string, newIndex *int64, expectedUpdateAt int64, force bool) (*model.Page, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, false, mmmodel.NewAppError("MovePage", "app.page.move.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, false, mmmodel.NewAppError("MovePage", "app.page.move.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, getErr := s.GetPageInSpace("MovePage", pageID, spaceID, false)
	if getErr != nil {
		return nil, false, getErr
	}

	// Validate the destination only when reparenting to a non-root parent that actually changes.
	if newParentID != nil && *newParentID != "" && *newParentID != page.ParentId {
		destParentID := *newParentID
		if destErr := s.validateDestinationParent("MovePage", pageID, destParentID, page.SpaceId); destErr != nil {
			return nil, false, destErr
		}
		// Cycle guard: the page may not move under one of its own descendants. Walking the
		// destination's ancestors and rejecting pageID covers the whole descendant set.
		ancestors, ancErr := s.store.GetPageAncestorIDs(destParentID)
		if ancErr != nil {
			return nil, false, storeAppError("MovePage", ancErr)
		}
		if slices.Contains(ancestors, pageID) {
			return nil, false, mmmodel.NewAppError("MovePage", "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest)
		}
		// Mirrors CreatePage's depth rule: destination is at len(ancestors)+1, moved page at
		// len(ancestors)+2. The cap also covers the deepest descendant below the moved page.
		descendants, descErr := s.store.GetPageDescendantIDParents(pageID)
		if descErr != nil {
			return nil, false, storeAppError("MovePage", descErr)
		}
		if capErr := checkDepthCap("MovePage", len(ancestors)+2, model.MaxDepthOfPreOrderedIDParents(descendants, pageID)); capErr != nil {
			return nil, false, capErr
		}
	}

	moved, didMove, storeErr := s.store.MovePage(pageID, spaceID, newParentID, newIndex, expectedUpdateAt, force, MaxPageDepth)
	if storeErr != nil {
		return nil, false, storeAppError("MovePage", storeErr)
	}
	return moved, didMove, nil
}

// MovePageToSpace moves a page and its subtree to another space in the same team. parentPageID
// nil/"" places it at the target root. Rejects cross-team moves, wrong-space parents, destination-
// inside-subtree cycles, and depth cap breaches. Per-page restrictions and redirects are not handled yet.
func (s *Service) MovePageToSpace(pageID, sourceSpaceID, targetSpaceID string, parentPageID *string, expectedUpdateAt int64, force bool) (*model.Page, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(sourceSpaceID) {
		return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_source_space.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(targetSpaceID) {
		return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_target_space.app_error", nil, "", http.StatusBadRequest)
	}

	page, getErr := s.GetPageInSpace("MovePageToSpace", pageID, sourceSpaceID, false)
	if getErr != nil {
		return nil, false, getErr
	}
	sameTeam, crossErr := s.sameTeamSpaces(page.SpaceId, targetSpaceID)
	if crossErr != nil {
		if crossErr.StatusCode == http.StatusNotFound {
			return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.target_not_found.app_error", nil, "", http.StatusNotFound).Wrap(crossErr)
		}
		return nil, false, crossErr
	}
	if !sameTeam {
		return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.cross_team.app_error", nil, "", http.StatusBadRequest)
	}

	// Resolve the requested parent: nil and "" both mean the target root (a cross-space move can't
	// keep a parent that lives in the source space).
	requestedParent := ""
	if parentPageID != nil {
		requestedParent = *parentPageID
	}
	// Short-circuit if source and target space are the same and the parent isn't changing. Every
	// real move falls through to the store. The optimistic-lock check below is enforced manually
	// because this path doesn't reach the store write.
	if sourceSpaceID == targetSpaceID && page.ParentId == requestedParent {
		if force {
			return page, false, nil
		}
		// Re-fetch within the source space: picks up any concurrent UpdatePage that bumped
		// UpdateAt since the top-of-function read, so the optimistic-lock check is current.
		fresh, freshErr := s.GetPageInSpace("MovePageToSpace", pageID, sourceSpaceID, false)
		if freshErr != nil {
			return nil, false, freshErr
		}
		if fresh.UpdateAt != expectedUpdateAt {
			return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.store.conflict.app_error", nil, "", http.StatusConflict)
		}
		return fresh, false, nil
	}

	// Fetch the moving subtree once; both the cycle guard and the depth cap below need it.
	descendants, descErr := s.store.GetPageDescendantIDParents(pageID)
	if descErr != nil {
		return nil, false, storeAppError("MovePageToSpace", descErr)
	}

	// Depth cap (parity with MovePage): the moved page lands at destinationDepth (1 at the target
	// root, len(ancestors)+2 under a parent) and its subtree extends below it.
	destinationDepth := 1
	if parentPageID != nil && *parentPageID != "" {
		destParentID := *parentPageID
		if destErr := s.validateDestinationParent("MovePageToSpace", pageID, destParentID, targetSpaceID); destErr != nil {
			return nil, false, destErr
		}
		// The destination parent may not live inside the moving subtree, or the move would detach
		// the subtree into a cycle.
		if slices.ContainsFunc(descendants, func(d model.PageIDParentRef) bool { return d.Id == destParentID }) {
			return nil, false, mmmodel.NewAppError("MovePageToSpace", "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest)
		}
		ancestors, ancErr := s.store.GetPageAncestorIDs(destParentID)
		if ancErr != nil {
			return nil, false, storeAppError("MovePageToSpace", ancErr)
		}
		destinationDepth = len(ancestors) + 2
	}
	if capErr := checkDepthCap("MovePageToSpace", destinationDepth, model.MaxDepthOfPreOrderedIDParents(descendants, pageID)); capErr != nil {
		return nil, false, capErr
	}

	moved, storeErr := s.store.MovePageToSpace(pageID, sourceSpaceID, targetSpaceID, parentPageID, expectedUpdateAt, force, MaxPageDepth)
	if storeErr != nil {
		return nil, false, storeAppError("MovePageToSpace", storeErr)
	}
	return moved, true, nil
}

// checkDepthCap rejects placing a page deeper than MaxPageDepth, or a subtree whose deepest
// point (subtreeMax levels below the landing page) would exceed it.
func checkDepthCap(where string, destinationDepth, subtreeMax int) *mmmodel.AppError {
	if destinationDepth > MaxPageDepth {
		return mmmodel.NewAppError(where, "app.page.move.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
	}
	if destinationDepth+subtreeMax > MaxPageDepth {
		return mmmodel.NewAppError(where, "app.page.move.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
	}
	return nil
}
