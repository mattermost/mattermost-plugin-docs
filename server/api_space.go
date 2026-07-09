// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

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

	// Decode only the client-settable fields; Id, timestamps, SortOrder, Props, CreatorId, and ChannelId are server-owned.
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
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, appErr := p.service.GetSpaceForUser(spaceID, userID)
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
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}

	var req struct {
		Title            *string                  `json:"title,omitempty"`
		Description      *string                  `json:"description,omitempty"`
		Icon             *string                  `json:"icon,omitempty"`
		Props            *mmmodel.StringInterface `json:"props,omitempty"`
		ExpectedUpdateAt *int64                   `json:"expected_update_at,omitempty"`
		Force            bool                     `json:"force,omitempty"`
	}
	if !decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleUpdateSpace", false) {
		return
	}

	patch := &model.SpacePatch{Title: req.Title, Description: req.Description, Icon: req.Icon, Props: req.Props}
	updated, appErr := p.service.UpdateSpace(spaceID, patch, mmmodel.SafeDereference(req.ExpectedUpdateAt), req.Force)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteSpace handles DELETE /api/v1/spaces/{space_id}.
func (p *Plugin) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}
	if appErr := p.service.DeleteSpace(spaceID); appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}

// handleRestoreSpace handles PATCH /api/v1/spaces/{space_id}/restore.
// includeDeleted=true is required here because the space is soft-deleted at the time of lookup.
func (p *Plugin) handleRestoreSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	if _, ok := p.requireSpaceMembership(w, spaceID, userID, true); !ok {
		return
	}
	restored, appErr := p.service.RestoreSpace(spaceID)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

// handleGetSpacePages handles GET /api/v1/spaces/{space_id}/pages.
func (p *Plugin) handleGetSpacePages(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
	pages, appErr := p.service.GetSpacePages(spaceID, page, perPage)
	if appErr != nil {
		writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, pages, page, perPage)
}
