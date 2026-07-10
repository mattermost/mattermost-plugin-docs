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
// All space- and page-scoped handlers additionally gate on backing-channel membership via
// CheckSpaceMembership (implemented). Per-page role ACLs (author vs. editor within a space)
// are not yet implemented and are deferred to a follow-up.
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

// requireSpaceMembership calls CheckSpaceMembership and writes the error response when access is
// denied. Returns the fetched space and true on success so callers can reuse the already-loaded
// record (e.g. to extract ChannelId for WS events). includeDeleted must be true for restore
// operations where the space is soft-deleted at lookup time.
func (p *Plugin) requireSpaceMembership(w http.ResponseWriter, spaceID, userID string, includeDeleted bool) (*model.Space, bool) {
	space, appErr := p.service.CheckSpaceMembership(spaceID, userID, includeDeleted)
	if appErr != nil {
		writeAppError(w, appErr)
		return nil, false
	}
	return space, true
}

// EnableDocsRequired is a middleware that rejects all API requests with 501 Not Implemented when
// the EnableDocs feature flag is off.
func (p *Plugin) EnableDocsRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.API.GetConfig().FeatureFlags.EnableDocs {
			writeAppError(w, mmmodel.NewAppError("EnableDocsRequired", "api.docs_not_enabled.app_error", nil, "", http.StatusNotImplemented))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeAppError serialises a *mmmodel.AppError as a JSON body with its own StatusCode as the HTTP status.
// DetailedError is cleared before encoding so internal store/DB details are never sent to clients.
func writeAppError(w http.ResponseWriter, appErr *mmmodel.AppError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(appErr.StatusCode)
	safe := *appErr
	safe.DetailedError = ""
	// The status header is already written, so an encode failure here can't change the response,
	// and these free functions have no logger to report it to.
	_ = json.NewEncoder(w).Encode(&safe)
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

// writePaginatedJSON wraps items in a paginatedResponse and writes it as a 200 JSON body.
// Callers must pass perPage+1 items from the store (via paginationOffsetLimit) or the app layer
// accumulation logic: if len(items) > perPage the slice is trimmed to perPage and HasMore is true;
// otherwise HasMore is false, meaning the store was exhausted before the extra probe row arrived.
func writePaginatedJSON[T any](w http.ResponseWriter, items []T, page, perPage int) {
	if items == nil {
		items = []T{}
	}
	hasMore := len(items) > perPage
	if hasMore {
		items = items[:perPage]
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
	// Reject trailing data after the first JSON value (e.g. two concatenated objects). Reading the
	// trailing bytes can itself trip the body cap, which is a too-large body, not a malformed one.
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
			return min(p, pageMaximum)
		}
	}
	return 0
}

// perPageParam returns a per-page count from "per_page" (default app.PerPageDefault, clamped to
// [1, app.PerPageMaximum]). A non-positive or unparseable value yields the default.
func perPageParam(r *http.Request) int {
	n := 0
	if v := r.URL.Query().Get("per_page"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	return app.ClampPerPage(n)
}
