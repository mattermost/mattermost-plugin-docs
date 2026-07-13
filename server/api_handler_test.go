// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package main HTTP handler integration tests. They wire a real *Plugin (store + service +
// router) over an isolated Postgres schema and drive handlers via httptest. CreateSpace needs a
// pluginapi client, so that path uses a plugintest.API mock; the rest seed via the store directly.
//
// Every space- and page-scoped handler enforces backing-channel membership (CheckSpaceMembership),
// returning 403 Forbidden for non-members. Per-page role ACLs (author vs. editor) are not yet
// implemented and are deferred to a follow-up.
package main

import (
	"bytes"
	"database/sql"
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
	// db is the schema-scoped handle, exposed so tests can seed core-table stand-ins
	// (e.g. ChannelMembers rows for the team-listing visibility join).
	db *sql.DB
}

// openTestPlugin wires a real *Plugin over an isolated test DB. When mockAPI is non-nil the
// service gets a pluginapi client backed by it (needed for CreateSpace's backing channel).
// A nil mockAPI creates a minimal stub that satisfies the EnableDocsRequired middleware.
func openTestPlugin(t *testing.T, mockAPI *plugintest.API) *apiTestHarness {
	t.Helper()
	s, db := testutil.OpenTestStore(t)

	var client *pluginapi.Client
	if mockAPI == nil {
		// Minimal stub: satisfies the EnableDocsRequired middleware and grants any user membership
		// to any channel/team so space-scoped tests pass membership checks without a real server.
		// Channel side-effects (archive/restore) are no-ops so store-only tests don't need to
		// set up specific channel expectations.
		mockAPI = newEnabledMockAPI()
		mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
		mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
		mockAPI.On("DeleteChannel", mock.Anything).Return(nil).Maybe()
		mockAPI.On("RestoreChannel", mock.Anything).Return(nil).Maybe()
		mockAPI.On("GetChannelMembers", mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).Return(mmmodel.ChannelMembers{}, nil).Maybe()
		mockAPI.On("GetChannelOfType", mock.Anything, mock.Anything).Return((*mmmodel.Channel)(nil), nil).Maybe()
	}
	// writeAppError logs 500-class failures (message plus four key/value pairs) regardless of
	// which mock a test supplies, so stub it universally.
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	client = pluginapi.NewClient(mockAPI, nil)
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })

	p := &Plugin{store: s, service: app.New(s, nil, client)}
	p.API = mockAPI
	p.snapshotFeatureFlags(p.API.GetConfig())
	p.router = p.initRouter()
	return &apiTestHarness{plugin: p, store: s, db: db}
}

// newEnabledMockAPI returns a plugintest mock pre-stubbed with the EnableDocs flag on and
// best-effort WS publishes swallowed — the shared baseline for tests that add their own
// channel/team expectations. Tests pinning a specific PublishWebSocketEvent expectation must
// build their mock by hand: this wildcard stub would swallow the pinned call.
func newEnabledMockAPI() *plugintest.API {
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: true}}).Maybe()
	mockAPI.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	return mockAPI
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
	return seedSpaceInTeam(t, s, channelID, mmmodel.NewId())
}

// seedSpaceInTeam mirrors testutil.MustCreateSpace's (channelID, teamID) parameter order.
func seedSpaceInTeam(t *testing.T, s *store.Store, channelID, teamID string) *model.Space {
	t.Helper()
	return testutil.MustCreateSpace(t, s, channelID, teamID)
}

func seedPage(t *testing.T, s *store.Store, spaceID, channelID, parentID string) *model.Page {
	t.Helper()
	return testutil.MustCreatePage(t, s, spaceID, channelID, mmmodel.NewId(), parentID)
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
	mockAPI := newEnabledMockAPI()
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
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
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

// TestHandler_DeleteSpace deletes a space; a follow-up read returns 403 (CheckSpaceMembership
// masks deleted-space existence to prevent probing via the status code).
func TestHandler_DeleteSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
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
	var resp paginatedResponse[*model.PageSummary]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	require.Equal(t, 0, resp.Page)
	require.False(t, resp.HasMore)
}

