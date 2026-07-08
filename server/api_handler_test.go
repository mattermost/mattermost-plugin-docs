// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package main HTTP handler integration tests. They wire a real *Plugin (store + service +
// router) over an isolated Postgres schema and drive handlers via httptest. CreateSpace needs a
// pluginapi client, so that path uses a plugintest.API mock; the rest seed via the store directly.
//
// Handlers use the authenticated-only access stub (full permission gating is deferred), so these
// verify auth-only behaviour: 401 without a user, the CRUD round-trip, and the not-in-space 404.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

type apiTestHarness struct {
	plugin *Plugin
	store  *store.Store
}

// openTestPlugin wires a real *Plugin over an isolated test DB. When mockAPI is non-nil the
// service gets a pluginapi client backed by it (needed for CreateSpace's backing channel).
// A nil mockAPI creates a minimal stub that satisfies the EnableDocsRequired middleware.
func openTestPlugin(t *testing.T, mockAPI *plugintest.API) *apiTestHarness {
	t.Helper()
	db := testutil.OpenTestDB(t)
	s, err := store.New(db, "postgres")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.RunMigrations())

	var client *pluginapi.Client
	if mockAPI == nil {
		// Minimal stub: only satisfies the EnableDocsRequired middleware. No pluginapi client so
		// the nil-client guard in DeleteSpace/RestoreSpace continues to skip the channel side-effect.
		mockAPI = &plugintest.API{}
		t.Cleanup(func() { mockAPI.AssertExpectations(t) })
		mockAPI.On("GetConfig").Return(&mmmodel.Config{
			FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: true},
		}).Maybe()
	} else {
		client = pluginapi.NewClient(mockAPI, nil)
	}

	p := &Plugin{store: s, service: app.New(s, nil, client)}
	p.API = mockAPI
	p.router = p.initRouter()
	return &apiTestHarness{plugin: p, store: s}
}

// do issues a request through the plugin router. An empty userID omits the auth header.
func (h *apiTestHarness) do(t *testing.T, method, path, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if userID != "" {
		req.Header.Set("Mattermost-User-ID", userID)
	}
	rec := httptest.NewRecorder()
	h.plugin.ServeHTTP(&plugin.Context{}, rec, req)
	return rec
}

func seedSpace(t *testing.T, s *store.Store, channelID string) *model.Space {
	t.Helper()
	space, err := s.CreateSpace(&model.Space{ChannelId: channelID, TeamId: mmmodel.NewId(), CreatorId: mmmodel.NewId(), Title: "Test Space"})
	require.NoError(t, err)
	return space
}

func seedPage(t *testing.T, s *store.Store, spaceID, channelID, parentID string) *model.Page {
	t.Helper()
	page, err := s.CreatePage(&model.Page{SpaceId: spaceID, ChannelId: channelID, UserId: mmmodel.NewId(), ParentId: parentID, Type: model.PageTypePage, Title: "Test Page", Body: `{"type":"doc","content":[]}`}, store.MaxPageHierarchyDepth+10)
	require.NoError(t, err)
	return page
}

// TestHandler_RequiresAuth verifies the auth middleware rejects a request with no user header.
func TestHandler_RequiresAuth(t *testing.T) {
	h := openTestPlugin(t, nil)
	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+mmmodel.NewId(), "", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestHandler_EnableDocsRequired verifies that all routes return 501 when the EnableDocs
// feature flag is off, regardless of the authenticated user.
func TestHandler_EnableDocsRequired(t *testing.T) {
	mockAPI := &plugintest.API{}
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })
	mockAPI.On("GetConfig").Return(&mmmodel.Config{
		FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: false},
	}).Maybe()
	h := openTestPlugin(t, mockAPI)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+mmmodel.NewId(), mmmodel.NewId(), nil)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

// TestHandler_CreateSpace drives POST /spaces through the backing-channel mock.
func TestHandler_CreateSpace(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: true}}).Maybe()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	rec := h.do(t, http.MethodPost, "/api/v1/teams/"+teamID+"/spaces", mmmodel.NewId(), map[string]any{
		"title": "My Space",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)
	require.Equal(t, "My Space", created.Title)
}

