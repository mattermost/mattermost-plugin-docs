// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package app_test contains integration-style tests for the Docs plugin service layer.
// Tests require a real Postgres database; the DSN comes from TEST_DATABASE_POSTGRESQL_DSN,
// defaulting to the standard local dev Postgres. They never skip —
// a missing database fails the run rather than passing on a skip.
package app_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// requireStoreDeletePage soft-deletes a page directly at the store layer, discarding the
// returned channel id.
func requireStoreDeletePage(t *testing.T, s *store.Store, pageID, spaceID, userID string) {
	t.Helper()
	_, err := s.DeletePage(pageID, spaceID, userID)
	require.NoError(t, err)
}

// --- harness ---

// testHarness holds both the service and the underlying store so tests can seed
// data directly without opening a second DB connection.
type testHarness struct {
	svc   *app.Service
	store *store.Store
	// db is the schema-scoped handle, exposed so tests can seed states the public
	// store API forbids (e.g. version snapshots: OriginalId set + soft-deleted).
	db *sql.DB
}

// openTestService opens an isolated Postgres schema, runs migrations, and returns
// the harness. The schema is dropped by t.Cleanup.
func openTestService(t *testing.T) *testHarness {
	t.Helper()

	s, db := testutil.OpenTestStore(t)

	// A permissive client rather than nil: most tests here seed data through the store and never
	// touch it, but the ones that publish a draft go through PublishPageDraft's own permission
	// gate, which needs a wired client. The stub grants the ordinary contribute-member set, so a
	// test asserting publish behaviour is not also asserting authorization.
	mockAPI := &plugintest.API{}
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })
	testutil.StubDefaultSpacePermissions(mockAPI)
	mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
	// The page-write gate reads the acting user to hold guests to read_page. Defaults to an
	// ordinary (non-guest) user; a test exercising the guest refusal registers its own stub first.
	mockAPI.On("GetUser", mock.Anything).Return(&mmmodel.User{}, nil).Maybe()
	// With a client wired, the WS publishes and channel side-effects these paths perform are no
	// longer no-ops. Tests that assert on specific events register their own expectations against
	// their own mock.
	mockAPI.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("GetChannelMembers", mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(mmmodel.ChannelMembers{}, nil).Maybe()
	mockAPI.On("DeleteChannel", mock.Anything).Return(nil).Maybe()
	mockAPI.On("RestoreChannel", mock.Anything).Return(nil).Maybe()
	// A 404, not a nil channel with a nil error: pluginapi maps this to ErrNotFound, which the
	// backing-channel readers branch on. A nil/nil pair is a state core never returns and would
	// send them past that branch into a dereference.
	mockAPI.On("GetChannelOfType", mock.Anything, mock.Anything).
		Return(nil, &mmmodel.AppError{Id: "app.channel.get.app_error", StatusCode: http.StatusNotFound}).Maybe()
	svc := app.New(s, nil, pluginapi.NewClient(mockAPI, nil))

	return &testHarness{svc: svc, store: s, db: db}
}

// helpers to create test data directly on the store (bypassing business logic where the
// higher-level Create methods need channel/space state that may not exist).
func mustCreateSpace(t *testing.T, s *store.Store, channelID string) *model.Space {
	t.Helper()
	return seedSpaceForTeam(t, s, channelID, mmmodel.NewId())
}

func mustCreatePage(t *testing.T, s *store.Store, spaceID, channelID, userID, parentID string) *model.Page {
	t.Helper()
	return testutil.MustCreatePage(t, s, spaceID, channelID, userID, parentID)
}

func TestServiceGetSpace(t *testing.T) {
	h := openTestService(t)

	t.Run("found", func(t *testing.T) {
		channelID := mmmodel.NewId()
		saved := mustCreateSpace(t, h.store, channelID)

		got, err := h.svc.GetSpace(saved.Id)
		require.Nil(t, err)
		require.Equal(t, saved.Id, got.Id)
		require.Equal(t, "Test Space", got.Title)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := h.svc.GetSpace(mmmodel.NewId())
		require.NotNil(t, err)
		require.Equal(t, 404, err.StatusCode)
	})
}