// TestHandler_PageCollectionsReturnMetadataOnly verifies the tree/list endpoints neither load nor
// serialize a page's large content or opaque props, while the single-page endpoint remains the
// full-content read path.
func TestHandler_PageCollectionsReturnMetadataOnly(t *testing.T) {
	h := openTestPlugin(t, nil)
	userID := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	parentInput := testutil.NewPage(space.Id, channelID, userID, "")
	parentInput.Title = "Parent"
	parentInput.Body = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"parent content"}]}]}`
	parentInput.SearchText = "parent content"
	parentInput.Props = mmmodel.StringInterface{"internal": "do not list"}
	parent, err := h.store.CreatePage(parentInput, store.MaxPageHierarchyDepth+10)
	require.NoError(t, err)

	childInput := testutil.NewPage(space.Id, channelID, userID, parent.Id)
	childInput.Title = "Child"
	childInput.Body = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"child content"}]}]}`
	childInput.SearchText = "child content"
	childInput.Props = mmmodel.StringInterface{"internal": "do not list"}
	child, err := h.store.CreatePage(childInput, store.MaxPageHierarchyDepth+10)
	require.NoError(t, err)

	assertSummary := func(path, expectedID string) {
		t.Helper()
		rec := h.do(t, http.MethodGet, path, userID, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var response struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

		for _, item := range response.Items {
			var id string
			require.NoError(t, json.Unmarshal(item["id"], &id))
			if id != expectedID {
				continue
			}
			require.Contains(t, item, "title")
			require.NotContains(t, item, "body")
			require.NotContains(t, item, "search_text")
			require.NotContains(t, item, "props")
			return
		}
		require.Failf(t, "summary not found", "expected page %s in %s", expectedID, path)
	}

	assertSummary("/api/v1/spaces/"+space.Id+"/pages", parent.Id)
	assertSummary("/api/v1/spaces/"+space.Id+"/pages/"+parent.Id+"/children", child.Id)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+parent.Id, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var detail map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	require.Contains(t, detail, "body")
	require.Contains(t, detail, "search_text")
	require.Contains(t, detail, "props")
}

// TestHandler_ListSpaceMembers lists a space's members through the backing channel.
func TestHandler_ListSpaceMembers(t *testing.T) {
	channelID := mmmodel.NewId()
	memberUserID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("GetChannelMembers", channelID, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: memberUserID}}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, channelID)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/members", mmmodel.NewId(), nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.SpaceMember]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.Equal(t, memberUserID, resp.Items[0].UserId)
	require.False(t, resp.HasMore)
}

// TestHandler_ListSpaceMembers_HasMore verifies the probe row: when the requested page comes back
// full and another member exists on the next page, has_more is true and the page is trimmed to
// per_page entries.
func TestHandler_ListSpaceMembers_HasMore(t *testing.T) {
	channelID := mmmodel.NewId()
	firstMember := mmmodel.NewId()
	secondMember := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	// Page 0 at size 1 comes back full, so the handler probes the next page's first slot
	// (page-indexed API: index (page+1)*perPage at size 1).
	mockAPI.On("GetChannelMembers", channelID, 0, 1).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: firstMember}}, nil)
	mockAPI.On("GetChannelMembers", channelID, 1, 1).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: secondMember}}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, channelID)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/members?per_page=1", mmmodel.NewId(), nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.SpaceMember]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.Equal(t, firstMember, resp.Items[0].UserId)
	require.True(t, resp.HasMore)
}

// TestHandler_AddSpaceMember adds a member to a space; any current space member may do so.
func TestHandler_AddSpaceMember(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("AddChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, channelID)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/members", mmmodel.NewId(), map[string]any{
		"user_id": targetUserID,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var member model.SpaceMember
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &member))
	require.Equal(t, targetUserID, member.UserId)
	mockAPI.AssertCalled(t, "AddChannelMember", channelID, targetUserID)
}

