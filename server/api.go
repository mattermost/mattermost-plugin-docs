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
)

// initRouter initializes the HTTP router for the plugin. Routes are served under the plugin's
// /api/v1 prefix (full root: <siteUrl>/plugins/com.mattermost.docs/api/v1/).
//
// Authorization (interim): every route requires an authenticated user via
// MattermostAuthorizationRequired, but does NOT yet gate per space/page. Any logged-in user can
// reach any space — a known cross-space access hole closed once space membership and per-page
// restriction are layered onto these routes.
func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.MattermostAuthorizationRequired)

	api := router.PathPrefix("/api/v1").Subrouter()

	// Space listing entry point (team-rooted).
	api.HandleFunc("/teams/{team_id}/spaces", p.handleGetTeamSpaces).Methods(http.MethodGet)

	// Space CRUD.
	api.HandleFunc("/spaces", p.handleCreateSpace).Methods(http.MethodPost)
	api.HandleFunc("/spaces/{space_id}", p.handleGetSpace).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}", p.handleUpdateSpace).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}", p.handleDeleteSpace).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/restore", p.handleRestoreSpace).Methods(http.MethodPatch)

	// Page collection.
	api.HandleFunc("/spaces/{space_id}/pages", p.handleGetSpacePages).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages", p.handleCreatePage).Methods(http.MethodPost)

	// Page resource + tree actions.
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}", p.handleGetSpacePage).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}", p.handleUpdatePage).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}", p.handleDeletePage).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/restore", p.handleRestorePage).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/breadcrumb", p.handleGetPageBreadcrumb).Methods(http.MethodGet)
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

// writeAppError serialises a *mmmodel.AppError as a JSON body using the AppError's StatusCode as
// the HTTP status, mirroring how Mattermost core returns errors to clients.
func writeAppError(w http.ResponseWriter, appErr *mmmodel.AppError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(appErr.StatusCode)
	// The status header is already written, so an encode failure here can't change the response,
	// and these free functions have no logger to report it to.
	_ = json.NewEncoder(w).Encode(appErr)
}

// writeJSON serialises v as a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// See writeAppError: the status header is already committed, so an encode failure is
	// unactionable here.
	_ = json.NewEncoder(w).Encode(v)
}

// writeStatusOK writes the {"status":"OK"} 200 body returned by actions with no resource payload.
func writeStatusOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

// paginatedResponse wraps a page of list-endpoint results with pagination metadata, so a client can
// tell whether more results exist without guessing from array length.
type paginatedResponse[T any] struct {
	Items   []T  `json:"items"`
	Page    int  `json:"page"`
	PerPage int  `json:"per_page"`
	HasMore bool `json:"has_more"`
}

// writePaginatedJSON wraps items in a paginatedResponse and writes it as a 200 JSON body. HasMore is
// a "did this page come back full" heuristic, not an exact count: a result set that ends exactly on
// a perPage boundary reports has_more=true until the next page comes back empty.
func writePaginatedJSON[T any](w http.ResponseWriter, items []T, page, perPage int) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse[T]{
		Items:   items,
		Page:    page,
		PerPage: perPage,
		HasMore: len(items) >= perPage,
	})
}

// decodeJSONBody caps the request body at maxBytes and decodes it into v, distinguishing a
// too-large body from a malformed one, attributed to where; returns false when the body cannot be
// decoded. When allowEmptyBody is true, an empty body is valid (v is left at its zero value)
// instead of being rejected, for endpoints where every field is optional.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64, v any, where string, allowEmptyBody bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if allowEmptyBody && errors.Is(err, io.EOF) {
			return true
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAppError(w, mmmodel.NewAppError(where, "api.request_too_large.app_error", map[string]any{"MaxBytes": maxBytes}, "", http.StatusRequestEntityTooLarge))
			return false
		}
		writeAppError(w, mmmodel.NewAppError(where, "api.invalid_json.app_error", nil, "", http.StatusBadRequest))
		return false
	}
	if !decodedToEOF(w, dec, where, maxBytes) {
		return false
	}
	return true
}

// decodedToEOF reports whether dec holds exactly one JSON value: after the first value is decoded,
// the next token must be io.EOF. Trailing bytes (a second JSON document, or junk after the value)
// are rejected as invalid, attributed to where — unless the trailing read is itself what pushed the
// body over maxBytes, in which case it's reported as too large instead, mirroring decodeJSONBody's
// own MaxBytesError handling on the first Decode call. json.Decoder skips trailing whitespace, so a
// value followed only by whitespace still reads as EOF.
func decodedToEOF(w http.ResponseWriter, dec *json.Decoder, where string, maxBytes int64) bool {
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAppError(w, mmmodel.NewAppError(where, "api.request_too_large.app_error", map[string]any{"MaxBytes": maxBytes}, "", http.StatusRequestEntityTooLarge))
			return false
		}
		writeAppError(w, mmmodel.NewAppError(where, "api.invalid_json.app_error", nil, "", http.StatusBadRequest))
		return false
	}
	return true
}

// userIDFromRequest returns the authenticated user id injected by the platform.
func userIDFromRequest(r *http.Request) string {
	return r.Header.Get("Mattermost-User-ID")
}

// pageMaximum caps the "page" query param so page*perPage in paginationOffsetLimit cannot
// overflow into a negative offset.
const pageMaximum = 1 << 20

// pageParam returns a zero-based page index from the "page" query param (default 0, clamped to
// [0, pageMaximum]).
func pageParam(r *http.Request) int {
	if v := r.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 0 {
			if p > pageMaximum {
				return pageMaximum
			}
			return p
		}
	}
	return 0
}

// perPageParam returns a per-page count from "per_page" (default app.PerPageDefault, clamped to
// [1, app.PerPageMaximum]), matching core's PerPageDefault/PerPageMaximum. A non-positive or
// unparseable value yields the default; the foundation's perPage<=0 "return all" path is never
// exposed to HTTP clients.
func perPageParam(r *http.Request) int {
	pp := app.PerPageDefault
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pp = n
		}
	}
	if pp > app.PerPageMaximum {
		pp = app.PerPageMaximum
	}
	return pp
}

// int64OrZero dereferences p, or returns 0 if nil. Used for optimistic-lock baseline request
// fields (base_edit_at/expected_update_at), which are *int64 so an omitted field is
// distinguishable from an explicit 0 in the JSON body — matching the ParentId/SiblingIndex
// pointer convention used elsewhere in these request structs. 0 is a safe default either way:
// EditAt/UpdateAt are never legitimately 0 post-creation.
func int64OrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