// TestServiceCreatePageParentDifferentSpace verifies that a parent belonging to a different
// space is rejected, preventing cross-space hierarchy corruption.
func TestServiceCreatePageParentDifferentSpace(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)

	// Create a real second space (different channel) and seed the rogue parent into it.
	// Using a real space avoids a foreign-key violation (Pages.SpaceId references Spaces)
	// and produces a page whose SpaceId is genuinely different from `space.Id`.
	otherChannelID := mmmodel.NewId()
	otherSpace := mustCreateSpace(t, h.store, otherChannelID)
	rogueParent := mustCreatePage(t, h.store, otherSpace.Id, otherChannelID, userID, "")

	_, err := h.svc.CreatePage(space.Id, rogueParent.Id, "Child", "", userID)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.invalid_parent.app_error", err.Id)
}

// TestServiceUpdatePageNotFound verifies the 404 path: a well-formed but absent page ID.
func TestServiceUpdatePageNotFound(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.UpdatePage(mmmodel.NewId(), mmmodel.NewId(), &model.PagePatch{Title: mmmodel.NewPointer("Title")}, new(int64(0)), false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusNotFound, err.StatusCode)
}

func TestServiceUpdatePageInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.UpdatePage("not-an-id", mmmodel.NewId(), &model.PagePatch{Title: mmmodel.NewPointer("Title")}, new(int64(0)), false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
}

func TestServiceDeleteSpace(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	saved := mustCreateSpace(t, h.store, channelID)

	require.Nil(t, h.svc.DeleteSpace(saved))

	_, err := h.svc.GetSpace(saved.Id)
	require.NotNil(t, err)
	require.Equal(t, 404, err.StatusCode)
}

func TestServiceGetPage(t *testing.T) {
	h := openTestService(t)

	t.Run("found", func(t *testing.T) {
		channelID := mmmodel.NewId()
		space := mustCreateSpace(t, h.store, channelID)
		created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

		got, err := h.svc.GetPage(created.Id)
		require.Nil(t, err)
		require.Equal(t, created.Id, got.Id)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := h.svc.GetPage(mmmodel.NewId())
		require.NotNil(t, err)
		require.Equal(t, 404, err.StatusCode)
	})
}

func TestServiceUpdatePage(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	t.Run("first update succeeds", func(t *testing.T) {
		// First update — baseEditAt matches (0 since freshly created)
		updated, err := h.svc.UpdatePage(
			created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Locked Title"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, new(created.EditAt), false, userID,
		)
		require.Nil(t, err)
		require.Equal(t, "Locked Title", updated.Title)

		// Second update with stale baseEditAt — must conflict
		_, err2 := h.svc.UpdatePage(
			updated.Id, updated.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Conflict Title"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, new(created.EditAt), false, userID,
		)
		require.NotNil(t, err2)
		require.Equal(t, 409, err2.StatusCode)
	})
}

// TestServiceUpdatePageSearchTextWithoutBody covers the update-path guard rejecting a
// SearchText change with no accompanying Body change: SearchText is the body's plain-text
// projection, so the two must be patched together (both or neither).
func TestServiceUpdatePageSearchTextWithoutBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	_, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{SearchText: mmmodel.NewPointer("some text")}, new(created.EditAt), false, userID,
	)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.patch.search_text_body_mismatch.app_error", err.Id)
}

// TestServiceUpdatePageBodyDerivesSearchText verifies that a Body-only patch succeeds and its
// SearchText is derived server-side from the body, keeping the search index in sync without the
// caller having to supply it.
func TestServiceUpdatePageBodyDerivesSearchText(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	updated, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer("new body")}, new(created.EditAt), false, userID,
	)
	require.Nil(t, err)
	require.Equal(t, "new body", updated.SearchText)
}

// TestServiceUpdatePageSearchTextIgnoredOnBodyClear verifies that clearing Body to "" derives an
// empty SearchText regardless of the caller-supplied value — SearchText is the body's projection.
func TestServiceUpdatePageSearchTextIgnoredOnBodyClear(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	updated, err := h.svc.UpdatePage(
		created.Id, created.SpaceId,
		&model.PagePatch{Body: mmmodel.NewPointer(""), SearchText: mmmodel.NewPointer("ignored")},
		new(created.EditAt), false, userID,
	)
	require.Nil(t, err)
	require.Equal(t, "", updated.SearchText)
	require.Equal(t, "", updated.Body)
}