// TestHandler_RemoveSpaceMember removes a member from a space.
func TestHandler_RemoveSpaceMember(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	// The last-member guard scans the member list before removing; report another (active)
	// member so the removal proceeds.
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: targetUserID}, {ChannelId: channelID, UserId: mmmodel.NewId()}}, nil)
	mockAPI.On("DeleteChannelMember", channelID, targetUserID).Return(nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, channelID)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID, mmmodel.NewId(), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	mockAPI.AssertCalled(t, "DeleteChannelMember", channelID, targetUserID)
}

// TestHandler_GetTeamSpaces lists the spaces on a team visible to the caller.
func TestHandler_GetTeamSpaces(t *testing.T) {
	channelID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	caller := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", teamID, caller).Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	seedSpaceInTeam(t, h.store, channelID, teamID)
	testutil.MustAddChannelMember(t, h.db, channelID, caller)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp paginatedResponse[*model.Space]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
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

	var updated model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, `{"type":"doc","content":[{"type":"paragraph"}]}`, updated.Body)
	require.Equal(t, "updated text", updated.SearchText)

	// The first update bumped EditAt, so the same baseline is now stale.
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, body)
	require.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandler_UpdatePage_BaselineRequired verifies a PATCH that omits base_edit_at without force
// is rejected up front with a clear 400 rather than a misleading 409 conflict. The same app-layer
// baseline gate covers move, move-to-space, and space update.
func TestHandler_UpdatePage_BaselineRequired(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, map[string]any{
		"body":        `{"type":"doc","content":[]}`,
		"search_text": "text",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.optimistic_lock.baseline_required.app_error", appErr.Id)

	// force substitutes for the baseline (last-write-wins).
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, map[string]any{
		"body":        `{"type":"doc","content":[]}`,
		"search_text": "text",
		"force":       true,
	})
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHandler_UpdatePage_Props verifies that props supplied in the PATCH body are persisted and
// returned in the response.
func TestHandler_UpdatePage_Props(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	body := map[string]any{
		"props":        map[string]any{"key": "value"},
		"base_edit_at": page.EditAt,
	}
	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, body)
	require.Equal(t, http.StatusOK, rec.Code)

	var updated model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, "value", updated.Props["key"])
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
	var resp paginatedResponse[*model.PageSummary]
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

// TestHandler_MovePageToSpace_MissingTargetSpaceId verifies that omitting target_space_id returns
// the handler-specific 400 before any app-layer call is made.
func TestHandler_MovePageToSpace_MissingTargetSpaceId(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "api.page.move_to_space.missing_target_space_id.app_error", appErr.Id)
}

// TestHandler_MovePageToSpace_InvalidTargetSpaceId verifies a malformed (non-empty) target_space_id
// is rejected with the handler-specific 400 before any app-layer call is made.
func TestHandler_MovePageToSpace_InvalidTargetSpaceId(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    "not-a-valid-id",
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "api.page.move_to_space.invalid_target_space_id.app_error", appErr.Id)
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
	require.Equal(t, "app.page.max_depth_exceeded.app_error", appErr.Id)
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
	require.Equal(t, "app.page.circular_reference.app_error", appErr.Id)
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
	require.Equal(t, "app.page.invalid_parent.app_error", appErr.Id)
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
	require.Equal(t, "app.page.max_depth_exceeded.app_error", appErr.Id)
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
		require.Equal(t, "app.page.circular_reference.app_error", appErr.Id)
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
		require.Equal(t, "app.page.circular_reference.app_error", appErr.Id)
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
	require.Equal(t, "app.page.invalid_parent.app_error", appErr.Id)
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

// TestHandler_DuplicatePage_InvalidTargetSpaceId verifies a malformed target_space_id is rejected
// with the handler-specific 400 before any app-layer call is made.
func TestHandler_DuplicatePage_InvalidTargetSpaceId(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/duplicate", user, map[string]any{
		"target_space_id": "not-a-valid-id",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "api.page.duplicate.invalid_target_space_id.app_error", appErr.Id)
}

