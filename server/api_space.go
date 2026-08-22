// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"maps"
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

const maxSpaceBodyBytes = 1 << 20 // 1 MiB

// maxPermissionsBodyBytes caps the permission-set request bodies, which carry only permission
// tokens from a small fixed vocabulary — no content fields.
const maxPermissionsBodyBytes = 4 * 1024 // 4 KiB

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
		Title              string            `json:"title"`
		Description        string            `json:"description,omitempty"`
		Icon               string            `json:"icon,omitempty"`
		DefaultPermissions *[]string         `json:"default_permissions,omitempty"`
		ViewAccess         *model.ViewAccess `json:"view_access,omitempty"`
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
	created, appErr := p.service.CreateSpace(space, userID, req.DefaultPermissions, req.ViewAccess)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleGetSpace handles GET /api/v1/spaces/{space_id}, returning the SpaceWithAccess wrapper
// carrying the space's default permission set and the caller's own effective permissions.
func (p *Plugin) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	// BuildSpaceWithAccess resolves the read gate itself and returns the same existence-hiding 403
	// on a denial, so it is the gate here rather than a second resolution behind requireSpaceRead.
	space, ok := p.getSpaceForGate(w, spaceID, false)
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
// required unless force is set. requireSpaceManage is the route floor; a ViewAccess change
// requires the stricter admin gate, enforced inside UpdateSpace against the live row.
func (p *Plugin) handleUpdateSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceManage(w, spaceID, userID)
	if !ok {
		return
	}

	var req struct {
		Title            *string                  `json:"title"`
		Description      *string                  `json:"description"`
		Icon             *string                  `json:"icon"`
		Props            *mmmodel.StringInterface `json:"props"`
		ViewAccess       *model.ViewAccess        `json:"view_access"`
		ExpectedUpdateAt *int64                   `json:"expected_update_at"`
		Force            bool                     `json:"force"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleUpdateSpace", false) {
		return
	}
	// Answered with the same SpaceWithAccess wrapper the create and single-read routes return.
	// Returning a bare space here would flatten to a body a client cannot tell apart from the
	// wrapper, so refreshing a cached record from this response would silently drop the permission
	// fields the other two routes supplied. Resolved BEFORE the mutation, on the pre-update space
	// already in hand from the gate, so a failure here aborts with nothing committed and a still-valid
	// baseline for the caller's retry — rather than leaving a fallible lookup after the commit that
	// could turn a successful write into a reported failure.
	preWrapper, wrapErr := p.service.BuildSpaceWithAccess(space, userID)
	if wrapErr != nil {
		p.writeAppError(w, wrapErr)
		return
	}

	patch := &model.SpacePatch{Title: req.Title, Description: req.Description, Icon: req.Icon, Props: req.Props, ViewAccess: req.ViewAccess}
	updated, appErr := p.service.UpdateSpace(space, patch, req.ExpectedUpdateAt, req.Force, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	// The permission fields resolved above still hold: this route's patch touches only
	// title/description/icon/props/view_access, never member roles or the scheme's default
	// permissions, and requireSpaceManage/UpdateSpace's stricter admin gate on a ViewAccess change
	// mean the caller already held whatever permissions they hold now before this call — so
	// carrying them over is exact, not an approximation, of a fresh post-commit resolution.
	wrapper := &model.SpaceWithAccess{
		Space:              *updated,
		DefaultPermissions: preWrapper.DefaultPermissions,
		Permissions:        preWrapper.Permissions,
	}
	wrapper.Props = maps.Clone(updated.Props)
	wrapper.EnsurePermissions()
	writeJSON(w, http.StatusOK, wrapper)
}

// handleDeleteSpace handles DELETE /api/v1/spaces/{space_id}.
func (p *Plugin) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceDelete(w, spaceID, userID, false)
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
	if _, ok := p.requireSpaceDelete(w, spaceID, userID, true); !ok {
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

// handleGetSpaceMembers handles GET /api/v1/spaces/{space_id}/members.
//
// Gated on read_page, the same permission the page listing takes, rather than on the manage tier
// the membership writes below take: the roster is what the space view renders member counts and
// avatars from, so gating the read on manage left every ordinary member looking at a space whose
// membership it could not see. Core takes the same position on the backing channel — a channel
// member may list its members and their roles.
func (p *Plugin) handleGetSpaceMembers(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpacePagePerm(w, spaceID, userID, mmmodel.PermissionReadPage)
	if !ok {
		return
	}
	// Resolved separately from the route gate, and a denial is not one: it selects the projection
	// rather than admitting the caller. Only a failure of the check itself aborts — reporting a
	// backend outage as "no manage tier" would silently redact a roster the caller may see in full.
	manageErr := p.service.RequireSpaceAdminOrTeamPerm("api.space.manage", space, userID, mmmodel.PermissionManageSpace)
	if manageErr != nil && manageErr.StatusCode != http.StatusForbidden {
		p.writeAppError(w, manageErr)
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
	members, hasMore, appErr := p.service.GetSpaceMembers(space, page, perPage, manageErr == nil)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, members, page, perPage, hasMore)
}

// handleAddSpaceMember handles POST /api/v1/spaces/{space_id}/members. Adds the target at the
// space default only; granted_permissions/permissions in the body are rejected (400) rather
// than silently dropped, so a caller cannot believe a new member's permissions were restricted
// when they were not.
func (p *Plugin) handleAddSpaceMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceManage(w, spaceID, userID)
	if !ok {
		return
	}
	var req struct {
		UserID             string    `json:"user_id"`
		GrantedPermissions *[]string `json:"granted_permissions"`
		Permissions        *[]string `json:"permissions"`
	}
	if !p.decodeJSONBody(w, r, maxSpaceBodyBytes, &req, "handleAddSpaceMember", false) {
		return
	}
	if req.GrantedPermissions != nil || req.Permissions != nil {
		p.writeAppError(w, mmmodel.NewAppError("handleAddSpaceMember", "api.space.add_member.permissions_not_allowed.app_error", nil, "", http.StatusBadRequest))
		return
	}
	member, appErr := p.service.AddSpaceMember(space, req.UserID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, member)
}

// handleSetSpaceMemberPermissions handles PUT /api/v1/spaces/{space_id}/members/{user_id}/permissions.
func (p *Plugin) handleSetSpaceMemberPermissions(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	targetUserID := vars["user_id"]
	space, ok := p.requireSpaceManage(w, spaceID, userID)
	if !ok {
		return
	}
	// A pointer, so an absent field is distinguishable from an explicit []. This endpoint replaces
	// the member's whole granted set, and a non-pointer slice would decode {}, null, and a
	// misspelled field name all to nil — silently clearing every grant the member holds. Only a
	// present [] clears.
	var req struct {
		GrantedPermissions *[]string `json:"granted_permissions"`
	}
	if !p.decodeJSONBody(w, r, maxPermissionsBodyBytes, &req, "handleSetSpaceMemberPermissions", false) {
		return
	}
	if req.GrantedPermissions == nil {
		p.writeAppError(w, mmmodel.NewAppError("handleSetSpaceMemberPermissions", "api.space.set_member_permissions.permissions_required.app_error", nil, "", http.StatusBadRequest))
		return
	}
	member, appErr := p.service.SetSpaceMemberPermissions(space, targetUserID, *req.GrantedPermissions, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

// handleSetSpaceDefaultPermissions handles PUT /api/v1/spaces/{space_id}/default-permissions.
func (p *Plugin) handleSetSpaceDefaultPermissions(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	spaceID := mux.Vars(r)["space_id"]
	space, ok := p.requireSpaceAdmin(w, spaceID, userID)
	if !ok {
		return
	}
	// A pointer, for the same reason as the member-permissions handler above: this repoints the
	// whole space, so decoding {}, null, or a misspelled field to nil would drop every space
	// default to read-only. Only a present [] does that deliberately.
	var req struct {
		DefaultPermissions *[]string `json:"default_permissions"`
	}
	if !p.decodeJSONBody(w, r, maxPermissionsBodyBytes, &req, "handleSetSpaceDefaultPermissions", false) {
		return
	}
	if req.DefaultPermissions == nil {
		p.writeAppError(w, mmmodel.NewAppError("handleSetSpaceDefaultPermissions", "api.space.set_default_permissions.permissions_required.app_error", nil, "", http.StatusBadRequest))
		return
	}
	updated, appErr := p.service.SetSpaceDefaultPermissions(space, *req.DefaultPermissions, userID)
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
		space, ok = p.requireSpaceManage(w, spaceID, userID)
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