// TestServiceUpdatePageClearSearchTextAlone verifies the coupling rule: clearing SearchText
// (to "") with no Body in the same patch is rejected, because SearchText and Body must move
// together. Clearing both (Body="" and SearchText="") is the supported way to empty a page.
func TestServiceUpdatePageClearSearchTextAlone(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	_, err := h.svc.UpdatePage(
		created.Id, created.SpaceId,
		&model.PagePatch{SearchText: mmmodel.NewPointer("")},
		new(created.EditAt), false, userID,
	)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.patch.search_text_body_mismatch.app_error", err.Id)
}

func TestServiceUpdatePageForce(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	// First update to advance EditAt. Assert it succeeds: if it silently failed,
	// EditAt would stay at created.EditAt and the force path below would pass
	// trivially (nothing to override), proving nothing.
	first, firstErr := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("First"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, new(created.EditAt), false, userID,
	)
	require.Nil(t, firstErr, "first update must succeed to establish a stale baseEditAt")
	require.Greater(t, first.EditAt, created.EditAt, "EditAt must advance after the first update")

	// Force update with the now-stale baseEditAt must still succeed.
	forced, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Forced"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, new(created.EditAt), true, userID,
	)
	require.Nil(t, err)
	require.Equal(t, "Forced", forced.Title)
}

func TestServiceGetPageInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.GetPage("not-an-id")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
}

func TestServiceDeleteSpaceInvalidID(t *testing.T) {
	h := openTestService(t)

	err := h.svc.DeleteSpace(nil)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
}