// TestHandler_DuplicatePage_WithParent duplicates a page under a specified parent.
func TestHandler_DuplicatePage_WithParent(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	parent := seedPage(t, h.store, space.Id, channelID, "")
	page := seedPage(t, h.store, space.Id, channelID, "")
	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/duplicate", user, map[string]any{
		"parent_id": parent.Id,
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
		"title":        "New Title",
		"base_edit_at": page.EditAt,
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
	_, delErr := h.store.DeletePage(page.Id, space.Id, user)
	require.NoError(t, delErr)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id+"/restore", user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_SpaceMembershipRequired verifies that all space- and page-scoped handlers
// reject callers who are not members of the space's backing channel with 403 Forbidden.
func TestHandler_SpaceMembershipRequired(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	stranger := mmmodel.NewId()

	// The stranger passes the team gate (an active member row) so the channel-membership
	// rejection below is what each route exercises.
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), stranger).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", channelID, stranger).
		Return(nil, &mmmodel.AppError{StatusCode: http.StatusNotFound})

	cases := []struct {
		method string
		path   string
		body   any
	}{
		// Space-scoped handlers.
		{http.MethodGet, "/api/v1/spaces/" + space.Id, nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id, map[string]any{"title": "X"}},
		{http.MethodDelete, "/api/v1/spaces/" + space.Id, nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/restore", nil},
		{http.MethodGet, "/api/v1/spaces/" + space.Id + "/pages", nil},
		// Page-scoped handlers.
		{http.MethodPost, "/api/v1/spaces/" + space.Id + "/pages", map[string]any{"title": "P"}},
		{http.MethodGet, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id, nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id, map[string]any{"title": "P"}},
		{http.MethodDelete, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id, nil},
		{http.MethodGet, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/children", nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/restore", nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/move", nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/move-to-space", nil},
		{http.MethodPost, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/duplicate", nil},
	}
	for _, tc := range cases {
		rec := h.do(t, tc.method, tc.path, stranger, tc.body)
		require.Equal(t, http.StatusForbidden, rec.Code, "expected 403 for %s %s", tc.method, tc.path)
	}
}

// TestHandler_TeamSpacesPrivacy verifies that GET /teams/{team_id}/spaces filters out spaces
// whose backing channel the caller does not belong to.
func TestHandler_TeamSpacesPrivacy(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	visibleChannelID := mmmodel.NewId()
	hiddenChannelID := mmmodel.NewId()
	visible := seedSpaceInTeam(t, h.store, visibleChannelID, teamID)
	_ = seedSpaceInTeam(t, h.store, hiddenChannelID, teamID)
	caller := mmmodel.NewId()

	mockAPI.On("GetTeamMember", teamID, caller).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: caller}, nil)
	testutil.MustAddChannelMember(t, h.db, visibleChannelID, caller)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.Space]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.Equal(t, visible.Id, resp.Items[0].Id)
}

// TestHandler_TeamSpacesHiddenPageBoundary verifies that spaces the caller cannot access are
// filtered at the SQL level: the ChannelMembers join excludes hidden spaces before applying
// offset/limit, so pagination never delivers invisible spaces.
func TestHandler_TeamSpacesHiddenPageBoundary(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	visibleChannelID := mmmodel.NewId()
	hidden1ChannelID := mmmodel.NewId()
	hidden2ChannelID := mmmodel.NewId()
	caller := mmmodel.NewId()

	visible := seedSpaceInTeam(t, h.store, visibleChannelID, teamID)
	_ = seedSpaceInTeam(t, h.store, hidden1ChannelID, teamID)
	_ = seedSpaceInTeam(t, h.store, hidden2ChannelID, teamID)

	mockAPI.On("GetTeamMember", teamID, caller).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: caller}, nil)
	// The caller belongs only to the visible channel; the ChannelMembers join excludes the
	// hidden channels' spaces entirely.
	testutil.MustAddChannelMember(t, h.db, visibleChannelID, caller)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces?per_page=2", caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.Space]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.Equal(t, visible.Id, resp.Items[0].Id)
	require.False(t, resp.HasMore)
}

