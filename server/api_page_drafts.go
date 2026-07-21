// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

const (
	// maxDraftBodyBytes caps the autosave request, which carries the full document body. Body is JSON
	// nested inside the request JSON, so its transport form can grow far beyond its decoded size once
	// quotes, backslashes, and control characters are escaped (worst case ~6x for all-control-char
	// input). Size the transport cap for that worst case plus headroom for the title/props/file-ids
	// and JSON envelope; the decoded body stays capped at model.PageBodyMaxBytes during normalization.
	draftBodyHeadroomBytes = 64 * 1024 // headroom for the title/props/file-ids and JSON envelope
	maxDraftBodyBytes      = 6*model.PageBodyMaxBytes + draftBodyHeadroomBytes
)

// handleUpdatePageDraft handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/draft
// It upserts the calling user's draft for the page. PATCH is deliberate: the request merges rather
// than replaces, so an omitted field means "unchanged", not "cleared" (autosave heartbeats carry
// only the fields the editor touched). parent_id: null omits the field; parent_id: "" explicitly
// clears it.
//
// For existing published pages, the first request creates the draft (open an edit session). The client
// must send the top-level base_edit_at field — the page's EditAt at the moment the user opened it — on
// every autosave, so a subsequent publish can detect a concurrent edit. It is not a props key.
//
// For new-page drafts (no page row yet), the draft must already exist via POST
// /spaces/{space_id}/drafts. This prevents a space member who learns another user's pending page
// ID from squatting on it via the autosave endpoint.
func (p *Plugin) handleUpdatePageDraft(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	pageID := vars["page_id"]
	userID := userIDFromRequest(r)

	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}

	var req struct {
		ParentId   *string                  `json:"parent_id"`
		Title      string                   `json:"title"`
		Body       string                   `json:"body"`
		FileIds    *mmmodel.StringArray     `json:"file_ids"`
		Props      *mmmodel.StringInterface `json:"props"`
		BaseEditAt *int64                   `json:"base_edit_at"`
	}
	if !p.decodeJSONBody(w, r, maxDraftBodyBytes, &req, "handleUpdatePageDraft", false) {
		return
	}

	// base_edit_at nil → 0 (no baseline: a new-page draft, or an existing-page edit that omitted it,
	// which fails closed to a forced publish). Props flows via the pointer below (like file_ids), so it
	// is not set on the struct here.
	var baseEditAt int64
	if req.BaseEditAt != nil {
		baseEditAt = *req.BaseEditAt
	}
	draft := &model.Draft{
		UserId:     userID,
		SpaceId:    spaceID,
		PageId:     pageID,
		Title:      req.Title,
		Body:       req.Body,
		BaseEditAt: baseEditAt,
	}

	// req.ParentId nil → preserve; pointer to "" → clear to root; pointer to ID → set parent.
	// req.FileIds nil → preserve; pointer to [] → clear; pointer to [...] → replace.
	// req.Props nil → preserve; non-nil → replace the whole map (an empty map clears all keys).
	saved, appErr := p.service.UpdatePageDraft(draft, req.ParentId, req.FileIds, req.Props, space.ChannelId)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	writeJSON(w, http.StatusOK, saved)
}

// handleGetPageDraft handles GET /api/v1/spaces/{space_id}/pages/{page_id}/draft
func (p *Plugin) handleGetPageDraft(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	pageID := vars["page_id"]
	userID := userIDFromRequest(r)

	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}

	draft, appErr := p.service.GetPageDraft(userID, spaceID, pageID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	writeJSON(w, http.StatusOK, draft)
}

// handleDeletePageDraft handles DELETE /api/v1/spaces/{space_id}/pages/{page_id}/draft
func (p *Plugin) handleDeletePageDraft(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	pageID := vars["page_id"]
	userID := userIDFromRequest(r)

	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}

	if appErr := p.service.DeletePageDraft(userID, spaceID, pageID, space.ChannelId); appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	writeStatusOK(w)
}

// handleCreateSpaceDraft handles POST /api/v1/spaces/{space_id}/drafts
// It creates a new-page draft (no Pages row) with a server-generated page id, reserving the id
// before the page is published so a new page has a stable link from the start.
func (p *Plugin) handleCreateSpaceDraft(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	userID := userIDFromRequest(r)

	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}

	var req struct {
		Title    string `json:"title"`
		ParentId string `json:"parent_id"`
	}
	if !p.decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handleCreateSpaceDraft", false) {
		return
	}

	saved, appErr := p.service.CreateSpaceDraft(userID, spaceID, req.Title, req.ParentId)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	writeJSON(w, http.StatusCreated, saved)
}

// handlePublishPageDraft handles POST /api/v1/spaces/{space_id}/pages/{page_id}/draft/publish
// It publishes the calling user's draft, atomically writing the page row and deleting the draft in
// one transaction.
//
// The optimistic-lock baseline for an edit-publish is not a field on this request: it travels with
// the draft, stored in its write-once BaseEditAt column (sent as the top-level base_edit_at field on
// the autosave requests). This differs from the per-request base_edit_at on handleUpdatePage (and
// expected_update_at on handleMovePage) because a publish ships whatever the draft already holds
// rather than re-supplying a freshly-read baseline.
func (p *Plugin) handlePublishPageDraft(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	pageID := vars["page_id"]
	userID := userIDFromRequest(r)

	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}

	// Optional body: {force: bool}. force=true overrides first-write-wins.
	var req struct {
		Force bool `json:"force"`
	}
	// allowEmptyBody=true: an absent body means force=false. A present-but-malformed body is still
	// an error, so the return value must be checked.
	if !p.decodeJSONBody(w, r, maxPageStructBodyBytes, &req, "handlePublishPageDraft", true) {
		return
	}

	page, wasCreated, appErr := p.service.PublishPageDraft(userID, spaceID, pageID, req.Force)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	status := http.StatusOK
	if wasCreated {
		status = http.StatusCreated
	}
	writeJSON(w, status, page)
}

// handleGetPageDraftsForSpace handles GET /api/v1/spaces/{space_id}/drafts
// It lists the calling user's unpublished page drafts for the space.
func (p *Plugin) handleGetPageDraftsForSpace(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	userID := userIDFromRequest(r)

	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}

	page, perPage := pageParam(r), perPageParam(r)
	drafts, hasMore, appErr := p.service.GetPageDraftsForSpace(userID, spaceID, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	writePaginatedJSON(w, drafts, page, perPage, hasMore)
}
