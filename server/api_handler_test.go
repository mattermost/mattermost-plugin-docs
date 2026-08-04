// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package main HTTP handler integration tests. They wire a real *Plugin (store + service +
// router) over an isolated Postgres schema and drive handlers via httptest. CreateSpace needs a
// pluginapi client, so that path uses a plugintest.API mock; the rest seed via the store directly.
//
// Every space- and page-scoped handler enforces the capability-based RBAC gates in
// server/app/permissions.go, returning 403 Forbidden for a caller the read resolver denies.
// Per-page role ACLs (author vs. editor) are not yet implemented and are deferred to a follow-up.
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
		mockAPI = newEnabledMockAPI()
	}
	// Minimal defaults: satisfy the EnableDocsRequired middleware and isActiveTeamMember/read
	// checks so space-scoped tests pass membership checks without a real server. Channel
	// side-effects (archive/restore) are no-ops so store-only tests don't need to set up specific
	// channel expectations. .Maybe() and registered last, so any test-specific stub registered
	// before openTestPlugin is called takes precedence.
	mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
	mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	mockAPI.On("DeleteChannel", mock.Anything).Return(nil).Maybe()
	mockAPI.On("RestoreChannel", mock.Anything).Return(nil).Maybe()
	mockAPI.On("GetChannelMembers", mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).Return(mmmodel.ChannelMembers{}, nil).Maybe()
	mockAPI.On("GetChannelStats", mock.Anything).Return(&mmmodel.ChannelStats{}, nil).Maybe()
	// writeAppError logs 500-class failures (message plus four key/value pairs) regardless of
	// which mock a test supplies, so stub it universally. The two-pair shape covers the
	// custom-scheme retire failures.
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	testutil.StubDefaultSpacePermissions(mockAPI)
	testutil.StubPresetSchemes(mockAPI)
	// Resolves any channel to the contribute preset, and serves the channel read/write that
	// UpdateSpace's best-effort metadata sync (syncSpaceChannelMetadata) performs. Registered last
	// because mock.Mock matches in registration order: a test seeding a channel at a specific
	// (non-contribute) scheme, or asserting a repoint, registers its own stub before calling
	// openTestPlugin (testutil.MustSeedChannelScheme / stubSpaceSchemeRepoint) so it wins.
	testutil.StubDefaultChannelScheme(mockAPI)
	// CreateSpace assigns the creator SchemeAdmin via the scheme's resolved role-name string; no
	// test asserts the exact roles argument, so a wildcard catch-all covers every create.
	mockAPI.On("UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
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

// grantSpaceManage, grantSpaceAdmin, and grantSpaceDelete register an elevated-permission
// expectation for userID that MUST be added to mockAPI before it is passed to openTestPlugin:
// openTestPlugin registers testutil.StubDefaultSpacePermissions' default-deny catch-alls for
// manage_space/admin_space/delete_space, and mock.Mock matches expectations in registration
// order, so a stub added after openTestPlugin runs would never be reached.
func grantSpaceManage(mockAPI *plugintest.API, userID string) {
	mockAPI.On("HasPermissionToTeam", userID, mock.Anything, mmmodel.PermissionManageSpace).Return(true)
}

func grantSpaceDelete(mockAPI *plugintest.API, userID string) {
	mockAPI.On("HasPermissionToTeam", userID, mock.Anything, mmmodel.PermissionDeleteSpace).Return(true)
}

// grantSpaceAdmin registers a channel-level admin_space grant for userID on channelID — the
// exposure-policy gate (RequireSpaceAdminOrSysadmin), stricter than grantSpaceManage's team
// manage_space grant, which that gate deliberately does not accept.
func grantSpaceAdmin(mockAPI *plugintest.API, channelID, userID string) {
	mockAPI.On("HasPermissionToChannel", userID, channelID, mmmodel.PermissionAdminSpace).Return(true)
}

// grantSysadmin registers the system-wide manage_system override for userID, satisfying every
// space gate unconditionally.
func grantSysadmin(mockAPI *plugintest.API, userID string) {
	mockAPI.On("HasPermissionTo", userID, mmmodel.PermissionManageSystem).Return(true)
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

func seedSpace(t *testing.T, s *store.Store, db *sql.DB, channelID string) *model.Space {
	t.Helper()
	return seedSpaceInTeam(t, s, db, channelID, mmmodel.NewId())
}

// seedSpaceInTeam creates a space via the store directly. Its channel resolves to the contribute
// preset through the generic catch-all (testutil.StubDefaultChannelScheme, wired in
// openTestPlugin), so no per-channel scheme stub is registered here.
func seedSpaceInTeam(t *testing.T, s *store.Store, db *sql.DB, channelID, teamID string) *model.Space {
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
	backingChannelID := mmmodel.NewId()
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("UpdateChannelMemberRoles", backingChannelID, mock.Anything, mock.Anything).
		Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	rec := h.do(t, http.MethodPost, "/api/v1/teams/"+teamID+"/spaces", mmmodel.NewId(), map[string]any{
		"title": "My Space",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created model.SpaceWithAccess
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)
	require.Equal(t, "My Space", created.Title)
	// Create establishes the space's access state, so the response carries it and the caller needs
	// no follow-up read: the seeded contribute default, and the creator's own admin set.
	contribute, ok := model.DefaultCapabilitiesForSchemeName(mmmodel.SchemeNameSpaceContribute)
	require.True(t, ok)
	require.Equal(t, contribute, created.DefaultCapabilities)
	require.Equal(t, model.AdminEffectiveCapabilities(), created.Capabilities)
	require.Contains(t, created.Capabilities, model.CapabilityAdminSpace)
}

// TestHandler_CreateSpace_IgnoresServerOwnedFields ensures the create handler does not trust
// server-owned fields from the request body: a client-supplied id, delete_at, create_at, and
// sort_order must be ignored so a caller cannot, e.g., create an already-soft-deleted space.
func TestHandler_CreateSpace_IgnoresServerOwnedFields(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	backingChannelID := mmmodel.NewId()
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: backingChannelID, Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("UpdateChannelMemberRoles", backingChannelID, mock.Anything, mock.Anything).
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
	space := seedSpace(t, h.store, h.db, channelID)

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

	t.Run("create page derives search_text from body", func(t *testing.T) {
		rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages", user, map[string]any{
			"title":       "Page B",
			"body":        `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"searchable body"}]}]}`,
			"search_text": "ignored client value",
		})
		require.Equal(t, http.StatusCreated, rec.Code)
		var page model.Page
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
		require.Equal(t, "searchable body", page.SearchText, "SearchText is derived from the body, not the caller-supplied value")
	})

	t.Run("get page in wrong space is 404", func(t *testing.T) {
		page := seedPage(t, h.store, space.Id, channelID, "")
		otherSpace := seedSpace(t, h.store, h.db, mmmodel.NewId())
		rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id, user, nil)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("move then delete page", func(t *testing.T) {
		parent := seedPage(t, h.store, space.Id, channelID, "")
		// Owned by the acting user: a contribute-default member holds only delete_own_page, not
		// delete_page (any) — deleting a page owned by someone else needs that separate grant,
		// exercised separately.
		page := testutil.MustCreatePage(t, h.store, space.Id, channelID, user, "")

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

// TestHandler_UpdateSpace patches a space's mutable fields. Updating a space is manage-gated: the
// acting user needs an elevated grant, not bare membership.
func TestHandler_UpdateSpace(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	adminID := mmmodel.NewId()
	grantSpaceManage(mockAPI, adminID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, adminID, map[string]any{
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
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, adminID, map[string]any{
		"title":              "Stale",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandler_UpdateSpace_NonManageMemberForbidden verifies an ordinary contribute-default member
// (no manage_space/admin_space grant) cannot update a space's mutable fields.
func TestHandler_UpdateSpace_NonManageMemberForbidden(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, mmmodel.NewId(), map[string]any{
		"title":              "Renamed",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_DeleteSpace deletes a space; a follow-up read returns 403 (the gate masks
// deleted-space existence to prevent probing via the status code). Delete/restore requires team
// delete_space or channel admin_space.
func TestHandler_DeleteSpace(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	user := mmmodel.NewId()
	grantSpaceDelete(mockAPI, user)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, user, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_RestoreSpace deletes then restores a space; a second restore of an already-live
// space is rejected as a 409 (conflict — the space is already live).
func TestHandler_RestoreSpace(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	user := mmmodel.NewId()
	grantSpaceDelete(mockAPI, user)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)

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

// TestHandler_GetSpaceMembers lists a space's members through the backing channel. The members
// list is manage-gated — deliberately stricter than MM channels, since the projection
// carries the per-member capability matrix.
func TestHandler_GetSpaceMembers(t *testing.T) {
	channelID := mmmodel.NewId()
	memberUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil).Maybe()
	mockAPI.On("GetChannelMembers", channelID, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: memberUserID}}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/members", adminID, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.SpaceMember]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.Equal(t, memberUserID, resp.Items[0].UserId)
	require.False(t, resp.HasMore)
}

// TestHandler_GetSpaceMembers_HasMore verifies the probe row: when the requested page comes back
// full and another member exists on the next page, has_more is true and the page is trimmed to
// per_page entries.
func TestHandler_GetSpaceMembers_HasMore(t *testing.T) {
	channelID := mmmodel.NewId()
	firstMember := mmmodel.NewId()
	secondMember := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil).Maybe()
	// Page 0 at size 1 comes back full, so the handler probes the next page's first slot
	// (page-indexed API: index (page+1)*perPage at size 1).
	mockAPI.On("GetChannelMembers", channelID, 0, 1).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: firstMember}}, nil)
	mockAPI.On("GetChannelMembers", channelID, 1, 1).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: secondMember}}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id+"/members?per_page=1", adminID, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp paginatedResponse[*model.SpaceMember]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.Equal(t, firstMember, resp.Items[0].UserId)
	require.True(t, resp.HasMore)
}

// TestHandler_AddSpaceMember adds a member to a space; the caller needs requireSpaceManage
// authority.
func TestHandler_AddSpaceMember(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil).Maybe()
	mockAPI.On("AddChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/members", adminID, map[string]any{
		"user_id": targetUserID,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var member model.SpaceMember
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &member))
	require.Equal(t, targetUserID, member.UserId)
	mockAPI.AssertCalled(t, "AddChannelMember", channelID, targetUserID)
}

// TestHandler_AddSpaceMember_NonManageMemberForbidden verifies an ordinary member without a
// manage grant cannot add another member.
func TestHandler_AddSpaceMember_NonManageMemberForbidden(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()

	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/members", mmmodel.NewId(), map[string]any{
		"user_id": targetUserID,
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_RemoveSpaceMember removes a member from a space; removing a non-self target
// requires requireSpaceManage — self-removal stays membership-gated, covered by
// TestHandler_RemoveSpaceMember_Self.
func TestHandler_RemoveSpaceMember(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
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

	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID, adminID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	mockAPI.AssertCalled(t, "DeleteChannelMember", channelID, targetUserID)
}

// TestHandler_RemoveSpaceMember_NonManageMemberForbidden verifies an ordinary member without a
// manage grant cannot remove another member.
func TestHandler_RemoveSpaceMember_NonManageMemberForbidden(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()

	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID, mmmodel.NewId(), nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_RemoveSpaceMember_Self verifies self-removal stays membership-gated: any member may
// leave the space they belong to, no manage grant required.
func TestHandler_RemoveSpaceMember_Self(t *testing.T) {
	channelID := mmmodel.NewId()
	selfID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: selfID}, {ChannelId: channelID, UserId: mmmodel.NewId()}}, nil)
	mockAPI.On("DeleteChannelMember", channelID, selfID).Return(nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+selfID, selfID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	mockAPI.AssertCalled(t, "DeleteChannelMember", channelID, selfID)
}

// TestHandler_GetTeamSpaces lists the spaces on a team visible to the caller.
func TestHandler_GetTeamSpaces(t *testing.T) {
	channelID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	caller := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", teamID, caller).Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	seedSpaceInTeam(t, h.store, h.db, channelID, teamID)
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
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	// SearchText is derived from the body server-side, so only the body is supplied; a
	// caller-supplied search_text is ignored.
	body := map[string]any{
		"body":         `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"updated text"}]}]}`,
		"base_edit_at": page.EditAt,
	}
	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, body)
	require.Equal(t, http.StatusOK, rec.Code)

	var updated model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Contains(t, updated.Body, "updated text")
	require.Equal(t, "updated text", updated.SearchText)

	// The first update bumped EditAt, so the same baseline is now stale.
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, body)
	require.Equal(t, http.StatusConflict, rec.Code)

	// Every 409 carries the same shape, so a client parses it without branching on the route. This
	// one populates current_page, letting the caller re-baseline without a follow-up read.
	var conflict struct {
		Error       *mmmodel.AppError `json:"error"`
		CurrentPage *model.Page       `json:"current_page"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.NotNil(t, conflict.Error)
	require.Empty(t, conflict.Error.DetailedError, "the conflict body must not carry internal error detail")
	require.NotNil(t, conflict.CurrentPage, "the update conflict must carry the current server page")
	require.Greater(t, conflict.CurrentPage.EditAt, page.EditAt, "current page must carry the advanced baseline")
}

// TestHandler_UpdatePage_BaselineRequired verifies a PATCH that omits base_edit_at without force
// is rejected up front with a clear 400 rather than a misleading 409 conflict. The same app-layer
// baseline gate covers move, move-to-space, and space update.
func TestHandler_UpdatePage_BaselineRequired(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id, user, map[string]any{
		"body":        `{"type":"doc","content":[]}`,
		"search_text": "text",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.optimistic_lock.baseline_required.app_error", appErr.Id)

	// force substitutes for the baseline, so the update applies over the stored row.
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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)
	// Owned by the acting user: a contribute-default member holds only delete_own_page, not
	// delete_page (any).
	page := testutil.MustCreatePage(t, h.store, space.Id, channelID, user, "")

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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)
	otherSpace := seedSpace(t, h.store, h.db, mmmodel.NewId())
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	// Owned by the acting user: move-to-space's source-side own-path requires ownership of the
	// entire moved subtree, and a contribute-default member holds only delete_own_page.
	page := testutil.MustCreatePage(t, h.store, spaceA.Id, channelA, user, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B", ViewAccess: model.ViewAccessOpen})
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	// A second space in a different team (seedSpace randomizes the team id per call).
	spaceB := seedSpace(t, h.store, h.db, mmmodel.NewId())

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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	// Owned by the acting user: move-to-space's source-side own-path requires ownership of the
	// entire moved subtree, and a contribute-default member holds only delete_own_page.
	page := testutil.MustCreatePage(t, h.store, spaceA.Id, channelA, user, "")

	channelB := mmmodel.NewId()
	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: channelB, TeamId: spaceA.TeamId, CreatorId: user, Title: "B", ViewAccess: model.ViewAccessOpen})
	require.NoError(t, err)

	parentID := ""
	for range model.MaxPageDepth {
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	// Owned by the acting user: a contribute-default member holds only delete_own_page, so the
	// same-space move requires ownership of the reparented page before the cycle check is reached.
	root := testutil.MustCreatePage(t, h.store, spaceA.Id, channelA, user, "")
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")
	parentInA := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B", ViewAccess: model.ViewAccessOpen})
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
	space := seedSpace(t, h.store, h.db, channelID)

	parentID := ""
	for range model.MaxPageDepth {
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
	space := seedSpace(t, h.store, h.db, channelID)

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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	channelB := mmmodel.NewId()
	spaceB := seedSpace(t, h.store, h.db, channelB)
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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B", ViewAccess: model.ViewAccessOpen})
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id+"/duplicate", user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_UpdatePage_WrongSpaceIs404 verifies update is scoped to the route's space_id.
func TestHandler_UpdatePage_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, h.db, mmmodel.NewId())

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
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id, user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_RestorePage_WrongSpaceIs404 verifies restore is scoped to the route's space_id.
func TestHandler_RestorePage_WrongSpaceIs404(t *testing.T) {
	h := openTestPlugin(t, nil)
	user := mmmodel.NewId()
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	otherSpace := seedSpace(t, h.store, h.db, mmmodel.NewId())

	// Soft-delete the page so restore has something to act on.
	_, delErr := h.store.DeletePage(page.Id, space.Id, user)
	require.NoError(t, delErr)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+otherSpace.Id+"/pages/"+page.Id+"/restore", user, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_SpaceMembershipRequired verifies that all space- and page-scoped handlers
// reject callers who are not members of a private space's backing channel with 403 Forbidden.
// Private, not the default open fixture: an open space deliberately allows non-member reads
// and auto-joins non-member default-granted writes, so this membership-required
// assertion only holds on a private space.
func TestHandler_SpaceMembershipRequired(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	channelID := mmmodel.NewId()
	stranger := mmmodel.NewId()

	// The stranger passes the team gate (an active member row) so the lack of the read_page
	// channel grant below is what each route exercises. Registered before openTestPlugin: mock
	// matching is by registration order, so a stub added after openTestPlugin would be shadowed
	// by StubDefaultSpacePermissions' permissive read_page-for-anyone catch-all.
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), stranger).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("HasPermissionToChannel", stranger, channelID, mmmodel.PermissionReadPage).
		Return(false)

	h := openTestPlugin(t, mockAPI)

	space, err := h.store.CreateSpace(&model.Space{ChannelId: channelID, TeamId: mmmodel.NewId(), CreatorId: mmmodel.NewId(), Title: "Private Space", ViewAccess: model.ViewAccessPrivate})
	require.NoError(t, err)
	page := seedPage(t, h.store, space.Id, channelID, "")

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
		// Draft + presence handlers.
		{http.MethodPost, "/api/v1/spaces/" + space.Id + "/drafts", map[string]any{"title": "D"}},
		{http.MethodGet, "/api/v1/spaces/" + space.Id + "/drafts", nil},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/draft", map[string]any{"title": "D"}},
		{http.MethodGet, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/draft", nil},
		{http.MethodDelete, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/draft", nil},
		{http.MethodPost, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/draft/publish", nil},
		{http.MethodGet, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/active-editors", nil},
	}
	for _, tc := range cases {
		rec := h.do(t, tc.method, tc.path, stranger, tc.body)
		require.Equal(t, http.StatusForbidden, rec.Code, "expected 403 for %s %s", tc.method, tc.path)
	}
}

// TestHandler_TeamSpacesPrivacy verifies that GET /teams/{team_id}/spaces filters out private
// spaces whose backing channel the caller does not belong to. An open space the caller doesn't
// belong to is deliberately still listed (the list-for-team predicate's open-space branch —
// members' spaces union open spaces the caller can read), so the hidden fixture here must be
// private, not merely non-member, for the exclusion to hold.
func TestHandler_TeamSpacesPrivacy(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	visibleChannelID := mmmodel.NewId()
	hiddenChannelID := mmmodel.NewId()
	visible := seedSpaceInTeam(t, h.store, h.db, visibleChannelID, teamID)
	_, err := h.store.CreateSpace(&model.Space{ChannelId: hiddenChannelID, TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "Hidden", ViewAccess: model.ViewAccessPrivate})
	require.NoError(t, err)
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

	visible := seedSpaceInTeam(t, h.store, h.db, visibleChannelID, teamID)
	// Private, not merely non-member: an open space the caller can't reach as a member is still
	// listed via the list-for-team predicate's open-space branch.
	_, err := h.store.CreateSpace(&model.Space{ChannelId: hidden1ChannelID, TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "Hidden 1", ViewAccess: model.ViewAccessPrivate})
	require.NoError(t, err)
	_, err = h.store.CreateSpace(&model.Space{ChannelId: hidden2ChannelID, TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "Hidden 2", ViewAccess: model.ViewAccessPrivate})
	require.NoError(t, err)

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
		_ = seedSpaceInTeam(t, h.store, h.db, channelID, teamID)
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
	teamID := mmmodel.NewId()
	caller := mmmodel.NewId()

	// Registered before openTestPlugin: mock matching is by registration order, so a stub added
	// after openTestPlugin would be shadowed by its permissive default GetTeamMember catch-all.
	mockAPI.On("GetTeamMember", teamID, caller).
		Return(nil, &mmmodel.AppError{StatusCode: http.StatusNotFound})

	h := openTestPlugin(t, mockAPI)
	_ = seedSpaceInTeam(t, h.store, h.db, mmmodel.NewId(), teamID)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_TeamSpacesTeamLookupError verifies that a non-NotFound backend error from the
// team-membership lookup propagates as 500.
func TestHandler_TeamSpacesTeamLookupError(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	teamID := mmmodel.NewId()
	caller := mmmodel.NewId()

	// Registered before openTestPlugin: mock matching is by registration order, so a stub added
	// after openTestPlugin would be shadowed by its permissive default GetTeamMember catch-all.
	mockAPI.On("GetTeamMember", teamID, caller).
		Return(nil, &mmmodel.AppError{StatusCode: http.StatusInternalServerError})

	h := openTestPlugin(t, mockAPI)
	_ = seedSpaceInTeam(t, h.store, h.db, mmmodel.NewId(), teamID)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestHandler_InvalidJSON returns 400 on a malformed request body.
func TestHandler_InvalidJSON(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)

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
	space := seedSpace(t, h.store, h.db, channelID)
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
	spaceA := seedSpace(t, h.store, h.db, channelA)
	page := seedPage(t, h.store, spaceA.Id, channelA, "")

	spaceB, err := h.store.CreateSpace(&model.Space{ChannelId: mmmodel.NewId(), TeamId: spaceA.TeamId, CreatorId: user, Title: "B", ViewAccess: model.ViewAccessOpen})
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
	space := seedSpace(t, h.store, h.db, channelID)

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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)

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
	space := seedSpace(t, h.store, h.db, channelID)
	// Owned by the acting user: a contribute-default member holds only delete_own_page, not
	// delete_page (any).
	page := testutil.MustCreatePage(t, h.store, space.Id, channelID, user, "")

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
	space := seedSpace(t, h.store, h.db, channelID)
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
	space := seedSpace(t, h.store, h.db, channelID)
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
		Return(&mmmodel.ChannelMember{}, nil).Maybe()
	mockAPI.On("PublishWebSocketEvent", "page_created", mock.Anything, mock.Anything).Return().Once()
	h := openTestPlugin(t, mockAPI)

	space := seedSpace(t, h.store, h.db, channelID)
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

// TestHandler_CreateSpace_RequiresCreateSpacePermission verifies that team membership alone does
// not authorize creating a space in it: a caller without team create_space (and not sysadmin) is
// rejected before any backing-channel side effect. TestHandler_CreateSpace (default-granted via
// StubDefaultSpacePermissions) covers the allowed path.
func TestHandler_CreateSpace_RequiresCreateSpacePermission(t *testing.T) {
	teamID := mmmodel.NewId()
	callerID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", teamID, callerID).Return(&mmmodel.TeamMember{}, nil)
	// Registered before openTestPlugin so it takes precedence over StubDefaultSpacePermissions'
	// permissive create_space catch-all (mock.Mock matches in registration order).
	mockAPI.On("HasPermissionToTeam", callerID, teamID, mmmodel.PermissionCreateSpace).Return(false)
	h := openTestPlugin(t, mockAPI)

	rec := h.do(t, http.MethodPost, "/api/v1/teams/"+teamID+"/spaces", callerID, map[string]any{
		"title": "Nope",
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	mockAPI.AssertNotCalled(t, "CreateChannel")
}

// TestHandler_AddSpaceMember_GuestProjectsReadOnly verifies a guest added to a space is projected
// through the same reverse-role logic as GetSpaceMembers/SetSpaceMemberCapabilities: is_guest
// true and capabilities read_page-only, never the space default.
func TestHandler_AddSpaceMember_GuestProjectsReadOnly(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil).Maybe()
	mockAPI.On("AddChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID, SchemeGuest: true}, nil)
	h := openTestPlugin(t, mockAPI)

	// The contribute-preset default (seeded by seedSpace) must not leak into a guest's projection.
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/members", adminID, map[string]any{
		"user_id": targetUserID,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var member model.SpaceMember
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &member))
	require.Equal(t, targetUserID, member.UserId)
	require.True(t, member.IsGuest)
	require.False(t, member.IsAdmin)
	require.Equal(t, []string{model.CapabilityReadPage}, member.Capabilities)
	require.Empty(t, member.GrantedCapabilities)
}

// TestHandler_RemoveSpaceMember_NonSelfMissingTargetIsNotFound verifies that a manage-gated
// caller removing a non-member target always gets the plain 404 — even on a private space, where
// self-removal would get the existence-hiding 403 instead. A manage-gated caller has already
// proven manage authority over the space, so there is nothing left to existence-hide behind.
func TestHandler_RemoveSpaceMember_NonSelfMissingTargetIsNotFound(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", channelID, targetUserID).
		Return((*mmmodel.ChannelMember)(nil), &mmmodel.AppError{StatusCode: http.StatusNotFound})
	h := openTestPlugin(t, mockAPI)

	space, err := h.store.CreateSpace(&model.Space{ChannelId: channelID, TeamId: mmmodel.NewId(), CreatorId: mmmodel.NewId(), Title: "Private Space", ViewAccess: model.ViewAccessPrivate})
	require.NoError(t, err)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID, adminID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_SetSpaceMemberCapabilities_Forbidden verifies a plain member without a manage grant
// cannot change another member's capabilities.
func TestHandler_SetSpaceMemberCapabilities_Forbidden(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()

	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", mmmodel.NewId(), map[string]any{
		"granted_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_SetSpaceMemberCapabilities_GuestTargetRejected verifies a guest target is rejected
// with 400: guests stay read-only via the scheme's guest role and are never grant-assignable.
func TestHandler_SetSpaceMemberCapabilities_GuestTargetRejected(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID, SchemeGuest: true}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
		"granted_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.space.member.guest_not_assignable.app_error", appErr.Id)
}

// TestHandler_SetSpaceMemberCapabilities_InvalidCapability verifies the granted-capability
// vocabulary is enforced: read_page (the non-grantable baseline) and an unknown token are both
// rejected with 400, before any target lookup.
func TestHandler_SetSpaceMemberCapabilities_InvalidCapability(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	cases := []struct {
		name         string
		capabilities []string
	}{
		{"read_page is not grantable", []string{"read_page"}},
		{"unknown token is rejected", []string{"not_a_real_capability"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
				"granted_capabilities": tc.capabilities,
			})
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// TestHandler_SetSpaceMemberCapabilities_Grant covers the happy path: a manage-granted caller
// grants create_page to a plain member on a read-only-default space, and the response reflects
// both the granted set and the resulting effective capabilities (read_page plus the new grant).
func TestHandler_SetSpaceMemberCapabilities_Grant(t *testing.T) {
	channelID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	// The pre-update target: a plain member, no grant yet. This is the state the guest and
	// last-admin guards read before the write.
	mockAPI.On("GetChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID}, nil)
	// The post-update target, echoing the roles just written — core returns the updated member from
	// this call, and the response is projected from it. Registered before openTestPlugin so it wins
	// over that helper's wildcard catch-all, which matching order would otherwise resolve first.
	mockAPI.On("UpdateChannelMemberRoles", channelID, targetUserID, mock.Anything).
		Return(func(chID, uID, newRoles string) (*mmmodel.ChannelMember, *mmmodel.AppError) {
			return &mmmodel.ChannelMember{ChannelId: chID, UserId: uID, ExplicitRoles: newRoles}, nil
		})
	// Registered before openTestPlugin: the read-only preset must win over
	// StubDefaultChannelScheme's contribute-preset catch-all, and mock matching is by
	// registration order.
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceReadOnly)
	h := openTestPlugin(t, mockAPI)

	space, err := h.store.CreateSpace(&model.Space{ChannelId: channelID, TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "RO Space", ViewAccess: model.ViewAccessOpen})
	require.NoError(t, err)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
		"granted_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var member model.SpaceMember
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &member))
	require.Equal(t, []string{"create_page"}, member.GrantedCapabilities)
	require.ElementsMatch(t, []string{"read_page", "create_page"}, member.Capabilities)
	require.False(t, member.IsAdmin)
}

// TestHandler_SetSpaceMemberCapabilities_OmissionRevokesAdminForbidden verifies that a manage-only
// caller (no channel admin_space grant) cannot demote a current SchemeAdmin target by omitting
// admin_space from the requested set: the escalation guard fires on the current-holder side, not
// only when admin_space is newly requested.
func TestHandler_SetSpaceMemberCapabilities_OmissionRevokesAdminForbidden(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID, SchemeAdmin: true}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
		"granted_capabilities": []string{},
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_SetSpaceMemberCapabilities_SelfTargetForbidden verifies a manage-only caller (no
// channel admin_space grant) cannot change their own capabilities: self-targeting always requires
// the stricter admin gate, regardless of what is requested.
func TestHandler_SetSpaceMemberCapabilities_SelfTargetForbidden(t *testing.T) {
	channelID := mmmodel.NewId()
	callerID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, callerID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+callerID+"/capabilities", callerID, map[string]any{
		"granted_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_SetSpaceMemberCapabilities_LastAdminConflict verifies removing admin_space from the
// space's sole authorized admin is rejected with 409, even for an admin-capable caller.
func TestHandler_SetSpaceMemberCapabilities_LastAdminConflict(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", channelID, targetUserID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: targetUserID, SchemeAdmin: true}, nil)
	// The last-admin guard scans the member list: only the sole target holds SchemeAdmin.
	mockAPI.On("GetChannelMembers", channelID, 0, app.PerPageMaximum).
		Return(mmmodel.ChannelMembers{{ChannelId: channelID, UserId: targetUserID, SchemeAdmin: true}}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
		"granted_capabilities": []string{},
	})
	require.Equal(t, http.StatusConflict, rec.Code)
	// A membership 409 carries the shared conflict envelope like every other 409; current_page is
	// nil because the conflict is not about a page.
	var conflict struct {
		Error       *mmmodel.AppError `json:"error"`
		CurrentPage *model.Page       `json:"current_page"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.NotNil(t, conflict.Error)
	require.Equal(t, "app.space.member.last_admin.app_error", conflict.Error.Id)
	require.Nil(t, conflict.CurrentPage)
}

// TestHandler_SetSpaceMemberCapabilities_EmptyDoesNotDemoteBelowDefault verifies the additive-only
// contract: granting the empty set to a plain member on a contribute-default space clears their
// per-member grant but never demotes their effective capabilities below the space default.
func TestHandler_SetSpaceMemberCapabilities_EmptyDoesNotDemoteBelowDefault(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
		"granted_capabilities": []string{},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var member model.SpaceMember
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &member))
	require.Empty(t, member.GrantedCapabilities)
	require.ElementsMatch(t,
		[]string{model.CapabilityReadPage, model.CapabilityCommentPage, model.CapabilityCreatePage, model.CapabilityEditPage, model.CapabilityDeleteOwnPage},
		member.Capabilities)
}

// TestHandler_SetSpaceMemberCapabilities_PublishesEvent pins space_member_capabilities_updated:
// space/user payload, delivered both to the target user directly and to the backing channel.
func TestHandler_SetSpaceMemberCapabilities_PublishesEvent(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	// Built by hand instead of via newEnabledMockAPI: its wildcard PublishWebSocketEvent stub
	// would swallow the pinned expectation below.
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&mmmodel.Config{FeatureFlags: &mmmodel.FeatureFlags{EnableDocs: true}}).Maybe()
	grantSpaceManage(mockAPI, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("GetChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil).Maybe()
	mockAPI.On("PublishWebSocketEvent", "space_member_capabilities_updated", mock.Anything, mock.Anything).Return()
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID+"/capabilities", adminID, map[string]any{
		"granted_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	payload := map[string]any{"space_id": space.Id, "user_id": targetUserID}
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_capabilities_updated",
		payload, &mmmodel.WebsocketBroadcast{UserId: targetUserID})
	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_capabilities_updated",
		payload, &mmmodel.WebsocketBroadcast{ChannelId: channelID})
}

// stubSpaceSchemeRepoint makes a SetSpaceDefaultCapabilities repoint against channelID observable:
// the returned shared *model.Channel is the one GetChannelOfType hands out, so the repoint's
// in-place SchemeId write is visible to the caller and to every subsequent scheme-resolving read.
// The channel starts pointed at the contribute preset scheme, mirroring seedSpace's default.
//
// Must be registered before openTestPlugin, whose testutil.StubDefaultChannelScheme catch-all would
// otherwise shadow it for this channelID — the same registration-order rule as
// grantSpaceManage/grantSpaceAdmin.
func stubSpaceSchemeRepoint(t *testing.T, mockAPI *plugintest.API, channelID string) *mmmodel.Channel {
	t.Helper()
	return testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
}

// stubSpaceCustomSchemeCreate wires the mock calls the custom-scheme path needs for a non-preset
// default-capability set: CreateScheme returning a scheme whose Name carries the custom prefix,
// plus GetRoleByName and PatchRole for each of its three generated roles. The new scheme's role
// names are registered with testutil so a channel repointed at it resolves them through
// GetSchemeRolesForChannel, the way core does.
//
// Returns the scheme id CreateScheme resolves to, so the caller can assert against it (e.g. the
// channel's post-repoint SchemeId, or a later DeleteScheme call retiring it).
func stubSpaceCustomSchemeCreate(t *testing.T, mockAPI *plugintest.API) string {
	t.Helper()
	testutil.StubPooledSchemeMiss(mockAPI)
	customSchemeID := mmmodel.NewId()
	userRole, adminRole, guestRole := "custom_scheme_user_role", "custom_scheme_admin_role", "custom_scheme_guest_role"
	customScheme := &mmmodel.Scheme{
		Id:                      customSchemeID,
		Name:                    model.SharedSchemeNameForCapabilities([]string{"create_page"}),
		Scope:                   mmmodel.SchemeScopeChannel,
		DefaultChannelUserRole:  userRole,
		DefaultChannelAdminRole: adminRole,
		DefaultChannelGuestRole: guestRole,
	}
	mockAPI.On("CreateScheme", mock.AnythingOfType("*model.Scheme")).Return(customScheme, nil)
	testutil.RegisterSchemeRoles(customSchemeID, guestRole, userRole, adminRole)
	// The generated roles start empty, the way core's CreateScheme leaves them before the plugin
	// patches in the exact sets. StubRole hands back one shared *Role per name so StubPatchRole's
	// mutation is visible to a later getRolePermissionsByName read of the same role.
	testutil.StubRole(mockAPI, userRole, nil)
	testutil.StubRole(mockAPI, adminRole, nil)
	testutil.StubRole(mockAPI, guestRole, nil)
	testutil.StubPatchRole(mockAPI)
	return customSchemeID
}

// assertCustomSchemeRolePermissions pins which permission set each generated role received, so a
// regression that sent the space-admin set to the guest or user role fails instead of passing on a
// single catch-all PatchRole expectation. capabilities is the requested default set.
func assertCustomSchemeRolePermissions(t *testing.T, mockAPI *plugintest.API, capabilities []string) {
	t.Helper()
	expected := map[string][]string{
		"custom_scheme_user_role":  append([]string{model.CapabilityReadPage}, capabilities...),
		"custom_scheme_admin_role": mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions),
		"custom_scheme_guest_role": {model.CapabilityReadPage},
	}
	for roleName, perms := range expected {
		mockAPI.AssertCalled(t, "PatchRole",
			mock.MatchedBy(func(roleID string) bool {
				patchedName, ok := testutil.StubbedRoleName(roleID)
				return ok && patchedName == roleName
			}),
			mock.MatchedBy(func(p *mmmodel.RolePatch) bool {
				return p != nil && p.Permissions != nil && slices.Equal(
					model.NormalizeCapabilitySet(*p.Permissions), model.NormalizeCapabilitySet(perms))
			}))
	}
}