// TestHandler_TeamSpacesPagination verifies that the listing paginates over the spaces visible to
// the caller: with three visible spaces and per_page=2, the first page returns two items and
// reports has_more.
func TestHandler_TeamSpacesPagination(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	caller := mmmodel.NewId()
	for range 3 {
		channelID := mmmodel.NewId()
		_ = seedSpaceInTeam(t, h.store, channelID, teamID)
		testutil.MustAddChannelMember(t, h.db, channelID, caller)
	}

	mockAPI.On("GetTeamMember", teamID, caller).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: caller}, nil)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces?per_page=2", caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.Space]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	require.True(t, resp.HasMore)
}

// TestHandler_TeamSpacesNotTeamMember verifies that a caller who is not a member of the team is
// denied with 403 (the team-membership gate on the listing route).
func TestHandler_TeamSpacesNotTeamMember(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	_ = seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	caller := mmmodel.NewId()

	mockAPI.On("GetTeamMember", teamID, caller).
		Return(nil, &mmmodel.AppError{StatusCode: http.StatusNotFound})

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_TeamSpacesTeamLookupError verifies that a non-NotFound backend error from the
// team-membership lookup propagates as 500.
func TestHandler_TeamSpacesTeamLookupError(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	_ = seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	caller := mmmodel.NewId()

	mockAPI.On("GetTeamMember", teamID, caller).
		Return(nil, &mmmodel.AppError{StatusCode: http.StatusInternalServerError})

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
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

// TestDecodeJSONBody_TrailingDataTooLarge covers a body whose first JSON value decodes within the
// cap but whose trailing bytes push the read past it: the size check must win over the
// trailing-data check, so the caller sees 413 rather than 400.
func TestDecodeJSONBody_TrailingDataTooLarge(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}{"b":2}`))
	rec := httptest.NewRecorder()

	var v map[string]any
	require.False(t, (&Plugin{}).decodeJSONBody(rec, req, 8, &v, "testWhere", false))
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "api.request_too_large.app_error", appErr.Id)
}

// TestDecodeJSONBody_TrailingDataWithinCap pins the complementary case: trailing data that fits
// under the cap is malformed input, not an oversized body, so it stays a 400.
func TestDecodeJSONBody_TrailingDataWithinCap(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}{"b":2}`))
	rec := httptest.NewRecorder()

	var v map[string]any
	require.False(t, (&Plugin{}).decodeJSONBody(rec, req, 1024, &v, "testWhere", false))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "api.invalid_json.app_error", appErr.Id)
}

// TestHandler_MovePage_InvalidParentID verifies that a malformed parent_id in the move request
// body is rejected with the same key as a missing parent.
func TestHandler_MovePage_InvalidParentID(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	bad := "not-a-valid-id"
	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/move", user, map[string]any{
		"parent_id":          bad,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.invalid_parent.app_error", appErr.Id)
}

// TestHandler_CreatePage_InvalidParentID verifies that a malformed parent_id in the create
// request body is rejected with the same key as a missing parent.
func TestHandler_CreatePage_InvalidParentID(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	bad := "not-a-valid-id"
	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", user, map[string]any{
		"title":     "New Page",
		"parent_id": bad,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.invalid_parent.app_error", appErr.Id)
}

// TestHandler_DuplicatePage_InvalidParentID verifies that a malformed parent_id in the duplicate
// request body is rejected with the same key as a missing parent.
func TestHandler_DuplicatePage_InvalidParentID(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	bad := "not-a-valid-id"
	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/duplicate", user, map[string]any{
		"parent_id": bad,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.invalid_parent.app_error", appErr.Id)
}

