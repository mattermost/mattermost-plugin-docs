// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// initRouter initializes the HTTP router for the plugin. Routes are served under the plugin's
// /api/v1 prefix (full root: <siteUrl>/plugins/com.mattermost.docs/api/v1/).
//
// Authorization: every route requires an authenticated user via MattermostAuthorizationRequired.
// Every space- and page-scoped handler additionally gates on the capability-based RBAC model:
// requireSpaceRead/requireSpacePagePerm for reads, gatePageWrite/gateDeleteOwnOrAny for page
// writes (with the open-space auto-join pre-step), requireSpaceManageGate for membership
// management and general space-field updates (where a patch touching ViewAccess is additionally
// admin-gated inside UpdateSpace, against the live row), and requireSpaceAdminGate/
// requireSpaceDeleteGate for the space-wide exposure-policy and delete/restore operations. See
// server/app/permissions.go for the gate implementations.
func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.MattermostAuthorizationRequired)
	router.Use(p.EnableDocsRequired)

	api := router.PathPrefix("/api/v1").Subrouter()

	api.HandleFunc("/teams/{team_id}/spaces", p.handleGetTeamSpaces).Methods(http.MethodGet)
	api.HandleFunc("/teams/{team_id}/spaces", p.handleCreateSpace).Methods(http.MethodPost)

	// Space CRUD.
	api.HandleFunc("/spaces/{space_id}", p.handleGetSpace).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}", p.handleUpdateSpace).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}", p.handleDeleteSpace).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/restore", p.handleRestoreSpace).Methods(http.MethodPatch)

	// Space membership.
	api.HandleFunc("/spaces/{space_id}/members", p.handleListSpaceMembers).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/members", p.handleAddSpaceMember).Methods(http.MethodPost)
	api.HandleFunc("/spaces/{space_id}/members/{user_id}", p.handleRemoveSpaceMember).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/members/{user_id}/capabilities", p.handleSetSpaceMemberCapabilities).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/default-capabilities", p.handleSetSpaceDefaultCapabilities).Methods(http.MethodPatch)

	// Page collection.
	api.HandleFunc("/spaces/{space_id}/pages", p.handleGetSpacePages).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages", p.handleCreatePage).Methods(http.MethodPost)

	// Page resource + tree actions.
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}", p.handleGetPage).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}", p.handleUpdatePage).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}", p.handleDeletePage).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/restore", p.handleRestorePage).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/children", p.handleGetPageChildren).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/move", p.handleMovePage).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/move-to-space", p.handleMovePageToSpace).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/duplicate", p.handleDuplicatePage).Methods(http.MethodPost)

	return router
}

// ServeHTTP routes plugin HTTP requests. The root URL is
// <siteUrl>/plugins/com.mattermost.docs/api/v1/.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w, r)
}

// MattermostAuthorizationRequired is a middleware that rejects unauthenticated
// requests. It reads the Mattermost-User-ID header set by the platform and
// rejects the request as unauthorized if the header is absent.
func (p *Plugin) MattermostAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("Mattermost-User-ID")
		if userID == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// fetchSpaceForGate fetches spaceID (with soft-deleted rows included when includeDeleted is
// true) and maps a not-found lookup to the shared existence-hiding 403, so no enforcement helper
// ever needs to special-case a missing space differently from a denied one. Writes the error
// response and returns ok=false on failure.
func (p *Plugin) fetchSpaceForGate(w http.ResponseWriter, spaceID string, includeDeleted bool) (*model.Space, bool) {
	var space *model.Space
	var appErr *mmmodel.AppError
	if includeDeleted {
		space, appErr = p.service.GetSpaceWithDeleted(spaceID)
	} else {
		space, appErr = p.service.GetSpace(spaceID)
	}
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			p.writeAppError(w, mmmodel.NewAppError("fetchSpaceForGate", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden).Wrap(appErr))
			return nil, false
		}
		p.writeAppError(w, appErr)
		return nil, false
	}
	return space, true
}

