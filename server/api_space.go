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
	spaces, hasMore, appErr := p.service.GetSpacesForTeam(teamID, userID, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, spaces, page, perPage, hasMore)
}

// handleCreateSpace handles POST /api/v1/teams/{team_id}/spaces. The app layer validates and stands
// up the backing channel; the handler only decodes and forwards the acting user.
func (p *Plugin) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	teamID := mux.Vars(r)["team_id"]
	auditRec := p.makeAuditRecord(r, auditEventCreateSpace, userID)
	defer p.client.Audit.Record(auditRec)

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Icon        string `json:"icon,omitempty"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleCreateSpace", false) {
		return
	}

	// Decode only the fields settable at creation; Id, timestamps, SortOrder, CreatorId, and
	// ChannelId are server-owned. Props is not settable here — it can be set after creation via
	// PATCH /spaces/{space_id}.
	space := &model.Space{
		TeamId:      teamID,
		Title:       req.Title,
		Description: req.Description,
		Icon:        req.Icon,
	}
	created, appErr := p.service.CreateSpace(space, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("space")
	auditRec.AddEventResultState(created)
	writeJSON(w, http.StatusCreated, created)
}

// handleGetSpace handles GET /api/v1/spaces/{space_id}.
func (p *Plugin) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, space)
}

// handleUpdateSpace handles PATCH /api/v1/spaces/{space_id}. Only the supplied mutable fields
// (title, description, icon, props) are applied onto the existing space; a supplied empty string
// clears the field. The optimistic-lock baseline (expected_update_at) is required unless force
// is set.
func (p *Plugin) handleUpdateSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	auditRec := p.makeAuditRecord(r, auditEventUpdateSpace, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}

	var req struct {
		Title            *string                  `json:"title"`
		Description      *string                  `json:"description"`
		Icon             *string                  `json:"icon"`
		Props            *mmmodel.StringInterface `json:"props"`
		ExpectedUpdateAt *int64                   `json:"expected_update_at"`
		Force            bool                     `json:"force"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleUpdateSpace", false) {
		return
	}
	patch := &model.SpacePatch{Title: req.Title, Description: req.Description, Icon: req.Icon, Props: req.Props}
	updated, appErr := p.service.UpdateSpace(space, patch, req.ExpectedUpdateAt, req.Force)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("space")
	auditRec.AddEventPriorState(space)
	auditRec.AddEventResultState(updated)
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteSpace handles DELETE /api/v1/spaces/{space_id}.
func (p *Plugin) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	auditRec := p.makeAuditRecord(r, auditEventDeleteSpace, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}
	if appErr := p.service.DeleteSpace(space); appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("space")
	auditRec.AddEventPriorState(space)
	writeStatusOK(w)
}

// handleRestoreSpace handles PATCH /api/v1/spaces/{space_id}/restore.
// includeDeleted=true is required here because the space is soft-deleted at the time of lookup.
func (p *Plugin) handleRestoreSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	auditRec := p.makeAuditRecord(r, auditEventRestoreSpace, userID)
	defer p.client.Audit.Record(auditRec)
	if _, ok := p.requireSpaceMembership(w, spaceID, userID, true); !ok {
		return
	}
	restored, appErr := p.service.RestoreSpace(spaceID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("space")
	auditRec.AddEventResultState(restored)
	writeJSON(w, http.StatusOK, restored)
}

// handleGetSpacePages handles GET /api/v1/spaces/{space_id}/pages, returning metadata summaries.
func (p *Plugin) handleGetSpacePages(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
	pages, hasMore, appErr := p.service.GetSpacePages(space, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, pages, page, perPage, hasMore)
}

// handleListSpaceMembers handles GET /api/v1/spaces/{space_id}/members.
func (p *Plugin) handleListSpaceMembers(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
	members, hasMore, appErr := p.service.ListSpaceMembers(space, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, members, page, perPage, hasMore)
}

// handleAddSpaceMember handles POST /api/v1/spaces/{space_id}/members. Any current space member
// may add another user to the space.
func (p *Plugin) handleAddSpaceMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	auditRec := p.makeAuditRecord(r, auditEventAddSpaceMember, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleAddSpaceMember", false) {
		return
	}
	mmmodel.AddEventParameterToAuditRec(auditRec, "user_id", req.UserID)
	member, appErr := p.service.AddSpaceMember(space, req.UserID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("space_member")
	writeJSON(w, http.StatusCreated, member)
}

// handleRemoveSpaceMember handles DELETE /api/v1/spaces/{space_id}/members/{user_id}. Any current
// space member may remove another user from the space, except the last remaining member (409).
func (p *Plugin) handleRemoveSpaceMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	targetUserID := vars["user_id"]
	auditRec := p.makeAuditRecord(r, auditEventRemoveSpaceMember, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, spaceID, userID, false)
	if !ok {
		return
	}
	if appErr := p.service.RemoveSpaceMember(space, targetUserID); appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("space_member")
	writeStatusOK(w)
}
