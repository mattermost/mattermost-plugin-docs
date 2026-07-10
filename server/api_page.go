// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// maxPageBodyBytes caps the raw request body for page create/update, sized with headroom over the
// model's Body and SearchText limits so ordinary payloads reach model validation. A body whose JSON
// encoding is dominated by escape sequences can still exceed this and be rejected with 413 rather
// than the model's 400; that is an intentional transport-level guard.
const maxPageBodyBytes = 8 << 20 // 8 MiB

// WebSocket event names published after structural page-tree mutations so clients on all cluster
// nodes can refresh the affected tree without a full reload.
const (
	wsEventPageMoved        = "docs_page_moved"
	wsEventPageDuplicated   = "docs_page_duplicated"
	wsEventPageMovedToSpace = "docs_page_moved_to_space"
)

// publishToChannels publishes a WebSocket event broadcast to each non-empty, distinct channel ID.
// WS events are best-effort and must not fail the primary mutation response.
func (p *Plugin) publishToChannels(event string, payload map[string]any, channelIDs ...string) {
	seen := make(map[string]bool, len(channelIDs))
	for _, chID := range channelIDs {
		if chID == "" || seen[chID] {
			continue
		}
		seen[chID] = true
		p.API.PublishWebSocketEvent(event, payload, &mmmodel.WebsocketBroadcast{ChannelId: chID})
	}
}

// spaceChannelID returns the backing channel ID of the space, or "" when s is nil (system caller path).
func spaceChannelID(s *model.Space) string {
	if s == nil {
		return ""
	}
	return s.ChannelId
}

// maxPageStructBodyBytes caps request bodies for move/duplicate/move-to-space, which carry only
// IDs, booleans, and timestamps — no content fields.
const maxPageStructBodyBytes = 4 * 1024 // 4 KiB

// handleCreatePage handles POST /api/v1/spaces/{space_id}/pages.
func (p *Plugin) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false); !ok {
		return
	}

	var req struct {
		Title      string `json:"title"`
		ParentId   string `json:"parent_id,omitempty"`
		Body       string `json:"body,omitempty"`
		SearchText string `json:"search_text,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleCreatePage", false) {
		return
	}

	page, appErr := p.service.CreatePage(vars["space_id"], req.ParentId, req.Title, req.Body, req.SearchText, userID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, page)
}

// handleGetPage handles GET /api/v1/spaces/{space_id}/pages/{page_id}.
func (p *Plugin) handleGetPage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false); !ok {
		return
	}
	page, appErr := p.service.GetPageInSpace("handleGetPage", vars["page_id"], vars["space_id"], false)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleUpdatePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}. The optimistic-lock
// baseline (base_edit_at) is carried in the body; a stale value yields 409 unless force is set.
func (p *Plugin) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false); !ok {
		return
	}

	var req struct {
		Title      *string                  `json:"title,omitempty"`
		Body       *string                  `json:"body,omitempty"`
		SearchText *string                  `json:"search_text,omitempty"`
		Props      *mmmodel.StringInterface `json:"props,omitempty"`
		BaseEditAt *int64                   `json:"base_edit_at,omitempty"`
		Force      bool                     `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageBodyBytes, &req, "handleUpdatePage", false) {
		return
	}

	patch := &model.PagePatch{Title: req.Title, Body: req.Body, SearchText: req.SearchText, Props: req.Props}

	updated, appErr := p.service.UpdatePage(vars["page_id"], vars["space_id"], patch, mmmodel.SafeDereference(req.BaseEditAt), req.Force, userID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeletePage handles DELETE /api/v1/spaces/{space_id}/pages/{page_id}.
func (p *Plugin) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false); !ok {
		return
	}
	if appErr := p.service.DeletePage(vars["page_id"], vars["space_id"], userID); appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}

// handleRestorePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/restore.
func (p *Plugin) handleRestorePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false); !ok {
		return
	}
	restored, appErr := p.service.RestorePage(vars["page_id"], vars["space_id"], userID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

// handleMovePage handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/move. It reparents the page
// (parent_id nil leaves the parent unchanged; "" moves to the space root) and positions it at
// sibling_index within the destination sibling group (clamped to the group's bounds rather than
// rejected if out of range), optimistic-locked on expected_update_at unless force.
func (p *Plugin) handleMovePage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}

	var req struct {
		ParentId         *string `json:"parent_id,omitempty"`
		SiblingIndex     *int64  `json:"sibling_index,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleMovePage", false) {
		return
	}
	if req.ParentId != nil && *req.ParentId != "" && !mmmodel.IsValidId(*req.ParentId) {
		writeAppError(w, mmmodel.NewAppError("handleMovePage", "api.page.move.invalid_parent_id.app_error", nil, "", http.StatusBadRequest))
		return
	}

	moved, didMove, appErr := p.service.MovePage(vars["page_id"], vars["space_id"], req.ParentId, req.SiblingIndex, mmmodel.SafeDereference(req.ExpectedUpdateAt), req.Force)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	if didMove {
		p.publishToChannels(wsEventPageMoved, map[string]any{"page_id": moved.Id, "space_id": vars["space_id"]}, spaceChannelID(space))
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
	sourceSpace, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}

	var req struct {
		IncludeChildren bool    `json:"include_children,omitempty"`
		TargetSpaceId   string  `json:"target_space_id,omitempty"`
		ParentId        *string `json:"parent_id,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleDuplicatePage", true) {
		return
	}
	if req.ParentId != nil && *req.ParentId != "" && !mmmodel.IsValidId(*req.ParentId) {
		writeAppError(w, mmmodel.NewAppError("handleDuplicatePage", "api.page.duplicate.invalid_parent_id.app_error", nil, "", http.StatusBadRequest))
		return
	}
	// Target space ID comes from the request body, not the URL, so it cannot be covered by a
	// subrouter middleware and must be checked inline.
	crossSpace := req.TargetSpaceId != "" && req.TargetSpaceId != vars["space_id"]
	var targetSpace *model.Space
	if crossSpace {
		if !mmmodel.IsValidId(req.TargetSpaceId) {
			writeAppError(w, mmmodel.NewAppError("handleDuplicatePage", "api.page.duplicate.invalid_target_space_id.app_error", nil, "", http.StatusBadRequest))
			return
		}
		targetSpace, ok = p.requireSpaceMembership(w, req.TargetSpaceId, userID, false)
		if !ok {
			return
		}
	}

	duplicated, appErr := p.service.DuplicatePage(vars["page_id"], vars["space_id"], userID, req.IncludeChildren, req.TargetSpaceId, req.ParentId)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	publishSpaceID, publishSpace := vars["space_id"], sourceSpace
	if crossSpace {
		publishSpaceID, publishSpace = req.TargetSpaceId, targetSpace
	}
	p.publishToChannels(wsEventPageDuplicated, map[string]any{"page_id": duplicated.Id, "space_id": publishSpaceID}, spaceChannelID(publishSpace))
	writeJSON(w, http.StatusCreated, duplicated)
}

// handleGetPageChildren handles GET /api/v1/spaces/{space_id}/pages/{page_id}/children, returning
// the page's direct live children (paginated).
func (p *Plugin) handleGetPageChildren(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	if _, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false); !ok {
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
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
	userID := userIDFromRequest(r)
	sourceSpace, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}

	var req struct {
		TargetSpaceId    string  `json:"target_space_id"`
		ParentId         *string `json:"parent_id,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleMovePageToSpace", false) {
		return
	}
	if req.TargetSpaceId == "" {
		writeAppError(w, mmmodel.NewAppError("handleMovePageToSpace", "api.page.move_to_space.missing_target_space_id.app_error", nil, "", http.StatusBadRequest))
		return
	}
	if !mmmodel.IsValidId(req.TargetSpaceId) {
		writeAppError(w, mmmodel.NewAppError("handleMovePageToSpace", "api.page.move_to_space.invalid_target_space_id.app_error", nil, "", http.StatusBadRequest))
		return
	}
	if req.ParentId != nil && *req.ParentId != "" && !mmmodel.IsValidId(*req.ParentId) {
		writeAppError(w, mmmodel.NewAppError("handleMovePageToSpace", "api.page.move_to_space.invalid_parent_id.app_error", nil, "", http.StatusBadRequest))
		return
	}
	// Target space ID comes from the request body, not the URL, so it cannot be covered by a
	// subrouter middleware and must be checked inline.
	targetSpace, ok := p.requireSpaceMembership(w, req.TargetSpaceId, userID, false)
	if !ok {
		return
	}

	moved, didMove, appErr := p.service.MovePageToSpace(vars["page_id"], vars["space_id"], req.TargetSpaceId, req.ParentId, mmmodel.SafeDereference(req.ExpectedUpdateAt), req.Force)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	if didMove {
		p.publishToChannels(wsEventPageMovedToSpace, map[string]any{"page_id": moved.Id, "source_space_id": vars["space_id"], "target_space_id": req.TargetSpaceId}, spaceChannelID(sourceSpace), spaceChannelID(targetSpace))
	}
	writeJSON(w, http.StatusOK, moved)
}