// requireSpaceGate fetches spaceID and applies gate to it, writing the error response and
// returning ok=false on either a failed fetch or a denied gate. Every space-scoped route gate
// below is this shape — only the enforcement helper and its operation label differ.
func (p *Plugin) requireSpaceGate(w http.ResponseWriter, spaceID string, includeDeleted bool, gate func(space *model.Space) *mmmodel.AppError) (*model.Space, bool) {
	space, ok := p.fetchSpaceForGate(w, spaceID, includeDeleted)
	if !ok {
		return nil, false
	}
	if appErr := gate(space); appErr != nil {
		p.writeAppError(w, appErr)
		return nil, false
	}
	return space, true
}

// requireSpaceRead gates a route on the space read resolver (member, or the non-member open-space
// team fall-through) — the gate for every page read. It reports how the read was admitted so a
// caller that goes on to evaluate a further permission on the same space can pass the resolution
// down instead of re-deriving the team membership behind it.
func (p *Plugin) requireSpaceRead(w http.ResponseWriter, spaceID, userID string) (*model.Space, app.ReadResolution, bool) {
	space, ok := p.fetchSpaceForGate(w, spaceID, false)
	if !ok {
		return nil, app.ReadDenied, false
	}
	resolution, ok := p.resolveSpaceReadOrDeny(w, "api.space.read", space, userID)
	if !ok {
		return nil, app.ReadDenied, false
	}
	return space, resolution, true
}

// requireSpacePagePerm gates a route on a single page-scoped permission (read-only; write
// permissions go through gatePageWrite so the auto-join pre-step can run first).
func (p *Plugin) requireSpacePagePerm(w http.ResponseWriter, spaceID, userID string, perm *mmmodel.Permission) (*model.Space, bool) {
	return p.requireSpaceGate(w, spaceID, false, func(space *model.Space) *mmmodel.AppError {
		return p.service.RequireSpacePagePermission("api.space.page", space, userID, perm)
	})
}

// requireSpaceManageGate gates a route on requireSpaceManage: sysadmin, channel admin_space, or
// (once the read resolver has already admitted the caller) team manage_space.
func (p *Plugin) requireSpaceManageGate(w http.ResponseWriter, spaceID, userID string) (*model.Space, bool) {
	return p.requireSpaceGate(w, spaceID, false, func(space *model.Space) *mmmodel.AppError {
		return p.service.RequireSpaceManage("api.space.manage", space, userID)
	})
}

// requireSpaceAdminGate gates a route on requireSpaceAdminOrSysadmin — the space-wide
// exposure-policy knobs (ViewAccess, default capabilities).
func (p *Plugin) requireSpaceAdminGate(w http.ResponseWriter, spaceID, userID string) (*model.Space, bool) {
	return p.requireSpaceGate(w, spaceID, false, func(space *model.Space) *mmmodel.AppError {
		return p.service.RequireSpaceAdminOrSysadmin("api.space.admin", space, userID)
	})
}

// requireSpaceDeleteGate gates space delete/restore: sysadmin, channel admin_space, or (once the
// read resolver has already admitted the caller) team delete_space. includeDeleted must be true
// for restore, where the space is soft-deleted at lookup time; the read resolver and the delete
// gate then evaluate against that soft-deleted record.
func (p *Plugin) requireSpaceDeleteGate(w http.ResponseWriter, spaceID, userID string, includeDeleted bool) (*model.Space, bool) {
	return p.requireSpaceGate(w, spaceID, includeDeleted, func(space *model.Space) *mmmodel.AppError {
		return p.service.RequireSpaceDeleteAuthority("api.space.delete", space, userID)
	})
}

// gatePageWrite resolves the read gate first — a caller cannot be granted write authority over a
// space it cannot read — then runs the auto-join pre-step when that read was admitted only via the
// non-member open-space fall-through, then re-resolves perm as a (possibly just-joined) member.
// ownerCheck, when non-nil, additionally must hold before a join happens (used for
// delete_own_page). Writes the error response and returns false on any denial.
func (p *Plugin) gatePageWrite(w http.ResponseWriter, space *model.Space, userID string, perm *mmmodel.Permission, ownerCheck func() (bool, error)) bool {
	resolution, ok := p.resolveSpaceReadOrDeny(w, "gatePageWrite", space, userID)
	if !ok {
		return false
	}
	if _, appErr := p.service.AutoJoinIfDefaultGranted(space, userID, resolution, perm, ownerCheck); appErr != nil {
		p.writeAppError(w, appErr)
		return false
	}
	if appErr := p.service.RequireSpacePagePermissionFrom("api.page.write", space, userID, perm, resolution); appErr != nil {
		p.writeAppError(w, appErr)
		return false
	}
	return true
}

