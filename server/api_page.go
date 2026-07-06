// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// maxPageBodyBytes caps the raw HTTP request body for page create/update/move/duplicate. A request
// at both service-level maxes (model.PageBodyMaxBytes + model.PageSearchTextMaxBytes, 4 MiB) still
// needs room for the JSON envelope, field names, and any characters needing \-escaping inside
// Body/SearchText — all of which only grow the wire size beyond the decoded byte length the model
// validates. Doubling the combined max gives that headroom so a model-valid payload is never
// rejected here before reaching validation.
const maxPageBodyBytes = 2 * (model.PageBodyMaxBytes + model.PageSearchTextMaxBytes) // 8 MiB

// resolvePageInSpace fetches the page scoped to spaceID via the App layer (which owns the
// page-belongs-to-space check — see Service.GetPageInSpace), writing the appropriate error response and
// returning ok=false when the caller should stop. where identifies the calling handler.
// includeDeleted also resolves soft-deleted pages (DeleteAt != 0).
func (p *Plugin) resolvePageInSpace(w http.ResponseWriter, spaceID, pageID, where string, includeDeleted bool) (*model.Page, bool) {
	page, appErr := p.service.GetPageInSpace(where, pageID, spaceID, includeDeleted)
	if appErr != nil {
		writeAppError(w, appErr)
		return nil, false
	}
	return page, true
}

// handleCreatePage handles POST /api/v1/spaces/{space_id}/pages.
func (p *Plugin) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	spaceID := mux.Vars(r)["space_id"]
	userID := userIDFromRequest(r)

	var req struct {
		Title      string `json:"title"`
		ParentId   string `json:"parent_id,omitempty"`
		Body       string `json:"content,omitempty"`
		SearchText string `json:"search_text,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleCreatePage", false) {
		return
	}

	page, appErr := p.service.CreatePage(spaceID, req.ParentId, req.Title, req.Body, req.SearchText, userID, "")
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, page)
}

// handleGetSpacePage handles GET /api/v1/spaces/{space_id}/pages/{page_id}.
func (p *Plugin) handleGetSpacePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	page, ok := p.resolvePageInSpace(w, vars["space_id"], vars["page_id"], "handleGetSpacePage", false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleUpdatePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}. The optimistic-lock
// baseline (base_edit_at) is carried in the body; a stale value yields 409 unless force is set.
func (p *Plugin) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)

	var req struct {
		Title      *string `json:"title,omitempty"`
		Body       *string `json:"content,omitempty"`
		SearchText *string `json:"search_text,omitempty"`
		BaseEditAt *int64  `json:"base_edit_at,omitempty"`
		Force      bool    `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleUpdatePage", false) {
		return
	}

	// UpdatePage already resolves the page scoped to space_id, so no pre-check here.
	// The request's "content" maps onto the page body; nil fields are left unchanged. The app
	// layer validates the patch (rejecting a nil/no-op or search-text-without-body patch).
	patch := &model.PagePatch{Title: req.Title, Body: req.Body, SearchText: req.SearchText}

	updated, appErr := p.service.UpdatePage(vars["page_id"], vars["space_id"], patch, int64OrZero(req.BaseEditAt), req.Force, userID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeletePage handles DELETE /api/v1/spaces/{space_id}/pages/{page_id}.
func (p *Plugin) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	// DeletePage already resolves the page scoped to space_id, so no pre-check here.
	if appErr := p.service.DeletePage(vars["page_id"], vars["space_id"]); appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}

// handleRestorePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/restore.
func (p *Plugin) handleRestorePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	// RestorePage already resolves the page scoped to space_id (regardless of live/deleted state),
	// so no pre-check here.
	restored, appErr := p.service.RestorePage(vars["page_id"], vars["space_id"])
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

// handleGetPageBreadcrumb handles GET /api/v1/spaces/{space_id}/pages/{page_id}/breadcrumb,
// returning the page's ancestor chain, root down, as a plain JSON array — bounded by the depth
// limit, so it skips the paginatedResponse envelope the list endpoints use.
func (p *Plugin) handleGetPageBreadcrumb(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	// GetPageAncestors already resolves the page and checks its space, so no pre-check here.
	ancestors, appErr := p.service.GetPageAncestors(vars["page_id"], vars["space_id"])
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, ancestors)
}

// handleMovePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/move. It reparents the page
// (parent_id nil leaves the parent unchanged; "" moves to the space root) and positions it at
// sibling_index within the destination sibling group (clamped to the group's bounds rather than
// rejected if out of range), optimistic-locked on expected_update_at unless force.
func (p *Plugin) handleMovePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	var req struct {
		ParentId         *string `json:"parent_id,omitempty"`
		SiblingIndex     *int64  `json:"sibling_index,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleMovePage", false) {
		return
	}

	// MovePage already resolves the page scoped to space_id, so no pre-check here.
	moved, appErr := p.service.MovePage(vars["page_id"], vars["space_id"], req.ParentId, req.SiblingIndex, int64OrZero(req.ExpectedUpdateAt), req.Force)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, moved)
}

// handleDuplicatePage handles POST /api/v1/spaces/{space_id}/pages/{page_id}/duplicate. An empty (or
// all-default) body duplicates the page in place: same space, same parent, single page.
// include_children copies the whole subtree; target_space_id/parent_id redirect the copy
// elsewhere.
func (p *Plugin) handleDuplicatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)

	var req struct {
		IncludeChildren bool    `json:"include_children,omitempty"`
		TargetSpaceId   string  `json:"target_space_id,omitempty"`
		ParentId        *string `json:"parent_id,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleDuplicatePage", true) {
		return
	}

	// DuplicatePage already resolves the source page scoped to space_id, so no pre-check here.
	duplicated, appErr := p.service.DuplicatePage(vars["page_id"], vars["space_id"], userID, req.IncludeChildren, req.TargetSpaceId, req.ParentId)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, duplicated)
}

// handleGetPageChildren handles GET /api/v1/spaces/{space_id}/pages/{page_id}/children, returning
// the page's direct live children (paginated).
func (p *Plugin) handleGetPageChildren(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	page, perPage := pageParam(r), perPageParam(r)
	// GetPageChildren already resolves the page and checks its space, so no pre-check here.
	children, appErr := p.service.GetPageChildren(vars["page_id"], vars["space_id"], page, perPage)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, children, page, perPage)
}

// handleMovePageToSpace handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/move-to-space,
// moving the page and its subtree to target_space_id (parent_id optional; "" = target root).
// Optimistic-locked on expected_update_at (the moved root's last-seen UpdateAt) unless force is set.
func (p *Plugin) handleMovePageToSpace(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	var req struct {
		TargetSpaceId    string  `json:"target_space_id"`
		ParentId         *string `json:"parent_id,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleMovePageToSpace", false) {
		return
	}

	// MovePageToSpace already resolves the page scoped to space_id, so no pre-check here.
	moved, appErr := p.service.MovePageToSpace(vars["page_id"], vars["space_id"], req.TargetSpaceId, req.ParentId, int64OrZero(req.ExpectedUpdateAt), req.Force)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, moved)
}