// TestHandler_SetSpaceDefaultCapabilities_Forbidden verifies a manage-only caller (team
// manage_space, no channel admin_space) cannot change a space's default capability set: the
// exposure-policy gate is stricter than ordinary manage.
func TestHandler_SetSpaceDefaultCapabilities_Forbidden(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	userID := mmmodel.NewId()
	// A manage-only grant, never exercised by this gate: RequireSpaceAdminOrSysadmin has no
	// team-manage_space branch, so this stub is optional, not asserted.
	mockAPI.On("HasPermissionToTeam", userID, mock.Anything, mmmodel.PermissionManageSpace).Return(true).Maybe()
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
		"default_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_SetSpaceDefaultCapabilities_Allowed covers the two callers admitted by the
// exposure-policy gate: a channel admin_space grant, and the system manage_system override.
func TestHandler_SetSpaceDefaultCapabilities_Allowed(t *testing.T) {
	t.Run("channel admin_space grant", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()

		mockAPI := newEnabledMockAPI()
		grantSpaceAdmin(mockAPI, channelID, userID)
		stubSpaceSchemeRepoint(t, mockAPI, channelID)
		h := openTestPlugin(t, mockAPI)
		space := seedSpace(t, h.store, h.db, channelID)

		rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
			"default_capabilities": []string{"comment_page"},
		})
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("sysadmin override", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()

		mockAPI := newEnabledMockAPI()
		grantSysadmin(mockAPI, userID)
		stubSpaceSchemeRepoint(t, mockAPI, channelID)
		h := openTestPlugin(t, mockAPI)
		space := seedSpace(t, h.store, h.db, channelID)

		rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
			"default_capabilities": []string{"comment_page"},
		})
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

