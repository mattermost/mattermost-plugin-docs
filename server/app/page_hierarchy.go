// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetPageChildren fetches direct live children of a page. spaceID scopes the read to the page's
// expected space; the internal page-in-space check (GetPageInSpace below) and the store read after
// it are separate, unlocked queries, so this narrows but does not fully close the race between them.
// perPage <= 0 defaults to PerPageDefault; larger values are capped at PerPageMaximum.
func (s *Service) GetPageChildren(pageID, spaceID string, page, perPage int) ([]*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageChildren", "app.page.get_children.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if _, err := s.GetPageInSpace("GetPageChildren", pageID, spaceID, false); err != nil {
		return nil, err
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetPageChildren(pageID, offset, limit)
	if storeErr != nil {
		return nil, storeAppError("GetPageChildren", storeErr)
	}
	return pages, nil
}

// GetPageInSpace fetches pageID (including soft-deleted rows when includeDeleted is set) and
// rejects with a not-found AppError — rather than leaking that the page exists elsewhere — when it
// does not belong to spaceID. where identifies the calling operation for the returned error id.
// Shared by GetPageChildren and by the API layer's pre-checks ahead of a mutation,
// so the page-in-space check lives in one place instead of being reimplemented per caller.
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

// validateDestinationParent rejects a destination parent that is the page itself, does not exist,
// or lives outside expectedSpaceID. where identifies the calling operation for the returned error.
// Shared by MovePage and MovePageToSpace, where pageID is the very page being relocated, so a
// destination equal to pageID is always a direct self-parent cycle. The cycle-via-subtree walk is
// left to the caller, as it differs by direction between an in-space move and a cross-space move.
func (s *Service) validateDestinationParent(where, pageID, destParentID, expectedSpaceID string) *mmmodel.AppError {
	if destParentID == pageID {
		return mmmodel.NewAppError(where, "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest)
	}
	return s.validateParentExists(where, destParentID, expectedSpaceID)
}

// validateParentExists rejects a destination parent that does not exist or lives outside
// expectedSpaceID. Used by DuplicatePage, where pageID identifies the source page being copied, not
// the copy's (as-yet ungenerated) id, so a destParentID equal to the source is not a cycle and the
// self-parent check validateDestinationParent adds is not applicable.
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

// MovePage reparents a page under newParentID (nil leaves the parent unchanged) within its own
// space and positions it in the destination sibling group (newIndex nil appends; non-nil places
// it at that index, clamped to the group's bounds rather than rejected if out of range). It rejects
// a move that would create a cycle (the destination is the page itself or one of its descendants),
// cross a space boundary, or breach the depth cap, then performs the move optimistically-locked on
// expectedUpdateAt (force overrides a stale baseline). Cycle/depth checks apply only when the
// parent actually changes.
func (s *Service) MovePage(pageID, spaceID string, newParentID *string, newIndex *int64, expectedUpdateAt int64, force bool) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("MovePage", "app.page.move.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("MovePage", "app.page.move.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	page, getErr := s.GetPageInSpace("MovePage", pageID, spaceID, false)
	if getErr != nil {
		return nil, getErr
	}

	// Validate the destination only when reparenting to a non-root parent that actually changes.
	if newParentID != nil && *newParentID != "" && *newParentID != page.ParentId {
		destParentID := *newParentID
		if destErr := s.validateDestinationParent("MovePage", pageID, destParentID, page.SpaceId); destErr != nil {
			return nil, destErr
		}
		// Cycle guard: the page may not move under one of its own descendants. Walking the
		// destination's ancestors and rejecting pageID covers the whole descendant set.
		ancestors, ancErr := s.store.GetPageAncestorIDs(destParentID)
		if ancErr != nil {
			return nil, storeAppError("MovePage", ancErr)
		}
		for _, a := range ancestors {
			if a.Id == pageID {
				return nil, mmmodel.NewAppError("MovePage", "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest)
			}
		}
		// The moved page becomes a child of the destination, mirroring CreatePage's depth rule:
		// ancestors counts the destination's ancestors (not itself), so the destination is at
		// len(ancestors)+1 and the moved page one deeper, len(ancestors)+2; the cap also covers the
		// deepest descendant below the moved page.
		descendants, descErr := s.store.GetPageDescendantIDParents(pageID)
		if descErr != nil {
			return nil, storeAppError("MovePage", descErr)
		}
		if capErr := checkDepthCap("MovePage", len(ancestors)+2, model.MaxDepthOfPreOrderedPages(descendants, pageID)); capErr != nil {
			return nil, capErr
		}
	}

	moved, storeErr := s.store.MovePage(pageID, spaceID, newParentID, newIndex, expectedUpdateAt, force, MaxPageDepth)
	if storeErr != nil {
		return nil, storeAppError("MovePage", storeErr)
	}
	return moved, nil
}

// MovePageToSpace moves a page and its whole subtree to another space within the same team
// (parentPageID nil/"" places it at the target root). It rejects a cross-team move, a destination
// parent in the wrong space, a cycle (the destination parent inside the moving subtree), and a move
// whose subtree would breach the depth cap, then performs the move. Returns the moved page.
// Per-page restriction re-derivation and redirects are not handled here yet.
func (s *Service) MovePageToSpace(pageID, sourceSpaceID, targetSpaceID string, parentPageID *string, expectedUpdateAt int64, force bool) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(sourceSpaceID) {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_source_space.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(targetSpaceID) {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_target_space.app_error", nil, "", http.StatusBadRequest)
	}

	page, getErr := s.GetPageInSpace("MovePageToSpace", pageID, sourceSpaceID, false)
	if getErr != nil {
		return nil, getErr
	}
	sameTeam, crossErr := s.sameTeamSpaces(page.SpaceId, targetSpaceID)
	if crossErr != nil {
		if crossErr.StatusCode == http.StatusNotFound {
			return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.target_not_found.app_error", nil, "", http.StatusNotFound).Wrap(crossErr)
		}
		return nil, crossErr
	}
	if !sameTeam {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.cross_team.app_error", nil, "", http.StatusBadRequest)
	}

	// Resolve the requested parent: nil and "" both mean the target root (a cross-space move can't
	// keep a parent that lives in the source space).
	requestedParent := ""
	if parentPageID != nil {
		requestedParent = *parentPageID
	}
	// Only a genuine no-op — source and target space are the same and the page is already under
	// exactly the requested parent — short-circuits here; every real relocation, including a
	// same-space move to the root, falls through to the store, which enforces the optimistic-lock
	// CAS. This no-op path bypasses that store CAS, so the block below re-enforces the
	// optimistic-lock baseline manually.
	if sourceSpaceID == targetSpaceID && page.ParentId == requestedParent {
		// This no-op path never reaches the store CAS, so enforce the optimistic-lock baseline here
		// rather than silently succeeding against a stale baseline. force skips it, matching the store.
		// Re-fetch immediately before the check rather than reusing the read from the top of this
		// function: two GetSpace round-trips elapsed since then, during which a concurrent UpdatePage
		// could have bumped page.UpdateAt, and comparing against the stale value would let this no-op
		// succeed where the store's locked CAS would have reported a conflict.
		if !force {
			fresh, freshErr := s.GetPage(pageID)
			if freshErr != nil {
				return nil, freshErr
			}
			if fresh.UpdateAt != expectedUpdateAt {
				return nil, mmmodel.NewAppError("MovePageToSpace", "app.store.conflict.app_error", nil, "", http.StatusConflict)
			}
			return fresh, nil
		}
		return page, nil
	}

	// Fetch the moving subtree once; both the cycle guard and the depth cap below need it.
	descendants, descErr := s.store.GetPageDescendantIDParents(pageID)
	if descErr != nil {
		return nil, storeAppError("MovePageToSpace", descErr)
	}

	// Depth cap (parity with MovePage): the moved page lands at destinationDepth (1 at the target
	// root, len(ancestors)+2 under a parent) and its subtree extends below it.
	destinationDepth := 1
	if parentPageID != nil && *parentPageID != "" {
		destParentID := *parentPageID
		if destErr := s.validateDestinationParent("MovePageToSpace", pageID, destParentID, targetSpaceID); destErr != nil {
			return nil, destErr
		}
		// The destination parent may not live inside the moving subtree, or the move would detach
		// the subtree into a cycle.
		for _, d := range descendants {
			if d.Id == destParentID {
				return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest)
			}
		}
		ancestors, ancErr := s.store.GetPageAncestorIDs(destParentID)
		if ancErr != nil {
			return nil, storeAppError("MovePageToSpace", ancErr)
		}
		destinationDepth = len(ancestors) + 2
	}
	if capErr := checkDepthCap("MovePageToSpace", destinationDepth, model.MaxDepthOfPreOrderedPages(descendants, pageID)); capErr != nil {
		return nil, capErr
	}

	moved, storeErr := s.store.MovePageToSpace(pageID, sourceSpaceID, targetSpaceID, parentPageID, expectedUpdateAt, force, MaxPageDepth)
	if storeErr != nil {
		return nil, storeAppError("MovePageToSpace", storeErr)
	}
	return moved, nil
}

// checkDepthCap rejects placing a page at destinationDepth, or a subtree whose deepest descendant
// (subtreeMax levels below it) would breach MaxPageDepth. where identifies the caller. Shared by
// MovePage, MovePageToSpace, and DuplicatePage.
func checkDepthCap(where string, destinationDepth, subtreeMax int) *mmmodel.AppError {
	if destinationDepth > MaxPageDepth {
		return mmmodel.NewAppError(where, "app.page.move.max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
	}
	if destinationDepth+subtreeMax > MaxPageDepth {
		return mmmodel.NewAppError(where, "app.page.move.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": MaxPageDepth}, "", http.StatusBadRequest)
	}
	return nil
}