// TestHandler_CreateSpace_IgnoresServerOwnedFields ensures the create handler does not trust
// server-owned fields from the request body: a client-supplied id, delete_at, create_at, and
// sort_order must be ignored so a caller cannot, e.g., create an already-soft-deleted space.
func TestHandler_CreateSpace_IgnoresServerOwnedFields(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: true}}).Maybe()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	forgedID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	rec := h.do(t, http.MethodPost, "/api/v1/teams/"+teamID+"/spaces", mmmodel.NewId(), map[string]any{
		"title":      "My Space",
		"id":         forgedID,
		"delete_at":  1,
		"create_at":  1,
		"sort_order": 99,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEqual(t, forgedID, created.Id, "client-supplied id must not be honored")
	require.Zero(t, created.DeleteAt, "space must not be created soft-deleted")
	require.NotZero(t, created.CreateAt, "create_at must be server-generated")

	// The space must be live and fetchable, not soft-deleted out of existence.
	get := h.do(t, http.MethodGet, "/api/v1/spaces/"+created.Id, mmmodel.NewId(), nil)
	require.Equal(t, http.StatusOK, get.Code)
}

// TestHandler_SpaceAndPageRoundTrip covers get-space, create/get page, the not-in-space 404,
// move, and delete.
func TestHandler_SpaceAndPageRoundTrip(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	t.Run("get space", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, user, nil)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("create page", func(t *testing.T) {
		rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", user, map[string]any{"title": "Page A"})
		require.Equal(t, http.StatusCreated, rec.Code)
		var page model.Page
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
		require.Equal(t, "Page A", page.Title)
		require.Equal(t, space.Id, page.SpaceId)
	})

	t.Run("create page with content and search_text", func(t *testing.T) {
		rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", user, map[string]any{
			"title":       "Page B",
			"body":        `{"type":"doc","content":[]}`,
			"search_text": "plain text projection",
		})
		require.Equal(t, http.StatusCreated, rec.Code)
		var page model.Page
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
		require.Equal(t, "plain text projection", page.SearchText)
	})

	t.Run("get page in wrong space is 404", func(t *testing.T) {
		page := seedPage(t, h.store, space.Id, channelID, "")
		otherSpace := seedSpace(t, h.store, mmmodel.NewId())
		rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id, user, nil)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("move then delete page", func(t *testing.T) {
		parent := seedPage(t, h.store, space.Id, channelID, "")
		page := seedPage(t, h.store, space.Id, channelID, "")

		rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/move", user, map[string]any{
			"parent_id":          parent.Id,
			"expected_update_at": page.UpdateAt,
		})
		require.Equal(t, http.StatusOK, rec.Code)
		var moved model.Page
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &moved))
		require.Equal(t, parent.Id, moved.ParentId)

		rec = h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestHandler_UpdateSpace patches a space's mutable fields.
func TestHandler_UpdateSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, mmmodel.NewId(), map[string]any{
		"title":              "Renamed",
		"description":        "New description",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var updated model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, "Renamed", updated.Title)
	require.Equal(t, "New description", updated.Description)

	// A stale baseline now conflicts (the optimistic lock is client-supplied).
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, mmmodel.NewId(), map[string]any{
		"title":              "Stale",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandler_DeleteSpace deletes a space; a follow-up read 404s.
func TestHandler_DeleteSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_RestoreSpace deletes then restores a space; a second restore of an already-live
// space is rejected as a 409 (conflict — the space is already live).
func TestHandler_RestoreSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/restore", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var restored model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &restored))
	require.Equal(t, space.Id, restored.Id)
	require.Zero(t, restored.DeleteAt)

	// A follow-up GET confirms the space is live.
	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// Restoring an already-live space is a 409 (state conflict, not a bad request).
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/restore", user, nil)
	require.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandler_GetSpacePages lists a space's pages.
func TestHandler_GetSpacePages(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	seedPage(t, h.store, space.Id, channelID, "")
	seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages", mmmodel.NewId(), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items   []*model.Page `json:"items"`
		Page    int           `json:"page"`
		PerPage int           `json:"per_page"`
		HasMore bool          `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	require.Equal(t, 0, resp.Page)
	require.False(t, resp.HasMore)
}

// TestHandler_GetTeamSpaces lists the spaces on a team.
func TestHandler_GetTeamSpaces(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+space.TeamId+"/spaces", mmmodel.NewId(), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items []*model.Space `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.GreaterOrEqual(t, len(resp.Items), 1)
}

// TestHandler_UpdatePage updates a page and verifies the optimistic-lock 409 on a stale baseline.
func TestHandler_UpdatePage(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	// Body and search text must be patched together (search text is the body's plain-text
	// projection), so both are supplied.
	body := map[string]any{
		"body":         `{"type":"doc","content":[{"type":"paragraph"}]}`,
		"search_text":  "updated text",
		"base_edit_at": page.EditAt,
	}
	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, body)
	require.Equal(t, http.StatusOK, rec.Code)

	// The first update bumped EditAt, so the same baseline is now stale.
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, body)
	require.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandler_RestorePage deletes then restores a page.