// TestHandler_SetSpaceDefaultCapabilities_InvalidCapability verifies the default-capability
// vocabulary is enforced: read_page (implicit baseline), admin_space (member-grant-only, never a
// space default), and an unknown token are all rejected with 400.
func TestHandler_SetSpaceDefaultCapabilities_InvalidCapability(t *testing.T) {
	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, userID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	cases := []struct {
		name         string
		capabilities []string
	}{
		{"read_page is implicit, not settable", []string{"read_page"}},
		{"admin_space is member-grant-only", []string{"admin_space"}},
		{"unknown token is rejected", []string{"not_a_real_capability"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
				"default_capabilities": tc.capabilities,
			})
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// TestHandler_SetSpaceDefaultCapabilities_CreatesPooledScheme verifies a non-preset default
// capability set repoints the backing channel at a scheme from the shared pool, minted on first
// use under the name that capability set resolves to, and that the response echoes exactly the
// requested set.
func TestHandler_SetSpaceDefaultCapabilities_CreatesPooledScheme(t *testing.T) {
	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, userID)
	channel := stubSpaceSchemeRepoint(t, mockAPI, channelID)
	customSchemeID := stubSpaceCustomSchemeCreate(t, mockAPI)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
		"default_capabilities": []string{"create_page"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var updated model.SpaceWithAccess
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, []string{"create_page"}, updated.DefaultCapabilities)

	// The pool key is a pure function of the capability set, so the minted scheme's name is exactly
	// the one any other space requesting this set would resolve to.
	mockAPI.AssertCalled(t, "CreateScheme", mock.MatchedBy(func(s *mmmodel.Scheme) bool {
		return s.Name == model.SharedSchemeNameForCapabilities([]string{"create_page"})
	}))
	require.NotNil(t, channel.SchemeId)
	require.Equal(t, customSchemeID, *channel.SchemeId, "the channel must be repointed at the pooled scheme")
	assertCustomSchemeRolePermissions(t, mockAPI, []string{"create_page"})
	// Exactly the three generated roles (user/admin/guest) are patched — no role skipped, none
	// double-patched. assertCustomSchemeRolePermissions pins the per-role content; this pins the count.
	mockAPI.AssertNumberOfCalls(t, "PatchRole", 3)
}

// TestHandler_SetSpaceDefaultCapabilities_ReusesPooledScheme pins the property the shared pool
// exists for: a capability set resolves to one scheme forever. Switching a space to a non-preset
// set mints a pooled scheme, switching away to a preset leaves it in place, and switching back
// resolves to that same scheme rather than minting a second one carrying identical permissions.
func TestHandler_SetSpaceDefaultCapabilities_ReusesPooledScheme(t *testing.T) {
	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, userID)
	channel := stubSpaceSchemeRepoint(t, mockAPI, channelID)
	testutil.StubSchemePool(mockAPI)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	setDefaults := func(capabilities []string) {
		t.Helper()
		rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
			"default_capabilities": capabilities,
		})
		require.Equal(t, http.StatusOK, rec.Code)
	}

	setDefaults([]string{"create_page"})
	require.NotNil(t, channel.SchemeId)
	pooledSchemeID := *channel.SchemeId
	require.NotEqual(t, testutil.PresetSchemeID(mmmodel.SchemeNameSpaceContribute), pooledSchemeID)

	setDefaults([]string{"comment_page", "create_page", "edit_page", "delete_own_page"})
	require.NotNil(t, channel.SchemeId)
	require.Equal(t, testutil.PresetSchemeID(mmmodel.SchemeNameSpaceContribute), *channel.SchemeId,
		"a set matching a preset must repoint at the shared preset scheme")

	setDefaults([]string{"create_page"})
	require.NotNil(t, channel.SchemeId)
	require.Equal(t, pooledSchemeID, *channel.SchemeId,
		"returning to a capability set must resolve to the scheme already pooled for it")

	// One scheme minted across all three switches, and none deleted: a pooled scheme is shared, so
	// no space owns it and no repoint retires it.
	mockAPI.AssertNumberOfCalls(t, "CreateScheme", 1)
	mockAPI.AssertNotCalled(t, "DeleteScheme", mock.Anything)
}