func TestServiceUpdatePageInvalidUserID(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	t.Run("UpdatePage rejects malformed userID", func(t *testing.T) {
		_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Title")}, new(created.EditAt), false, "not-an-id")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})
}

func TestServiceGetPageChildren(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

	children, _, err := h.svc.GetPageChildren(parent.Id, space.Id, 0, 0)
	require.Nil(t, err)
	require.Len(t, children, 1)
	require.Equal(t, child.Id, children[0].Id)
}

func TestServiceGetSpacePages(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	for range 3 {
		mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	}

	pages, _, err := h.svc.GetSpacePages(space, 0, 0)
	require.Nil(t, err)
	require.Len(t, pages, 3)

	t.Run("pages are isolated by space, not by reused channel", func(t *testing.T) {
		// Soft-delete the first space (which cascades its 3 pages to soft-deleted),
		// then create a second live space on the SAME channel. GetSpacePages for the
		// new space must not leak the old space's pages.
		require.Nil(t, h.svc.DeleteSpace(space))

		space2 := mustCreateSpace(t, h.store, channelID)
		mustCreatePage(t, h.store, space2.Id, channelID, userID, "")

		pages2, _, err := h.svc.GetSpacePages(space2, 0, 0)
		require.Nil(t, err)
		require.Len(t, pages2, 1, "new space must only see its own page, not the prior space's pages on the reused channel")
		require.Equal(t, space2.Id, pages2[0].SpaceId)
	})
}

// TestServiceGetSpacePagesInvalidID verifies that GetSpacePages with a nil space
// returns 400 with the expected error key.
func TestServiceGetSpacePagesInvalidID(t *testing.T) {
	h := openTestService(t)

	_, _, err := h.svc.GetSpacePages(nil, 0, 0)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.space.get_pages.invalid_space_id.app_error", err.Id)
}

func TestServiceGetTeamSpaces(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	testutil.MustAddTeamMember(t, h.db, teamID, userID, 0)
	for range 2 {
		sp := &model.Space{ChannelId: mmmodel.NewId(), TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "Test Space", ViewAccess: model.ViewAccessPrivate}
		_, err := h.store.CreateSpace(sp)
		require.NoError(t, err)
		testutil.MustAddChannelMember(t, h.db, sp.ChannelId, userID)
	}

	spaces, _, err := h.svc.GetSpacesForTeam(teamID, userID, 0, 0)
	require.Nil(t, err)
	require.Len(t, spaces, 2)
}

// TestServiceGetTeamSpacesInvalidID verifies a malformed team id is rejected with 400.
func TestServiceGetTeamSpacesInvalidID(t *testing.T) {
	h := openTestService(t)
	_, _, err := h.svc.GetSpacesForTeam("not-a-valid-id", mmmodel.NewId(), 0, 0)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.space.get_for_team.invalid_team_id.app_error", err.Id)
}

// TestServiceCreatePageDerivesChannelFromSpace verifies the page's ChannelId is
// derived from its space's backing channel, not supplied by the caller.
func TestServiceCreatePageDerivesChannelFromSpace(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	created, err := h.svc.CreatePage(space.Id, "", "My Page", "", userID)
	require.Nil(t, err)
	require.Equal(t, "My Page", created.Title)
	require.Equal(t, space.Id, created.SpaceId)
	require.Equal(t, channelID, created.ChannelId)
}

func TestServiceCreatePage(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	t.Run("rejects invalid space id", func(t *testing.T) {
		_, err := h.svc.CreatePage("not-a-valid-id", "", "Title", "", userID)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.create.invalid_space_id.app_error", err.Id)
	})

	t.Run("rejects invalid user id", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, "", "Title", "", "not-a-valid-id")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.create.invalid_user_id.app_error", err.Id)
	})

	t.Run("rejects empty title", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, "", "   ", "", userID)
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects title too long", func(t *testing.T) {
		long := strings.Repeat("x", model.PageTitleMaxRunes+1)
		_, err := h.svc.CreatePage(space.Id, "", long, "", userID)
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects nonexistent parent", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, mmmodel.NewId(), "Title", "", userID)
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects parent in a different space", func(t *testing.T) {
		otherChannelID := mmmodel.NewId()
		otherSpace := mustCreateSpace(t, h.store, otherChannelID)
		parent := mustCreatePage(t, h.store, otherSpace.Id, otherChannelID, userID, "")
		_, err := h.svc.CreatePage(space.Id, parent.Id, "Title", "", userID)
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects exceeding max depth", func(t *testing.T) {
		// Own channel/space so the deep chain does not pollute the shared fixture.
		depthChannelID := mmmodel.NewId()
		depthSpace := mustCreateSpace(t, h.store, depthChannelID)
		// Build a full-depth chain (root at depth 1 up to MaxPageDepth); the next
		// child would be at depth MaxPageDepth+1 and must be rejected.
		parentID := ""
		for range model.MaxPageDepth {
			p, err := h.svc.CreatePage(depthSpace.Id, parentID, "d", "", userID)
			require.Nil(t, err)
			parentID = p.Id
		}
		_, err := h.svc.CreatePage(depthSpace.Id, parentID, "too deep", "", userID)
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("creates a valid page", func(t *testing.T) {
		created, err := h.svc.CreatePage(space.Id, "", "My Page", "", userID)
		require.Nil(t, err)
		require.Equal(t, "My Page", created.Title)
	})
}

// TestServiceGetSpaceInvalidID verifies that GetSpace with a malformed ID returns 400
// with the expected error key.
func TestServiceGetSpaceInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.GetSpace("not-a-valid-id")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.space.get.invalid_id.app_error", err.Id)
}

// TestServiceGetPageEmptyID verifies that GetPage with an empty string returns 400.
func TestServiceGetPageEmptyID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.GetPage("")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.get.invalid_id.app_error", err.Id)
}

// TestServiceUpdatePageNothingToUpdate verifies that a patch with no fields set is rejected
// with 400, rather than advancing edit metadata for a no-op.
func TestServiceUpdatePageNothingToUpdate(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{}, new(created.EditAt), false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.patch.nothing_to_update.app_error", err.Id)
}

// TestServiceUpdatePageOversizedBody verifies that the update path rejects a body that
// exceeds PageBodyMaxBytes with 400.
func TestServiceUpdatePageOversizedBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	oversized := strings.Repeat("x", model.PageBodyMaxBytes+1)
	_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer(oversized), SearchText: mmmodel.NewPointer("")}, new(created.EditAt), false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.invalid_content.app_error", err.Id)
}