// gateDeleteOwnOrAny gates a delete-class page operation: delete_page (any), or delete_own_page
// when ownerID == userID. The auto-join pre-step runs against delete_own_page, gated on
// ownership, since only that path can admit a non-member write.
func (p *Plugin) gateDeleteOwnOrAny(w http.ResponseWriter, space *model.Space, userID, ownerID string) bool {
	resolution, ok := p.resolveSpaceReadOrDeny(w, "gateDeleteOwnOrAny", space, userID)
	if !ok {
		return false
	}
	if _, appErr := p.service.AutoJoinIfDefaultGranted(space, userID, resolution, mmmodel.PermissionDeleteOwnPage, func() (bool, error) { return ownerID == userID, nil }); appErr != nil {
		p.writeAppError(w, appErr)
		return false
	}
	_, ok, permErr := p.resolveOwnOrAny(space, userID, "api.page.delete", mmmodel.PermissionDeletePage, "api.page.delete_own", mmmodel.PermissionDeleteOwnPage, ownerID == userID, resolution)
	if permErr != nil {
		p.writeAppError(w, permErr)
		return false
	}
	if !ok {
		p.writeAppError(w, mmmodel.NewAppError("gateDeleteOwnOrAny", "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden))
		return false
	}
	return true
}

// resolveSpaceReadOrDeny resolves the read gate that precedes every page read and page-write gate,
// mapping a denied read to the shared existence-hiding 403. Writes the error response and returns
// ok=false on failure.
func (p *Plugin) resolveSpaceReadOrDeny(w http.ResponseWriter, where string, space *model.Space, userID string) (app.ReadResolution, bool) {
	resolution, resErr := p.service.ResolveSpaceRead(where, space, userID)
	if resErr != nil {
		p.writeAppError(w, resErr)
		return app.ReadDenied, false
	}
	if resolution == app.ReadDenied {
		p.writeAppError(w, mmmodel.NewAppError(where, "app.space.access.forbidden.app_error", nil, "", http.StatusForbidden))
		return app.ReadDenied, false
	}
	return resolution, true
}

// resolveOwnOrAny evaluates a two-tier own/any permission pair: anyPerm if held, else ownPerm
// when ownerMatches. Reports whether the caller qualified only via ownPerm (ownOnly), so a caller
// that must push ownership enforcement further down — MovePageToSpace's subtree-wide check — can
// tell the two tiers apart. ok=false with a nil appErr means neither tier admitted the caller; the
// caller writes its own denial so the operation label stays its own. A non-nil appErr is a genuine
// backend failure from the check itself, which the caller must surface as-is rather than reporting
// as a denial.
func (p *Plugin) resolveOwnOrAny(space *model.Space, userID, anyWhere string, anyPerm *mmmodel.Permission, ownWhere string, ownPerm *mmmodel.Permission, ownerMatches bool, admittedVia app.ReadResolution) (ownOnly, ok bool, appErr *mmmodel.AppError) {
	anyErr := p.service.RequireSpacePagePermissionFrom(anyWhere, space, userID, anyPerm, admittedVia)
	if anyErr == nil {
		return false, true, nil
	}
	if anyErr.StatusCode != http.StatusForbidden {
		return false, false, anyErr
	}
	if !ownerMatches {
		return false, false, nil
	}
	ownErr := p.service.RequireSpacePagePermissionFrom(ownWhere, space, userID, ownPerm, admittedVia)
	if ownErr == nil {
		return true, true, nil
	}
	if ownErr.StatusCode != http.StatusForbidden {
		return false, false, ownErr
	}
	return false, false, nil
}

// EnableDocsRequired is a middleware that rejects all API requests with 501 Not Implemented when
// the EnableDocs feature flag is off.
func (p *Plugin) EnableDocsRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.enableDocs.Load() {
			p.writeAppError(w, mmmodel.NewAppError("EnableDocsRequired", "api.docs_not_enabled.app_error", nil, "", http.StatusNotImplemented))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeAppError serialises a *mmmodel.AppError as a JSON body with its own StatusCode as the HTTP status.
// DetailedError is cleared before encoding so internal store/DB details are never sent to clients;
// 500-class errors are logged (with their wrapped cause) before that scrub, since the response is
// the error's only other trace.
func (p *Plugin) writeAppError(w http.ResponseWriter, appErr *mmmodel.AppError) {
	if appErr.StatusCode >= http.StatusInternalServerError {
		p.API.LogError("Docs API request failed", "where", appErr.Where, "id", appErr.Id, "status_code", appErr.StatusCode, "err", appErr.Error())
	}
	safe := *appErr
	safe.WipeDetailed()
	writeJSON(w, appErr.StatusCode, &safe)
}

// writeJSON serialises v as a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status header is already committed, so an encode failure is unactionable here.
	_ = json.NewEncoder(w).Encode(v)
}

// writeStatusOK writes the {"status":"OK"} 200 body returned by actions with no resource payload.
func writeStatusOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]string{"status": mmmodel.StatusOk})
}

