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
// CheckSpaceMembership. Per-page role ACLs (author vs. editor within a space) are not yet
// implemented.
func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.MattermostAuthorizationRequired)
	router.Use(p.EnableDocsRequired)

	api := router.PathPrefix("/api/v1").Subrouter()

	api.HandleFunc("/teams/{team_id}/spaces", p.handleGetTeamSpaces).Methods(http.MethodGet)
	api.HandleFunc("/teams/{team_id}/spaces", p.handleCreateSpace).Methods(http.MethodPost)

	// Confluence bundle import. Job visibility is actor-only: another user's job reads as 404, not
	// 403, so these routes cannot be used to probe for someone else's import.
	api.HandleFunc("/imports/preflight", p.handleCreateImport).Methods(http.MethodPost)
	api.HandleFunc("/imports", p.handleListImports).Methods(http.MethodGet)
	api.HandleFunc("/imports/{job_id}", p.handleGetImport).Methods(http.MethodGet)
	api.HandleFunc("/imports/{job_id}/issues", p.handleGetImportIssues).Methods(http.MethodGet)
	api.HandleFunc("/imports/{job_id}/preflight-results", p.handleGetImportPreflightResults).Methods(http.MethodGet)
	api.HandleFunc("/imports/{job_id}/report", p.handleGetImportReport).Methods(http.MethodGet)
	api.HandleFunc("/imports/{job_id}/source", p.handleSelectImportSource).Methods(http.MethodPost)
	api.HandleFunc("/imports/{job_id}/confirm", p.handleConfirmImport).Methods(http.MethodPost)
	api.HandleFunc("/imports/{job_id}/cancel", p.handleCancelImport).Methods(http.MethodPost)

	// Space CRUD.
	api.HandleFunc("/spaces/{space_id}", p.handleGetSpace).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}", p.handleUpdateSpace).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}", p.handleDeleteSpace).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/restore", p.handleRestoreSpace).Methods(http.MethodPatch)

	// Space membership.
	api.HandleFunc("/spaces/{space_id}/members", p.handleListSpaceMembers).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/members", p.handleAddSpaceMember).Methods(http.MethodPost)
	api.HandleFunc("/spaces/{space_id}/members/{user_id}", p.handleRemoveSpaceMember).Methods(http.MethodDelete)

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

	// Draft CRUD + publish.
	api.HandleFunc("/spaces/{space_id}/drafts", p.handleCreateSpaceDraft).Methods(http.MethodPost)
	api.HandleFunc("/spaces/{space_id}/drafts", p.handleGetPageDraftsForSpace).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/draft", p.handleUpdatePageDraft).Methods(http.MethodPatch)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/draft", p.handleGetPageDraft).Methods(http.MethodGet)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/draft", p.handleDeletePageDraft).Methods(http.MethodDelete)
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/draft/publish", p.handlePublishPageDraft).Methods(http.MethodPost)

	// Presence.
	api.HandleFunc("/spaces/{space_id}/pages/{page_id}/active-editors", p.handleGetPageActiveEditors).Methods(http.MethodGet)

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
// record instead of re-fetching it. includeDeleted must be true for restore
// operations where the space is soft-deleted at lookup time.
func (p *Plugin) requireSpaceMembership(w http.ResponseWriter, spaceID, userID string, includeDeleted bool) (*model.Space, bool) {
	space, appErr := p.service.CheckSpaceMembership(spaceID, userID, includeDeleted)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return nil, false
	}
	return space, true
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
	if appErr.StatusCode == http.StatusConflict {
		p.writeConflictWithPage(w, appErr, nil)
		return
	}
	safe := *appErr
	safe.WipeDetailed()
	writeJSON(w, appErr.StatusCode, &safe)
}

// conflictResponse is the body every 409 carries: the scrubbed AppError plus the current server
// page. One shape across all conflicts means a client parses a 409 the same way whichever endpoint
// produced it, rather than branching on the route.
//
// current_page is null when the handler has no page to offer — the conflict was not about a page, or
// the re-read that would have produced it failed — so a client treats it as an optional shortcut and
// falls back to a GET. Where it is populated (publish and page-update conflicts, which already read
// the live page to build the error) it saves that round-trip: the client diffs and re-baselines
// against the returned EditAt directly. The whole page is returned rather than a curated snapshot,
// so the client renders whatever it needs.
type conflictResponse struct {
	Error       *mmmodel.AppError `json:"error"`
	CurrentPage *model.Page       `json:"current_page"`
}

// writeConflictWithPage writes a conflictResponse using the AppError's own StatusCode (409) as the
// HTTP status. DetailedError is scrubbed first, matching writeAppError.
func (p *Plugin) writeConflictWithPage(w http.ResponseWriter, appErr *mmmodel.AppError, current *model.Page) {
	safe := *appErr
	safe.WipeDetailed()
	writeJSON(w, appErr.StatusCode, conflictResponse{Error: &safe, CurrentPage: current})
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