// TestServiceUpdatePageOversizedSearchTextIgnored verifies a caller-supplied oversized SearchText
// is harmless: it is ignored and SearchText is derived from the (small) body instead.
func TestServiceUpdatePageOversizedSearchTextIgnored(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	oversized := strings.Repeat("x", model.PageSearchTextMaxBytes+1)
	updated, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Title"), Body: mmmodel.NewPointer("body"), SearchText: mmmodel.NewPointer(oversized)}, new(created.EditAt), false, mmmodel.NewId())
	require.Nil(t, err)
	require.Equal(t, "body", updated.SearchText)
}

// TestServiceCreatePageOversizedBody verifies that CreatePage rejects a body that exceeds
// PageBodyMaxBytes with 400.
func TestServiceCreatePageOversizedBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)

	oversized := strings.Repeat("x", model.PageBodyMaxBytes+1)
	_, err := h.svc.CreatePage(space.Id, "", "Title", oversized, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.invalid_content.app_error", err.Id)
}

// TestServiceUpdatePageCanSetEmptyBody verifies the patch contract: a non-nil empty Body
// explicitly clears the body, which the old ""-sentinel API could not express.
func TestServiceUpdatePageCanSetEmptyBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	withBody, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("text")}, new(created.EditAt), false, userID,
	)
	require.Nil(t, err)
	require.NotEqual(t, "", withBody.Body)

	emptied, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer(""), SearchText: mmmodel.NewPointer("")}, new(withBody.EditAt), false, userID,
	)
	require.Nil(t, err)
	require.Equal(t, "", emptied.Body, "an explicit empty Body must clear the stored body")
}

// TestServiceCreatePageSearchTextDerivedFromBody verifies CreatePage ignores a caller-supplied
// SearchText and derives it from the body — so an empty body yields an empty SearchText.
func TestServiceCreatePageSearchTextDerivedFromBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)

	created, err := h.svc.CreatePage(space.Id, "", "Title", "", mmmodel.NewId())
	require.Nil(t, err)
	require.Equal(t, "", created.SearchText)
}