func TestHandler_RestorePage(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/restore", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var restored model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &restored))
	require.Equal(t, page.Id, restored.Id)
	require.Zero(t, restored.DeleteAt)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHandler_GetPageChildren returns a page's direct live children, paginated.
func TestHandler_GetPageChildren(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	parent := seedPage(t, h.store, space.Id, channelID, "")
	childA := seedPage(t, h.store, space.Id, channelID, parent.Id)
	childB := seedPage(t, h.store, space.Id, channelID, parent.Id)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+parent.Id+"/children", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items   []*model.Page `json:"items"`
		Page    int           `json:"page"`
		PerPage int           `json:"per_page"`
		HasMore bool          `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	require.ElementsMatch(t, []string{childA.Id, childB.Id}, []string{resp.Items[0].Id, resp.Items[1].Id})
	require.Equal(t, 0, resp.Page)
	require.False(t, resp.HasMore)
}

// TestHandler_Children_WrongSpaceIs404 verifies children is scoped to the route's space_id, not
// just the page_id: a page moved out of the URL's space reads as not-found rather than returning
// child data for its current location.
func TestHandler_GetPageChildren_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	otherSpace := seedSpace(t, h.store, mmmodel.NewId())
	parent := seedPage(t, h.store, space.Id, channelID, "")
	seedPage(t, h.store, space.Id, channelID, parent.Id)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+parent.Id+"/children", user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_MovePageToSpace moves a page to another space in the same team.
func TestHandler_MovePageToSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B"})
	require.NoError(t, err)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    spaceB.Id,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var moved model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &moved))
	require.Equal(t, spaceB.Id, moved.SpaceId)
}

// TestHandler_MovePageToSpace_CrossTeamRejected drives the cross-team rejection through the real
// HTTP handler, asserting the status code and AppError id the app layer returns.
func TestHandler_MovePageToSpace_CrossTeamRejected(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	// A second space in a different team (seedSpace randomizes the team id per call).
	spaceB := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    spaceB.Id,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.move_to_space.cross_team.app_error", appErr.Id)
}

// TestHandler_MovePageToSpace_DepthExceeded drives the depth-cap rejection through the real HTTP
// handler: spaceB already holds a chain down to MaxPageDepth, so moving a page from spaceA under
// the deepest node breaches the cap.
func TestHandler_MovePageToSpace_DepthExceeded(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	channelB := mmmodel.NewId()
	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: channelB, TeamId: spaceA.TeamId, CreatorId: user, Title: "B"})
	require.NoError(t, err)

	parentID := ""
	for range app.MaxPageDepth {
		p := seedPage(t, h.store, spaceB.Id, channelB, parentID)
		parentID = p.Id
	}

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    spaceB.Id,
		"parent_id":          parentID,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.move.max_depth_exceeded.app_error", appErr.Id)
}

// TestHandler_MovePageToSpace_RejectsCycle drives the cycle rejection through the real HTTP
// handler: moving a root page into the target space under one of its own descendants.
func TestHandler_MovePageToSpace_RejectsCycle(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	root := seedPage(t, h.store, spaceA.Id, channelA, "")
	child := seedPage(t, h.store, spaceA.Id, channelA, root.Id)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+root.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    spaceA.Id,
		"parent_id":          child.Id,
		"expected_update_at": root.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.move.circular_reference.app_error", appErr.Id)
}

// TestHandler_MovePageToSpace_ParentInWrongSpace drives the wrong-space-parent rejection through
// the real HTTP handler: the requested destination parent lives in spaceA, not the target spaceB.
func TestHandler_MovePageToSpace_ParentInWrongSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")
	parentInA := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B"})
	require.NoError(t, err)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    spaceB.Id,
		"parent_id":          parentInA.Id,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.move.parent_different_space.app_error", appErr.Id)
}

// TestHandler_MovePage_MaxDepthExceeded drives MovePage's depth-cap rejection through the real
// HTTP handler.
func TestHandler_MovePage_MaxDepthExceeded(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	parentID := ""
	for range app.MaxPageDepth {
		p := seedPage(t, h.store, space.Id, channelID, parentID)
		parentID = p.Id
	}
	leaf := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+leaf.Id+"/move", user, map[string]any{
		"parent_id":          parentID,
		"expected_update_at": leaf.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.move.max_depth_exceeded.app_error", appErr.Id)
}

// TestHandler_MovePage_CircularReference drives MovePage's circular-reference rejection through
// the real HTTP handler, covering both the self-parent case and the under-own-descendant case.
func TestHandler_MovePage_CircularReference(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	t.Run("self as parent", func(t *testing.T) {
		page := seedPage(t, h.store, space.Id, channelID, "")
		rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/move", user, map[string]any{
			"parent_id":          page.Id,
			"expected_update_at": page.UpdateAt,
		})
		require.Equal(t, http.StatusBadRequest, rec.Code)
		var appErr mmmodel.AppError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
		require.Equal(t, "app.page.move.circular_reference.app_error", appErr.Id)
	})

	t.Run("under own descendant", func(t *testing.T) {
		root := seedPage(t, h.store, space.Id, channelID, "")
		child := seedPage(t, h.store, space.Id, channelID, root.Id)

		rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+root.Id+"/move", user, map[string]any{
			"parent_id":          child.Id,
			"expected_update_at": root.UpdateAt,
		})
		require.Equal(t, http.StatusBadRequest, rec.Code)
		var appErr mmmodel.AppError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
		require.Equal(t, "app.page.move.circular_reference.app_error", appErr.Id)
	})
}

// TestHandler_MovePage_ParentInDifferentSpace drives MovePage's cross-space-parent rejection
// through the real HTTP handler: the requested parent lives in a different space.
func TestHandler_MovePage_ParentInDifferentSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	channelB := mmmodel.NewId()
	spaceB := seedSpace(t, h.store, channelB)
	parentInB := seedPage(t, h.store, spaceB.Id, channelB, "")

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move", user, map[string]any{
		"parent_id":          parentInB.Id,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.move.parent_different_space.app_error", appErr.Id)
}

// TestHandler_DuplicatePage duplicates a page in place.
func TestHandler_DuplicatePage(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/duplicate", user, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var dup model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dup))
	require.NotEqual(t, page.Id, dup.Id)
}

// TestHandler_DuplicatePage_WithChildren duplicates a page and its subtree.
func TestHandler_DuplicatePage_WithChildren(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	parent := seedPage(t, h.store, space.Id, channelID, "")
	seedPage(t, h.store, space.Id, channelID, parent.Id)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+parent.Id+"/duplicate", user, map[string]any{
		"include_children": true,
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var dup model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dup))
	require.NotEqual(t, parent.Id, dup.Id)
}

// TestHandler_DuplicatePage_CrossSpace duplicates a page into another space in the same team.
func TestHandler_DuplicatePage_CrossSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B"})
	require.NoError(t, err)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/duplicate", user, map[string]any{
		"target_space_id": spaceB.Id,
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var dup model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dup))
	require.Equal(t, spaceB.Id, dup.SpaceId)
}

// TestHandler_DuplicatePage_WithParent duplicates a page under a specified parent.
func TestHandler_DuplicatePage_WithParent(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	parent := seedPage(t, h.store, space.Id, channelID, "")
	page := seedPage(t, h.store, space.Id, channelID, "")
	parentID := parent.Id

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/duplicate", user, map[string]any{
		"parent_id": parentID,
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var dup model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dup))
	require.Equal(t, parent.Id, dup.ParentId)
}

// TestHandler_DuplicatePage_WrongSpaceIs404 verifies duplicate is scoped to the route's space_id.
func TestHandler_DuplicatePage_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id+"/duplicate", user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_UpdatePage_WrongSpaceIs404 verifies update is scoped to the route's space_id.
func TestHandler_UpdatePage_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id, user, map[string]any{
		"title": "New Title",
	})
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_DeletePage_WrongSpaceIs404 verifies delete is scoped to the route's space_id.
func TestHandler_DeletePage_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_RestorePage_WrongSpaceIs404 verifies restore is scoped to the route's space_id.
func TestHandler_RestorePage_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, mmmodel.NewId())

	// Soft-delete the page so restore has something to act on.
	require.NoError(t, h.store.DeletePage(page.Id, space.Id))

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id+"/restore", user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_InvalidJSON returns 400 on a malformed request body.
func TestHandler_InvalidJSON(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Mattermost-User-ID", mmmodel.NewId())
	rec := httptest.NewRecorder()
	h.plugin.ServeHTTP(&plugin.Context{}, rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandler_RequestTooLarge returns 413 when the request body exceeds maxPageBodyBytes,
// distinguishing the too-large branch of decodeJSONBody from the malformed-JSON branch covered by
// TestHandler_InvalidJSON.
func TestHandler_RequestTooLarge(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	// A well-formed JSON document whose "content" field alone exceeds maxPageBodyBytes, so the
	// body is rejected for size before JSON parsing can even consider it malformed.
	oversizedContent := strings.Repeat("a", maxPageBodyBytes+1)
	body := map[string]any{"content": oversizedContent}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, bytes.NewReader(raw))
	req.Header.Set("Mattermost-User-ID", user)
	rec := httptest.NewRecorder()
	h.plugin.ServeHTTP(&plugin.Context{}, rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "api.request_too_large.app_error", appErr.Id)
}