// paginatedResponse wraps a page of list-endpoint results with pagination metadata, so a client can
// tell whether more results exist without guessing from array length.
type paginatedResponse[T any] struct {
	Items   []T  `json:"items"`
	Page    int  `json:"page"`
	PerPage int  `json:"per_page"`
	HasMore bool `json:"has_more"`
}

// writePaginatedJSON wraps items (exactly the page to render) in a paginatedResponse and writes
// it as a 200 JSON body. hasMore comes from the app layer alongside the items.
func writePaginatedJSON[T any](w http.ResponseWriter, items []T, page, perPage int, hasMore bool) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse[T]{
		Items:   items,
		Page:    page,
		PerPage: perPage,
		HasMore: hasMore,
	})
}

// decodeJSONBody caps the request body at maxBytes and decodes it into v, distinguishing a
// too-large body from a malformed one, attributed to where; returns false when the body cannot be
// decoded. When allowEmptyBody is true, an empty body is valid (v is left at its zero value)
// instead of being rejected, for endpoints where every field is optional.
func (p *Plugin) decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64, v any, where string, allowEmptyBody bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	writeDecodeError := func(err error) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			p.writeAppError(w, mmmodel.NewAppError(where, "api.request_too_large.app_error", map[string]any{"MaxBytes": maxBytes}, "", http.StatusRequestEntityTooLarge).Wrap(err))
			return
		}
		p.writeAppError(w, mmmodel.NewAppError(where, "api.invalid_json.app_error", nil, "", http.StatusBadRequest).Wrap(err))
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if allowEmptyBody && errors.Is(err, io.EOF) {
			return true
		}
		writeDecodeError(err)
		return false
	}
	// Reject trailing data after the first JSON value (e.g. two concatenated objects). Reading the
	// trailing bytes can itself trip the body cap, which is a too-large body, not a malformed one.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeDecodeError(err)
		return false
	}
	return true
}

// userIDFromRequest returns the authenticated user id injected by the platform.
func userIDFromRequest(r *http.Request) string {
	return r.Header.Get("Mattermost-User-ID")
}

// intQueryParam parses an integer query param, yielding 0 when it is absent or unparseable.
func intQueryParam(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(key))
	return n
}

// pageParam returns a zero-based page index from the "page" query param (default 0, clamped to
// [0, app.PageMaximum]).
func pageParam(r *http.Request) int {
	return app.ClampPage(intQueryParam(r, "page"))
}

// perPageParam returns a per-page count from "per_page" (default app.PerPageDefault, clamped to
// [1, app.PerPageMaximum]). A non-positive or unparseable value yields the default.
func perPageParam(r *http.Request) int {
	return app.ClampPerPage(intQueryParam(r, "per_page"))
}
