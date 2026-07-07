// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

const maxSpaceBodyBytes = 1 << 20 // 1 MiB

// handleGetTeamSpaces handles GET /api/v1/teams/{team_id}/spaces.
func (p *Plugin) handleGetTeamSpaces(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	teamID := mux.Vars(r)["team_id"]
	page, perPage := pageParam(r), perPageParam(r)
	spaces, appErr := p.service.GetSpacesForTeam(teamID, userID, page, perPage)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, spaces, page, perPage)
}

// handleCreateSpace handles POST /api/v1/teams/{team_id}/spaces. The app layer validates and stands
// up the backing channel; the handler only decodes and forwards the acting user.
func (p *Plugin) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	teamID := mux.Vars(r)["team_id"]

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Icon        string `json:"icon,omitempty"`
	}
	if !decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleCreateSpace", false) {
		return
	}

	// Decode only the client-settable fields; Id/CreateAt/DeleteAt/SortOrder/Props are server-owned
	// and must not be accepted from the request body (CreatorId/ChannelId are set by CreateSpace).
	space := &model.Space{
		TeamId:      teamID,
		Title:       req.Title,
		Description: req.Description,
		Icon:        req.Icon,
	}
	created, appErr := p.service.CreateSpace(space, userID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleGetSpace handles GET /api/v1/spaces/{space_id}.
func (p *Plugin) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	space, appErr := p.service.GetSpace(mux.Vars(r)["space_id"])
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, space)
}

// handleUpdateSpace handles PATCH /api/v1/spaces/{space_id}. Only the supplied mutable fields
// (title, description, icon) are applied onto the existing space; a supplied empty string clears the
// field. The update is optimistic-locked on the client-supplied expected_update_at unless force.
func (p *Plugin) handleUpdateSpace(w http.ResponseWriter, r *http.Request) {
	spaceID := mux.Vars(r)["space_id"]

	var req struct {
		Title            *string `json:"title,omitempty"`
		Description      *string `json:"description,omitempty"`
		Icon             *string `json:"icon,omitempty"`
		ExpectedUpdateAt *int64  `json:"expected_update_at,omitempty"`
		Force            bool    `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleUpdateSpace", false) {
		return
	}

	patch := &model.SpacePatch{Title: req.Title, Description: req.Description, Icon: req.Icon}
	updated, appErr := p.service.UpdateSpace(spaceID, patch, int64OrZero(req.ExpectedUpdateAt), req.Force)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteSpace handles DELETE /api/v1/spaces/{space_id}.
func (p *Plugin) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	if appErr := p.service.DeleteSpace(mux.Vars(r)["space_id"]); appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}

// handleRestoreSpace handles PATCH /api/v1/spaces/{space_id}/restore.
func (p *Plugin) handleRestoreSpace(w http.ResponseWriter, r *http.Request) {
	spaceID := mux.Vars(r)["space_id"]
	restored, appErr := p.service.RestoreSpace(spaceID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

// handleGetSpacePages handles GET /api/v1/spaces/{space_id}/pages.
func (p *Plugin) handleGetSpacePages(w http.ResponseWriter, r *http.Request) {
	spaceID := mux.Vars(r)["space_id"]
	page, perPage := pageParam(r), perPageParam(r)
	pages, appErr := p.service.GetSpacePages(spaceID, page, perPage)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, pages, page, perPage)
}