// TestHandler_SetSpaceDefaultCapabilities_ResubmitCurrentSetIsNoOp verifies that resubmitting the
// space's current default capability set (deduplicated and reordered) is a no-op: the backing
// channel's SchemeId does not change.
func TestHandler_SetSpaceDefaultCapabilities_ResubmitCurrentSetIsNoOp(t *testing.T) {
	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, userID)
	// stubSpaceSchemeRepoint's channel already starts pointed at the contribute preset scheme,
	// the same default seedSpace/MustCreateSpaceWithScheme use, so the requested set — which
	// normalizes to that same preset — already matches the live scheme with no further setup.
	channel := stubSpaceSchemeRepoint(t, mockAPI, channelID)
	contributeID := testutil.PresetSchemeID(mmmodel.SchemeNameSpaceContribute)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/default-capabilities", userID, map[string]any{
		"default_capabilities": []string{"edit_page", "comment_page", "comment_page", "create_page", "delete_own_page"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	mockAPI.AssertNotCalled(t, "UpdateChannel", mock.Anything)
	require.NotNil(t, channel.SchemeId)
	require.Equal(t, contributeID, *channel.SchemeId, "resubmitting the current set must not repoint the channel")
}

// TestHandler_UpdateSpace_ViewAccessRequiresAdmin verifies that a manage-only caller (no channel
// admin_space grant) cannot change ViewAccess, and that a rejected ViewAccess change fails the
// whole request — the title in the same patch is not applied either.
func TestHandler_UpdateSpace_ViewAccessRequiresAdmin(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	userID := mmmodel.NewId()
	grantSpaceManage(mockAPI, userID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())
	require.Equal(t, model.ViewAccessOpen, space.ViewAccess)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, userID, map[string]any{
		"view_access":        "private",
		"title":              "x",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusForbidden, rec.Code)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "Test Space", got.Title, "the whole request must fail together; title must not be applied")
	require.Equal(t, model.ViewAccessOpen, got.ViewAccess)
}