// TestHandler_MovePageToSpace_InvalidParentID verifies that a malformed parent_id in the
// move-to-space request body is rejected with the same key as a missing parent.
func TestHandler_MovePageToSpace_InvalidParentID(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelA := mmmodel.NewId()
	spaceA := seedSpace(t, h.store, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B"})
	require.NoError(t, err)

	bad := "not-a-valid-id"
	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+spaceA.Id+"/pages/"+page.Id+"/move-to-space", user, map[string]any{
		"target_space_id":    spaceB.Id,
		"parent_id":          bad,
		"expected_update_at": page.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.page.invalid_parent.app_error", appErr.Id)
}

// TestHandler_GetSpace covers the happy path: a member fetches a space and receives its fields.
func TestHandler_GetSpace(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var got model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, space.Id, got.Id)
	require.Equal(t, space.TeamId, got.TeamId)
	require.Equal(t, "Test Space", got.Title)
}

// TestHandler_GetPage covers the happy path: a member fetches a page and receives its fields.
func TestHandler_GetPage(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var got model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, page.Id, got.Id)
	require.Equal(t, space.Id, got.SpaceId)
	require.Equal(t, "Test Page", got.Title)
}

// TestHandler_CreatePage covers the happy path: a member creates a page and receives the
// server-generated resource with a 201.
func TestHandler_CreatePage(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", user, map[string]any{
		"title": "Brand New Page",
		"body":  `{"type":"doc","content":[]}`,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.True(t, mmmodel.IsValidId(created.Id), "id must be server-generated")
	require.Equal(t, space.Id, created.SpaceId)
	require.Equal(t, "Brand New Page", created.Title)
	require.Equal(t, user, created.UserId)

	// The page is readable back through the API.
	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+created.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHandler_DeletePage covers the happy path: deleting a page returns OK and the page stops
// resolving through the API.
func TestHandler_DeletePage(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_GetSpacePagesHasMoreBoundary pins the has_more transition exactly at the page-size
// boundary: a window equal to the result count reports has_more=false; one smaller reports true.
func TestHandler_GetSpacePagesHasMoreBoundary(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	for range 3 {
		seedPage(t, h.store, space.Id, channelID, "")
	}

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages?per_page=3", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var full paginatedResponse[model.PageSummary]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))
	require.Len(t, full.Items, 3)
	require.False(t, full.HasMore, "a window holding every row must not report more")

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages?per_page=2", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var trimmed paginatedResponse[model.PageSummary]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &trimmed))
	require.Len(t, trimmed.Items, 2)
	require.True(t, trimmed.HasMore, "a window one short of the row count must report more")
}

// TestHandler_GetPageChildrenHasMoreBoundary pins the same has_more boundary for the children
// listing.
func TestHandler_GetPageChildrenHasMoreBoundary(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	parent := seedPage(t, h.store, space.Id, channelID, "")
	for range 3 {
		seedPage(t, h.store, space.Id, channelID, parent.Id)
	}

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+parent.Id+"/children?per_page=3", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var full paginatedResponse[model.PageSummary]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))
	require.Len(t, full.Items, 3)
	require.False(t, full.HasMore)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/pages/"+parent.Id+"/children?per_page=2", user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var trimmed paginatedResponse[model.PageSummary]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &trimmed))
	require.Len(t, trimmed.Items, 2)
	require.True(t, trimmed.HasMore)
}

// TestHandler_CreatePage_PublishesCreatedEvent pins the created event's name, payload shape, and
// channel-scoped broadcast — a typo in any of them would otherwise pass every Maybe-stubbed test.
func TestHandler_CreatePage_PublishesCreatedEvent(t *testing.T) {
	channelID := mmmodel.NewId()

	// Built by hand instead of via newEnabledMockAPI: its wildcard PublishWebSocketEvent stub
	// would swallow the pinned expectation below.
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: true}}).Maybe()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("PublishWebSocketEvent", "page_created", mock.Anything, mock.Anything).Return().Once()
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, channelID)
	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", mmmodel.NewId(), map[string]any{
		"title": "Evented Page",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "page_created",
		map[string]any{"page_id": created.Id, "space_id": space.Id, "parent_id": created.ParentId},
		&mmmodel.WebsocketBroadcast{ChannelId: channelID})
}