// TestServiceGetPageWithDeleted verifies the with-deleted reader returns a soft-deleted
// page that the live reader hides, and rejects an invalid id.
func TestServiceGetPageWithDeleted(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	actorID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, actorID, "")
	requireStoreDeletePage(t, h.store, created.Id, created.SpaceId, actorID)

	t.Run("live get fails, with-deleted get returns the page", func(t *testing.T) {
		_, liveErr := h.svc.GetPage(created.Id)
		require.NotNil(t, liveErr, "live get should fail for a deleted page")
		require.Equal(t, http.StatusNotFound, liveErr.StatusCode)

		got, err := h.svc.GetPageWithDeleted(created.Id)
		require.Nil(t, err)
		require.Equal(t, created.Id, got.Id)
		require.NotZero(t, got.DeleteAt)
	})

	t.Run("invalid id is rejected", func(t *testing.T) {
		_, err := h.svc.GetPageWithDeleted("not-an-id")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("version snapshot is treated as not found", func(t *testing.T) {
		snap := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")
		// Turn it into a version snapshot: OriginalId set, soft-deleted (chk_docs_page_snapshot_deleted).
		_, rawErr := h.db.Exec(
			"UPDATE DOCS_Page SET OriginalId = $2, DeleteAt = $3 WHERE Id = $1",
			snap.Id, mmmodel.NewId(), mmmodel.GetMillis())
		require.NoError(t, rawErr)

		_, err := h.svc.GetPageWithDeleted(snap.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusNotFound, err.StatusCode)
	})
}

// TestServiceDeletePage verifies the app delete path: a deleted page 404s on live get, its
// children are promoted to the page's parent, and missing/invalid ids map to 404/400.
func TestServiceDeletePage(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	t.Run("deletes a page so the live get returns 404", func(t *testing.T) {
		created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

		require.Nil(t, h.svc.DeletePage(created.Id, created.SpaceId, userID))

		_, err := h.svc.GetPage(created.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusNotFound, err.StatusCode)
	})

	t.Run("reparents live children to the deleted page's parent", func(t *testing.T) {
		parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

		require.Nil(t, h.svc.DeletePage(parent.Id, parent.SpaceId, userID))

		gotChild, err := h.svc.GetPage(child.Id)
		require.Nil(t, err)
		require.Equal(t, parent.ParentId, gotChild.ParentId)
	})

	t.Run("missing page returns 404", func(t *testing.T) {
		err := h.svc.DeletePage(mmmodel.NewId(), space.Id, userID)
		require.NotNil(t, err)
		require.Equal(t, http.StatusNotFound, err.StatusCode)
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		err := h.svc.DeletePage("not-an-id", space.Id, userID)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})
}

// TestServiceRestorePage verifies the app restore path: the page becomes live with promoted
// children left in place, a non-deleted page → 400, and an invalid id → 400.
func TestServiceRestorePage(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	t.Run("restores a soft-deleted page", func(t *testing.T) {
		created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		requireStoreDeletePage(t, h.store, created.Id, created.SpaceId, userID)

		restored, err := h.svc.RestorePage(created.Id, created.SpaceId, userID)
		require.Nil(t, err)
		require.Zero(t, restored.DeleteAt)

		got, err := h.svc.GetPage(created.Id)
		require.Nil(t, err)
		require.Zero(t, got.DeleteAt)
	})

	t.Run("leaves promoted children in place on restore", func(t *testing.T) {
		parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

		require.Nil(t, h.svc.DeletePage(parent.Id, parent.SpaceId, userID))
		_, err := h.svc.RestorePage(parent.Id, parent.SpaceId, userID)
		require.Nil(t, err)

		// Matching Confluence: the promoted child stays put after restore, not pulled back.
		gotChild, err := h.svc.GetPage(child.Id)
		require.Nil(t, err)
		require.Equal(t, parent.ParentId, gotChild.ParentId)
	})

	t.Run("a non-deleted page returns 409", func(t *testing.T) {
		live := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		_, err := h.svc.RestorePage(live.Id, live.SpaceId, userID)
		require.NotNil(t, err)
		require.Equal(t, http.StatusConflict, err.StatusCode)
		require.Equal(t, "app.page.restore.not_deleted.app_error", err.Id)
	})

	t.Run("a version snapshot is not restorable", func(t *testing.T) {
		snap := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		_, rawErr := h.db.Exec(
			"UPDATE DOCS_Page SET OriginalId = $2, DeleteAt = $3 WHERE Id = $1",
			snap.Id, mmmodel.NewId(), mmmodel.GetMillis())
		require.NoError(t, rawErr)

		_, err := h.svc.RestorePage(snap.Id, snap.SpaceId, userID)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.restore.not_restorable.app_error", err.Id)
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		_, err := h.svc.RestorePage("not-an-id", space.Id, userID)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})
}

// TestServiceRestoreSpace verifies the app restore path: a soft-deleted space becomes live, a
// live (not-deleted) space → 409, an invalid id → 400, and restoring over a backing channel a
// new live space now owns → 409.
func TestServiceRestoreSpace(t *testing.T) {
	h := openTestService(t)

	t.Run("restores a soft-deleted space", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		require.Nil(t, h.svc.DeleteSpace(space))

		got, err := h.svc.RestoreSpace(space.Id)
		require.Nil(t, err)
		require.Zero(t, got.DeleteAt)
	})

	t.Run("a non-deleted space returns 409", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		_, err := h.svc.RestoreSpace(space.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusConflict, err.StatusCode)
		require.Equal(t, "app.space.restore.not_deleted.app_error", err.Id)
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		_, err := h.svc.RestoreSpace("not-an-id")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.space.restore.invalid_id.app_error", err.Id)
	})

	t.Run("restoring over a channel a new live space owns returns 409", func(t *testing.T) {
		channelID := mmmodel.NewId()
		original := mustCreateSpace(t, h.store, channelID)
		require.Nil(t, h.svc.DeleteSpace(original))

		// A new live space now owns the backing channel; restoring the original would breach
		// the partial unique index uq_docs_space_channel_id.
		mustCreateSpace(t, h.store, channelID)

		_, err := h.svc.RestoreSpace(original.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusConflict, err.StatusCode)
	})
}