// TestHandler_UpdateSpace_ViewAccessForceRejected verifies force=true is rejected on a ViewAccess
// change: the exposure-policy escalation check must always run, never be skipped by force.
func TestHandler_UpdateSpace_ViewAccessForceRejected(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	userID := mmmodel.NewId()
	grantSpaceManage(mockAPI, userID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, mmmodel.NewId())

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, userID, map[string]any{
		"view_access": "open",
		"force":       true,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var appErr mmmodel.AppError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
	require.Equal(t, "app.space.update.view_access_force.app_error", appErr.Id)
}

// TestHandler_UpdateSpace_ViewAccessAdminSucceedsMemberRetained verifies an admin-capable caller
// can flip a space from open to private, and that an existing member can still read the space
// afterwards (privatizing does not shed members).
func TestHandler_UpdateSpace_ViewAccessAdminSucceedsMemberRetained(t *testing.T) {
	channelID := mmmodel.NewId()
	adminID := mmmodel.NewId()
	memberID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, adminID)
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("AddChannelMember", channelID, memberID).
		Return(&mmmodel.ChannelMember{ChannelId: channelID, UserId: memberID}, nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/members", adminID, map[string]any{"user_id": memberID})
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, adminID, map[string]any{
		"view_access":        "private",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var updated model.Space
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, model.ViewAccessPrivate, updated.ViewAccess)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, memberID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHandler_UpdateSpace_ViewAccessInvalidValue verifies an admin-capable caller supplying a
// nonsensical view_access value is rejected with 400.
func TestHandler_UpdateSpace_ViewAccessInvalidValue(t *testing.T) {
	channelID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceAdmin(mockAPI, channelID, adminID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)

	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id, adminID, map[string]any{
		"view_access":        "bogus",
		"expected_update_at": space.UpdateAt,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandler_OpenSpaceReadFallthrough_VisibleWithReadPublicChannel verifies a non-member of an
// open space with team read_public_channel sees it both in the team space listing and via the
// single-space read, the latter carrying only the read_page capability (never a hypothetical
// post-join grant).
func TestHandler_OpenSpaceReadFallthrough_VisibleWithReadPublicChannel(t *testing.T) {
	teamID := mmmodel.NewId()
	channelID := mmmodel.NewId()
	caller := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	// Registered before openTestPlugin: mock matching is by registration order, so this specific
	// stub (simulating "not a member") takes precedence over StubDefaultSpacePermissions'
	// permissive read_page-for-anyone catch-all.
	mockAPI.On("HasPermissionToChannel", caller, channelID, mmmodel.PermissionReadPage).Return(false)
	mockAPI.On("GetTeamMember", teamID, caller).Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpaceInTeam(t, h.store, h.db, channelID, teamID)
	require.Equal(t, model.ViewAccessOpen, space.ViewAccess)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list paginatedResponse[*model.Space]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	require.Equal(t, space.Id, list.Items[0].Id)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var wrapper model.SpaceWithAccess
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &wrapper))
	require.Equal(t, []string{model.CapabilityReadPage}, wrapper.Capabilities)
}

// TestHandler_OpenSpaceReadFallthrough_HiddenWithoutReadPublicChannel verifies the complementary
// case: the same non-member caller, but without team read_public_channel, sees the open space
// neither in the team listing nor via the single-space read (403).
func TestHandler_OpenSpaceReadFallthrough_HiddenWithoutReadPublicChannel(t *testing.T) {
	teamID := mmmodel.NewId()
	channelID := mmmodel.NewId()
	caller := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	// Registered before openTestPlugin: same registration-order rule as above.
	mockAPI.On("HasPermissionToChannel", caller, channelID, mmmodel.PermissionReadPage).Return(false)
	mockAPI.On("HasPermissionToTeam", caller, teamID, mmmodel.PermissionReadPublicChannel).Return(false)
	mockAPI.On("GetTeamMember", teamID, caller).Return(&mmmodel.TeamMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	space := seedSpaceInTeam(t, h.store, h.db, channelID, teamID)

	rec := h.do(t, http.MethodGet, "/api/v1/teams/"+teamID+"/spaces", caller, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list paginatedResponse[*model.Space]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Empty(t, list.Items)

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+space.Id, caller, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandler_DraftWritesRequireContributeCapability verifies the draft-write gate: a member who
// can read the space but holds neither create_page nor edit_page may still read their own drafts
// and the presence snapshot, but cannot autosave, reserve a new page id, or publish. A draft is a
// pending page, so holding one at all requires the authority to contribute pages here.
func TestHandler_DraftWritesRequireContributeCapability(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	channelID := mmmodel.NewId()
	reader := mmmodel.NewId()

	// Registered before openTestPlugin so these deny-stubs win over StubDefaultSpacePermissions'
	// permissive catch-alls (mock matching is by registration order).
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), reader).Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("HasPermissionToChannel", reader, channelID, mmmodel.PermissionReadPage).Return(true)
	mockAPI.On("HasPermissionToChannel", reader, channelID, mmmodel.PermissionCreatePage).Return(false)
	mockAPI.On("HasPermissionToChannel", reader, channelID, mmmodel.PermissionEditPage).Return(false)

	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	testutil.MustAddChannelMember(t, h.db, channelID, reader)

	// Seeded through the store, not the API: the publish gate must be what rejects the request,
	// and without an existing draft publish would 404 on its idempotency guard before reaching it.
	_, _, err := h.store.UpsertDraft(&model.Draft{
		UserId: reader, SpaceId: space.Id, PageId: page.Id, Title: "Pending", BaseEditAt: page.EditAt,
	}, nil, nil, nil)
	require.NoError(t, err)

	denied := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/v1/spaces/" + space.Id + "/drafts", map[string]any{"title": "D"}},
		{http.MethodPatch, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/draft", map[string]any{"title": "D"}},
		{http.MethodPost, "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/draft/publish", nil},
	}
	for _, tc := range denied {
		rec := h.do(t, tc.method, tc.path, reader, tc.body)
		require.Equal(t, http.StatusForbidden, rec.Code, "expected 403 for %s %s", tc.method, tc.path)
	}

	// Reads stay open to a plain reader: the draft routes are own-scoped in the store, so reading
	// carries no exposure beyond the space read the caller already holds.
	allowed := []string{
		"/api/v1/spaces/" + space.Id + "/drafts",
		"/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/active-editors",
	}
	for _, path := range allowed {
		rec := h.do(t, http.MethodGet, path, reader, nil)
		require.Equal(t, http.StatusOK, rec.Code, "expected 200 for GET %s", path)
	}
}

// TestHandler_PublishGatesOnTargetKind verifies that publish resolves its permission from the
// target the draft lands on, not from the route: a member holding create_page but not edit_page
// may publish a brand-new page, and is refused when the same call would overwrite a page that is
// already live. The distinction is derived inside PublishPageDraft, where the target row is
// classified.
func TestHandler_PublishGatesOnTargetKind(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	channelID := mmmodel.NewId()
	author := mmmodel.NewId()

	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), author).Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("HasPermissionToChannel", author, channelID, mmmodel.PermissionReadPage).Return(true)
	mockAPI.On("HasPermissionToChannel", author, channelID, mmmodel.PermissionCreatePage).Return(true)
	mockAPI.On("HasPermissionToChannel", author, channelID, mmmodel.PermissionEditPage).Return(false)

	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, h.db, channelID)
	testutil.MustAddChannelMember(t, h.db, channelID, author)

	// New-page path: reserve an id, autosave into it, publish. create_page alone carries this.
	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/drafts", author, map[string]any{"title": "New"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var reserved model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reserved))

	rec = h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+reserved.PageId+"/draft/publish", author, nil)
	require.Equal(t, http.StatusCreated, rec.Code, "create_page must carry a new-page publish")
	var published model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &published))

	// Edit path: the same author now has a draft over the page they just published. Publishing it
	// updates live content, which needs edit_page. base_edit_at baselines the draft against the
	// page just published, so the publish below fails on authority rather than on a stale baseline.
	rec = h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+reserved.PageId+"/draft", author,
		map[string]any{"title": "Revised", "base_edit_at": published.EditAt})
	require.Equal(t, http.StatusOK, rec.Code, "the draft write itself only needs the create-or-edit pair")

	rec = h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+reserved.PageId+"/draft/publish", author, nil)
	require.Equal(t, http.StatusForbidden, rec.Code, "publishing over a live page must require edit_page")
}
