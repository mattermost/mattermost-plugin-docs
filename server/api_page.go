// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// maxPageBodyBytes caps the raw request body for page create/update, sized with headroom over the
// model's Body and SearchText limits so ordinary payloads reach model validation. A body whose JSON
// encoding is dominated by escape sequences can still exceed this and be rejected with 413 rather
// than the model's 400; that is an intentional transport-level guard.
const maxPageBodyBytes = 8 << 20 // 8 MiB

// maxPageStructBodyBytes caps request bodies for move/duplicate/move-to-space, which carry only
// IDs, booleans, and timestamps — no content fields.
const maxPageStructBodyBytes = 4 * 1024 // 4 KiB

// requireTargetSpaceRead validates a target_space_id carried in the request body and resolves the
// caller's read access to it. Unlike the {space_id} in the URL path — which every handler checks
// up front — a body-supplied ID only surfaces after decoding, so it is checked inline here. It
// writes the error response and returns ok=false on failure. Callers pass the invalid-ID
// rejection as a pre-built AppError with a string-literal ID so the i18n extraction tool can
// discover the message key.
func (p *Plugin) requireTargetSpaceRead(w http.ResponseWriter, invalidIDErr *mmmodel.AppError, targetSpaceID, userID string) (*model.Space, app.ReadResolution, bool) {
	if !mmmodel.IsValidId(targetSpaceID) {
		p.writeAppError(w, invalidIDErr)
		return nil, app.ReadDenied, false
	}
	return p.requireSpaceRead(w, targetSpaceID, userID)
}

// handleCreatePage handles POST /api/v1/spaces/{space_id}/pages.
func (p *Plugin) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.fetchSpaceForGate(w, vars["space_id"], false)
	if !ok {
		return
	}
	if !p.requirePageWrite(w, space, userID, mmmodel.PermissionCreatePage) {
		return
	}

	// SearchText is not accepted: it is derived server-side from Body.
	var req struct {
		Title    string  `json:"title"`
		ParentId *string `json:"parent_id,omitempty"`
		Body     string  `json:"body,omitempty"`
	}
	if !p.decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleCreatePage", false) {
		return
	}
	page, appErr := p.service.CreatePage(vars["space_id"], mmmodel.SafeDereference(req.ParentId), req.Title, req.Body, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, page)
}

// handleGetPage handles GET /api/v1/spaces/{space_id}/pages/{page_id}.
func (p *Plugin) handleGetPage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpacePagePerm(w, vars["space_id"], userID, mmmodel.PermissionReadPage); !ok {
		return
	}
	page, appErr := p.service.GetPageInSpace("handleGetPage", vars["page_id"], vars["space_id"], false)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleUpdatePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}. The optimistic-lock
// baseline (base_edit_at) is required unless force is set; a stale value yields 409.
func (p *Plugin) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.fetchSpaceForGate(w, vars["space_id"], false)
	if !ok {
		return
	}
	if !p.requirePageWrite(w, space, userID, mmmodel.PermissionEditPage) {
		return
	}

	// SearchText is not accepted: it is derived server-side from Body (matches the create path).
	var req struct {
		Title      *string                  `json:"title"`
		Body       *string                  `json:"body"`
		Props      *mmmodel.StringInterface `json:"props"`
		BaseEditAt *int64                   `json:"base_edit_at"`
		Force      bool                     `json:"force"`
	}
	if !p.decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleUpdatePage", false) {
		return
	}
	patch := &model.PagePatch{Title: req.Title, Body: req.Body, Props: req.Props}

	updated, appErr := p.service.UpdatePage(vars["page_id"], vars["space_id"], patch, req.BaseEditAt, req.Force, userID)
	if appErr != nil {
		if updated != nil {
			p.writeConflictWithPage(w, appErr, updated)
			return
		}
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeletePage handles DELETE /api/v1/spaces/{space_id}/pages/{page_id}. Own/any: delete_page
// (any), or delete_own_page when the caller owns the page.
func (p *Plugin) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, read, ok := p.requireSpaceRead(w, vars["space_id"], userID)
	if !ok {
		return
	}
	page, appErr := p.service.GetPageInSpace("handleDeletePage", vars["page_id"], vars["space_id"], false)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	if !p.requireDeleteOwnOrAnyFrom(w, space, userID, page.UserId, read) {
		return
	}
	if appErr := p.service.DeletePage(vars["page_id"], vars["space_id"], userID); appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}

// handleRestorePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/restore. The page is
// soft-deleted at gate time, so the owner comparison resolves it via the include-deleted getter.
func (p *Plugin) handleRestorePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, read, ok := p.requireSpaceRead(w, vars["space_id"], userID)
	if !ok {
		return
	}
	page, appErr := p.service.GetPageInSpace("handleRestorePage", vars["page_id"], vars["space_id"], true)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	if !p.requireDeleteOwnOrAnyFrom(w, space, userID, page.UserId, read) {
		return
	}
	restored, appErr := p.service.RestorePage(vars["page_id"], vars["space_id"], userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

// handleMovePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/move. It reparents the page
// (parent_id nil leaves the parent unchanged; "" moves to the space root) and positions it at
// sibling_index within the destination sibling group (clamped to the group's bounds rather than
// rejected if out of range). The optimistic-lock baseline (expected_update_at) is required unless
// force is set. Gated on edit_page.
func (p *Plugin) handleMovePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.fetchSpaceForGate(w, vars["space_id"], false)
	if !ok {
		return
	}
	if !p.requirePageWrite(w, space, userID, mmmodel.PermissionEditPage) {
		return
	}

	var req struct {
		ParentId         *string `json:"parent_id,omitempty"`
		SiblingIndex     *int64  `json:"sibling_index,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !p.decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleMovePage", false) {
		return
	}
	moved, appErr := p.service.MovePage(vars["page_id"], vars["space_id"], req.ParentId, req.SiblingIndex, req.ExpectedUpdateAt, req.Force, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, moved)
}

// handleDuplicatePage handles POST /api/v1/spaces/{space_id}/pages/{page_id}/duplicate. An empty (or
// all-default) body duplicates the page in place: same space, same parent, single page.
// include_children copies the whole subtree; target_space_id/parent_id redirect the copy
// elsewhere. Gated on source read_page plus target create_page (target defaults to the source
// space, so an in-place duplicate is still create-gated).
func (p *Plugin) handleDuplicatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	sourceSpace, sourceRead, ok := p.requireSpaceRead(w, vars["space_id"], userID)
	if !ok {
		return
	}

	var req struct {
		IncludeChildren bool    `json:"include_children,omitempty"`
		TargetSpaceId   string  `json:"target_space_id,omitempty"`
		ParentId        *string `json:"parent_id,omitempty"`
	}
	if !p.decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleDuplicatePage", true) {
		return
	}
	// An omitted or same-space target duplicates into the source space; the fetched records and the
	// read each was admitted by are passed through so neither is resolved twice.
	targetSpace, targetRead := sourceSpace, sourceRead
	if req.TargetSpaceId != "" && req.TargetSpaceId != vars["space_id"] {
		var targetOK bool
		targetSpace, targetRead, targetOK = p.requireTargetSpaceRead(w, mmmodel.NewAppError("handleDuplicatePage", "api.page.duplicate.invalid_target_space_id.app_error", nil, "", http.StatusBadRequest), req.TargetSpaceId, userID)
		if !targetOK {
			return
		}
	}
	if !p.requirePageWriteFrom(w, targetSpace, userID, mmmodel.PermissionCreatePage, targetRead) {
		return
	}

	duplicated, appErr := p.service.DuplicatePage(vars["page_id"], sourceSpace, userID, req.IncludeChildren, targetSpace, req.ParentId)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, duplicated)
}

// handleGetPageChildren handles GET /api/v1/spaces/{space_id}/pages/{page_id}/children, returning
// the page's direct live-child metadata summaries (paginated).
func (p *Plugin) handleGetPageChildren(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpacePagePerm(w, vars["space_id"], userID, mmmodel.PermissionReadPage); !ok {
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
	children, hasMore, appErr := p.service.GetPageChildren(vars["page_id"], vars["space_id"], page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, children, page, perPage, hasMore)
}

// handleMovePageToSpace handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/move-to-space,
// moving the page and its subtree to target_space_id (parent_id optional; "" = target root).
// The optimistic-lock baseline (expected_update_at, the moved root's last-seen UpdateAt) is
// required unless force is set. Gated on source read_page, a remove-class delete permission over
// the moved subtree, and target create_page.
func (p *Plugin) handleMovePageToSpace(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	sourceSpace, sourceRead, ok := p.requireSpaceRead(w, vars["space_id"], userID)
	if !ok {
		return
	}

	var req struct {
		TargetSpaceId    string  `json:"target_space_id"`
		ParentId         *string `json:"parent_id,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !p.decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleMovePageToSpace", false) {
		return
	}
	if req.TargetSpaceId == "" {
		p.writeAppError(w, mmmodel.NewAppError("handleMovePageToSpace", "api.page.move_to_space.missing_target_space_id.app_error", nil, "", http.StatusBadRequest))
		return
	}
	// The fetched records are passed through so the service never re-reads them; a same-space
	// move reuses the membership gate's record instead of resolving the same space twice.
	targetSpace, targetRead := sourceSpace, sourceRead
	if req.TargetSpaceId != vars["space_id"] {
		var targetOK bool
		targetSpace, targetRead, targetOK = p.requireTargetSpaceRead(w, mmmodel.NewAppError("handleMovePageToSpace", "api.page.move_to_space.invalid_target_space_id.app_error", nil, "", http.StatusBadRequest), req.TargetSpaceId, userID)
		if !targetOK {
			return
		}
	}
	// Source-side remove-class gate: delete_page (any) if held, else delete_own_page, which
	// requires the caller own every page in the moved subtree (passed down as requiredOwnerID).
	// No source auto-join —
	// only the target side allows a non-member write. Resolved before the target gate below, which
	// can join the caller to the target space: a caller denied here must not be left holding a
	// membership the rejected request created.
	ownOnly, ok := p.requireOwnOrAnyFrom(w, "handleMovePageToSpace", sourceSpace, userID,
		"api.page.move_to_space", mmmodel.PermissionDeletePage,
		"api.page.move_to_space.own", mmmodel.PermissionDeleteOwnPage, true, sourceRead)
	if !ok {
		return
	}
	requiredOwnerID := ""
	if ownOnly {
		requiredOwnerID = userID
	}

	if !p.requirePageWriteFrom(w, targetSpace, userID, mmmodel.PermissionCreatePage, targetRead) {
		return
	}

	moved, appErr := p.service.MovePageToSpace(vars["page_id"], sourceSpace, targetSpace, req.ParentId, req.ExpectedUpdateAt, req.Force, userID, requiredOwnerID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, moved)
}
