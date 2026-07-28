// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// GetPageChildren returns one page of metadata summaries for a page's direct live children,
// scoped to spaceID, plus whether more children exist beyond it.
func (s *Service) GetPageChildren(pageID, spaceID string, page, perPage int) ([]*model.PageSummary, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, false, mmmodel.NewAppError("GetPageChildren", "app.page.get_children.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, false, mmmodel.NewAppError("GetPageChildren", "app.page.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	// Existence-only probe: a missing or wrong-space parent 404s without hauling the parent's
	// body across the wire only to discard it. Both cases read as not-found so the error can't
	// be used to probe page ids in other spaces.
	exists, existsErr := s.store.PageExistsInSpace(pageID, spaceID)
	if existsErr != nil {
		return nil, false, storeAppError("GetPageChildren", existsErr)
	}
	if !exists {
		return nil, false, mmmodel.NewAppError("GetPageChildren", "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	pages, storeErr := s.store.GetPageChildren(pageID, spaceID, offset, limit)
	if storeErr != nil {
		return nil, false, storeAppError("GetPageChildren", storeErr)
	}
	pages, hasMore := trimPage(pages, limit)
	return pages, hasMore, nil
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
		return mmmodel.NewAppError(where, "app.page.circular_reference.app_error", nil, "", http.StatusBadRequest)
	}
	return s.validateParentExists(where, destParentID, expectedSpaceID)
}

// validateParentExists rejects a parent ID that is malformed, doesn't exist, or lives outside
// expectedSpaceID. All three cases read identically, so the caller can't use the distinct error
// to probe page ids in spaces it isn't a member of. The check is an existence-only probe — the
// parent's body is never fetched. Unlike validateDestinationParent, it doesn't check for
// self-parenting (DuplicatePage may legitimately place the copy under its source).
func (s *Service) validateParentExists(where, destParentID, expectedSpaceID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(destParentID) {
		return mmmodel.NewAppError(where, "app.page.invalid_parent.app_error", nil, "", http.StatusBadRequest)
	}
	exists, existsErr := s.store.PageExistsInSpace(destParentID, expectedSpaceID)
	if existsErr != nil {
		return storeAppError(where, existsErr)
	}
	if !exists {
		return mmmodel.NewAppError(where, "app.page.invalid_parent.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// MovePage reparents a page within its space. newParentID nil = no reparent; "" = move to root.
// newIndex nil appends; non-nil places at that index, clamped to bounds. Cycle, cross-space-
// parent, and depth-cap violations are enforced authoritatively by store.MovePage and
// surface here through storeAppError's shared message keys; only the parent's
// existence is pre-checked here, for its more specific invalid_parent rejection.
// Optimistic-locked on expectedUpdateAt (force overrides); a nil expectedUpdateAt without force
// is rejected. userID is the acting user, recorded in logs only — a move does not change the
// page's LastModifiedBy.
func (s *Service) MovePage(pageID, spaceID string, newParentID *string, newIndex *int64, expectedUpdateAt *int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("MovePage", "app.page.move.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("MovePage", "app.page.move.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("MovePage", "app.page.move.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := requireBaseline("MovePage", "expected_update_at", expectedUpdateAt, force); appErr != nil {
		return nil, appErr
	}
	// A parent-only probe, not a full page fetch: the move needs just the current parent for the
	// parent-change check below, so the page body is never hauled here.
	curParentID, exists, parentErr := s.store.GetPageParentInSpace(pageID, spaceID)
	if parentErr != nil {
		return nil, storeAppError("MovePage", parentErr)
	}
	if !exists {
		return nil, mmmodel.NewAppError("MovePage", "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}

	// Validate the destination only when reparenting to a non-root parent that actually changes.
	if newParentID != nil && *newParentID != "" && *newParentID != curParentID {
		if destErr := s.validateDestinationParent("MovePage", pageID, *newParentID, spaceID); destErr != nil {
			return nil, destErr
		}
	}

	s.log.Debug("Moving page", "page_id", pageID, "space_id", spaceID, "user_id", userID)

	return s.reparentWithinSpace("MovePage", pageID, spaceID, newParentID, newIndex, expectedUpdateAt, force)
}

// reparentWithinSpace performs the shared tail of an in-space move — the authoritative store
// reparent and the page_moved publish — for MovePage and MovePageToSpace's same-space delegation,
// so the event's payload shape and channel scoping live in one place. old_parent_id is the
// parent the store actually replaced, not the callers' earlier probes: a concurrent move
// committed in between (surviving here via force) would make the earlier-read parent stale and
// point clients at the wrong subtree to invalidate.
func (s *Service) reparentWithinSpace(where, pageID, spaceID string, newParentID *string, newIndex *int64, expectedUpdateAt *int64, force bool) (*model.Page, *mmmodel.AppError) {
	moved, priorParentID, didMove, storeErr := s.store.MovePage(pageID, spaceID, newParentID, newIndex, mmmodel.SafeDereference(expectedUpdateAt), force, model.MaxPageDepth)
	if storeErr != nil {
		return nil, storeAppError(where, storeErr)
	}
	if didMove {
		s.publishToChannels(wsEventPageMoved, map[string]any{
			"page_id":       moved.Id,
			"space_id":      moved.SpaceId,
			"old_parent_id": priorParentID,
			"new_parent_id": moved.ParentId,
		}, moved.ChannelId)
	}
	return moved, nil
}

// MovePageToSpace moves a page and its subtree to another space in the same team. parentPageID
// nil/"" places it at the target root. Rejects cross-team moves and wrong-space parents here;
// destination-inside-subtree cycles and depth-cap breaches are enforced authoritatively by
// store.MovePageToSpace and surface through storeAppError's shared message keys.
// A nil expectedUpdateAt without force is rejected: the mutation must supply a baseline.
// sourceSpace and targetSpace are the caller's already-fetched records (from its membership
// gates), so no re-read happens here. userID is the acting user and must be a valid ID: the store
// scopes the target-space draft quota to the drafts it owns. It does not change the page's
// LastModifiedBy. Per-page restrictions and redirects are not handled yet.
func (s *Service) MovePageToSpace(pageID string, sourceSpace, targetSpace *model.Space, parentPageID *string, expectedUpdateAt *int64, force bool, userID string) (*model.Page, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_id.app_error", nil, "", http.StatusBadRequest)
	}
	if sourceSpace == nil {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_source_space.app_error", nil, "", http.StatusBadRequest)
	}
	if targetSpace == nil {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_target_space.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := requireBaseline("MovePageToSpace", "expected_update_at", expectedUpdateAt, force); appErr != nil {
		return nil, appErr
	}

	// A parent-only probe, not a full page fetch: the move needs just the current parent for the
	// parent-change check below, so the page body is never hauled here.
	curParentID, exists, parentErr := s.store.GetPageParentInSpace(pageID, sourceSpace.Id)
	if parentErr != nil {
		return nil, storeAppError("MovePageToSpace", parentErr)
	}
	if !exists {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}
	if sourceSpace.TeamId != targetSpace.TeamId {
		return nil, mmmodel.NewAppError("MovePageToSpace", "app.page.move_to_space.cross_team.app_error", nil, "", http.StatusBadRequest)
	}

	// Resolve the requested parent: nil and "" both mean the target root (a cross-space move can't
	// keep a parent that lives in the source space).
	requestedParent := ""
	if parentPageID != nil {
		requestedParent = *parentPageID
	}
	// A same-space request never needs the cross-space machinery: the subtree keeps its
	// SpaceId/ChannelId, so delegate to store.MovePage.
	//
	// A same-parent request no-ops there (a stale expected_update_at still conflicts, force
	// still overrides, and nothing is published). A real reparent appends under the new parent
	// like a cross-space arrival would, but without collecting the subtree — so the
	// descendant-count cap and the draft/snapshot rewrites never apply — and publishes the
	// ordinary page_moved event.
	//
	// requestedParent is passed explicitly because nil means "target root" here but "leave
	// unchanged" to MovePage.
	if sourceSpace.Id == targetSpace.Id {
		if requestedParent != "" && requestedParent != curParentID {
			if destErr := s.validateDestinationParent("MovePageToSpace", pageID, requestedParent, sourceSpace.Id); destErr != nil {
				return nil, destErr
			}
		}
		return s.reparentWithinSpace("MovePageToSpace", pageID, sourceSpace.Id, &requestedParent, nil, expectedUpdateAt, force)
	}

	// Pre-check the destination parent's existence for its more specific invalid_parent rejection;
	// a parent inside the moving subtree lives in the source space on a cross-space move, so this
	// also rejects that case before the store's own authoritative cycle re-check.
	if requestedParent != "" {
		if destErr := s.validateDestinationParent("MovePageToSpace", pageID, requestedParent, targetSpace.Id); destErr != nil {
			return nil, destErr
		}
	}

	s.log.Debug("Moving page to space", "page_id", pageID, "source_space_id", sourceSpace.Id, "target_space_id", targetSpace.Id, "user_id", userID)

	moved, priorParentID, storeErr := s.store.MovePageToSpace(pageID, sourceSpace.Id, targetSpace.Id, userID, parentPageID, mmmodel.SafeDereference(expectedUpdateAt), force, model.MaxPageDepth)
	if storeErr != nil {
		return nil, storeAppError("MovePageToSpace", storeErr)
	}
	// Each side gets only its own space's half of the move: members of the source channel may
	// not be members of the target space and vice versa, so a shared payload naming both spaces
	// (and both parents) would leak the other space's existence and activity to users who cannot
	// read it. old_parent_id is the parent the store actually replaced (see reparentWithinSpace).
	// A member of both spaces receives both events.
	s.publishToChannels(wsEventPageMovedToSpace, map[string]any{
		"page_id":         moved.Id,
		"source_space_id": sourceSpace.Id,
		"old_parent_id":   priorParentID,
	}, sourceSpace.ChannelId)
	s.publishToChannels(wsEventPageMovedToSpace, map[string]any{
		"page_id":         moved.Id,
		"target_space_id": targetSpace.Id,
		"new_parent_id":   moved.ParentId,
	}, moved.ChannelId)
	return moved, nil
}
