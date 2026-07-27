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

// maxCapabilitiesBodyBytes caps the capability-set request bodies, which carry only tokens from a
// fixed five-value vocabulary — no content fields.
const maxCapabilitiesBodyBytes = 4 * 1024 // 4 KiB

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

	var req struct {
		Title               string    `json:"title"`
		Description         string    `json:"description,omitempty"`
		Icon                string    `json:"icon,omitempty"`
		DefaultCapabilities *[]string `json:"default_capabilities,omitempty"`
		ViewAccess          *string   `json:"view_access,omitempty"`
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
	created, appErr := p.service.CreateSpace(space, userID, req.DefaultCapabilities, req.ViewAccess)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleGetSpace handles GET /api/v1/spaces/{space_id}, returning the SpaceWithAccess wrapper
// carrying the space's default capability set and the caller's own effective capabilities.
func (p *Plugin) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	// BuildSpaceWithAccess resolves the read gate itself and returns the same existence-hiding 403
	// on a denial, so it is the gate here rather than a second resolution behind requireSpaceRead.
	space, ok := p.fetchSpaceForGate(w, spaceID, false)
	if !ok {
		return
	}
	wrapper, appErr := p.service.BuildSpaceWithAccess(space, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, wrapper)
}

// handleUpdateSpace handles PATCH /api/v1/spaces/{space_id}. Only the supplied mutable fields
// (title, description, icon, props, view_access) are applied onto the existing space; a supplied
// empty string clears a string field. The optimistic-lock baseline (expected_update_at) is
// required unless force is set. requireSpaceManageGate is the route floor; a ViewAccess change
// that requires the stricter admin gate is enforced inside UpdateSpace itself, against the live
// row, under the space's membership advisory lock.
func (p *Plugin) handleUpdateSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceManageGate(w, spaceID, userID)
	if !ok {
		return
	}

	var req struct {
		Title            *string                  `json:"title"`
		Description      *string                  `json:"description"`
		Icon             *string                  `json:"icon"`
		Props            *mmmodel.StringInterface `json:"props"`
		ViewAccess       *string                  `json:"view_access"`
		ExpectedUpdateAt *int64                   `json:"expected_update_at"`
		Force            bool                     `json:"force"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleUpdateSpace", false) {
		return
	}
	patch := &model.SpacePatch{Title: req.Title, Description: req.Description, Icon: req.Icon, Props: req.Props, ViewAccess: req.ViewAccess}
	updated, appErr := p.service.UpdateSpace(space, patch, req.ExpectedUpdateAt, req.Force, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteSpace handles DELETE /api/v1/spaces/{space_id}.
func (p *Plugin) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceDeleteGate(w, spaceID, userID, false)
	if !ok {
		return
	}
	if appErr := p.service.DeleteSpace(space); appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}

// handleRestoreSpace handles PATCH /api/v1/spaces/{space_id}/restore.
// includeDeleted=true is required here because the space is soft-deleted at the time of lookup.
func (p *Plugin) handleRestoreSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	if _, ok := p.requireSpaceDeleteGate(w, spaceID, userID, true); !ok {
		return
	}
	restored, appErr := p.service.RestoreSpace(spaceID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}

// handleGetSpacePages handles GET /api/v1/spaces/{space_id}/pages, returning metadata summaries.
func (p *Plugin) handleGetSpacePages(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpacePagePerm(w, spaceID, userID, mmmodel.PermissionReadPage)
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
	space, ok := p.requireSpaceManageGate(w, spaceID, userID)
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

// handleAddSpaceMember handles POST /api/v1/spaces/{space_id}/members. Adds the target at the
// space default only; granted_capabilities/capabilities in the body are rejected (400) rather
// than silently dropped, since a caller believing they restricted a new member's capabilities
// would otherwise be misled.
func (p *Plugin) handleAddSpaceMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceManageGate(w, spaceID, userID)
	if !ok {
		return
	}
	var req struct {
		UserID              string    `json:"user_id"`
		GrantedCapabilities *[]string `json:"granted_capabilities"`
		Capabilities        *[]string `json:"capabilities"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleAddSpaceMember", false) {
		return
	}
	if req.GrantedCapabilities != nil || req.Capabilities != nil {
		p.writeAppError(w, mmmodel.NewAppError("handleAddSpaceMember", "api.space.add_member.capabilities_not_allowed.app_error", nil, "", http.StatusBadRequest))
		return
	}
	member, appErr := p.service.AddSpaceMember(space, req.UserID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, member)
}

// handleSetSpaceMemberCapabilities handles PATCH /api/v1/spaces/{space_id}/members/{user_id}/capabilities.
func (p *Plugin) handleSetSpaceMemberCapabilities(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	targetUserID := vars["user_id"]
	space, ok := p.requireSpaceManageGate(w, spaceID, userID)
	if !ok {
		return
	}
	var req struct {
		GrantedCapabilities []string `json:"granted_capabilities"`
	}
	if !p.decodeJSONBody(w, r, maxCapabilitiesBodyBytes, &req, "handleSetSpaceMemberCapabilities", false) {
		return
	}
	member, appErr := p.service.SetSpaceMemberCapabilities(space, targetUserID, req.GrantedCapabilities, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

// handleSetSpaceDefaultCapabilities handles PATCH /api/v1/spaces/{space_id}/default-capabilities.
func (p *Plugin) handleSetSpaceDefaultCapabilities(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceAdminGate(w, spaceID, userID)
	if !ok {
		return
	}
	var req struct {
		DefaultCapabilities []string `json:"default_capabilities"`
	}
	if !p.decodeJSONBody(w, r, maxCapabilitiesBodyBytes, &req, "handleSetSpaceDefaultCapabilities", false) {
		return
	}
	updated, appErr := p.service.SetSpaceDefaultCapabilities(space, req.DefaultCapabilities, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleRemoveSpaceMember handles DELETE /api/v1/spaces/{space_id}/members/{user_id}. Self-removal
// is gated on the read resolver alone (any member may leave); removing another user requires
// requireSpaceManage, with the escalation/last-admin guards enforced inside the service.
func (p *Plugin) handleRemoveSpaceMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	targetUserID := vars["user_id"]

	var space *model.Space
	var ok bool
	if targetUserID == userID {
		space, _, ok = p.requireSpaceRead(w, spaceID, userID)
	} else {
		space, ok = p.requireSpaceManageGate(w, spaceID, userID)
	}
	if !ok {
		return
	}
	if appErr := p.service.RemoveSpaceMember(space, targetUserID, userID); appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeStatusOK(w)
}
