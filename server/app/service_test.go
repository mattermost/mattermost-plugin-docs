// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package app_test contains integration-style tests for the Docs plugin service layer.
// Tests require a real Postgres database; the DSN comes from MM_SQLSETTINGS_DATASOURCE
// or TEST_DATABASE_DSN, defaulting to the standard local dev Postgres. They never skip —
// a missing database fails the run rather than passing on a skip.
package app_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

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

	db := testutil.OpenTestDB(t)

	s, err := store.New(db, "postgres")
	require.NoError(t, err, "create store")
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.RunMigrations(), "run migrations")

	// nil pluginapi client: these tests seed data directly through the store and
	// never exercise the client, so no mock is needed.
	svc := app.New(s, nil)

	return &testHarness{svc: svc, store: s, db: db}
}

// helpers to create test data directly on the store (bypassing business logic where the
// higher-level Create methods need channel/space state that may not exist).
func mustCreateSpace(t *testing.T, s *store.Store, channelID string) *model.Space {
	t.Helper()
	w := &model.Space{
		ChannelId: channelID,
		TeamId:    mmmodel.NewId(),
		CreatorId: mmmodel.NewId(),
		Title:     "Test Space",
	}
	saved, err := s.CreateSpace(w)
	require.NoError(t, err)
	return saved
}

func mustCreatePage(t *testing.T, s *store.Store, spaceID, channelID, userID, parentID string) *model.Page {
	t.Helper()
	p := &model.Page{
		SpaceId:   spaceID,
		ChannelId: channelID,
		UserId:    userID,
		ParentId:  parentID,
		Type:      model.PageTypePage,
		Title:     "Test Page",
		Body:      `{"type":"doc","content":[]}`,
	}
	// This fixture helper isn't exercising the depth cap (some callers build chains deeper than
	// MaxPageDepth to test store.MaxPageHierarchyDepth instead), so bypass it with a value well
	// past the store's own read-side limit.
	created, err := s.CreatePage(p, store.MaxPageHierarchyDepth+10)
	require.NoError(t, err)
	return created
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

func TestServiceGetSpaceForChannel(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	saved := mustCreateSpace(t, h.store, channelID)

	got, err := h.svc.GetSpaceForChannel(channelID)
	require.Nil(t, err)
	require.Equal(t, saved.Id, got.Id)
}

func TestServiceReplaceSpace(t *testing.T) {
	h := openTestService(t)

	t.Run("successful update", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		space.Title = "Updated"
		updated, err := h.svc.ReplaceSpace(space, false)
		require.Nil(t, err)
		require.Equal(t, "Updated", updated.Title)
	})

	t.Run("nil space rejected with 400", func(t *testing.T) {
		_, err := h.svc.ReplaceSpace(nil, false)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("invalid id rejected with 400", func(t *testing.T) {
		_, err := h.svc.ReplaceSpace(&model.Space{Id: "not-an-id", Title: "x"}, false)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("whitespace-only title rejected with 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		clone := *space
		clone.Title = "   "
		_, err := h.svc.ReplaceSpace(&clone, false)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("title over max runes rejected with 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		clone := *space
		clone.Title = strings.Repeat("a", model.SpaceTitleMaxRunes+1)
		_, err := h.svc.ReplaceSpace(&clone, false)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("description over max runes rejected with 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		clone := *space
		clone.Description = strings.Repeat("a", model.SpaceDescriptionMaxRunes+1)
		_, err := h.svc.ReplaceSpace(&clone, false)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.shared.description_too_long.app_error", err.Id)
	})

	t.Run("icon over max bytes rejected with 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		clone := *space
		clone.Icon = strings.Repeat("a", model.SpaceIconMaxBytes+1)
		_, err := h.svc.ReplaceSpace(&clone, false)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.shared.icon_too_large.app_error", err.Id)
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

	_, err := h.svc.CreatePage(space.Id, rogueParent.Id, "Child", "", "", userID, "")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Contains(t, err.Id, "parent_different_space")
}

// TestServiceUpdatePageNotFound verifies the 404 path: a well-formed but absent page ID.
func TestServiceUpdatePageNotFound(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.UpdatePage(mmmodel.NewId(), mmmodel.NewId(), &model.PagePatch{Title: mmmodel.NewPointer("Title")}, 0, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusNotFound, err.StatusCode)
}

func TestServiceUpdatePageInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.UpdatePage("not-an-id", mmmodel.NewId(), &model.PagePatch{Title: mmmodel.NewPointer("Title")}, 0, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
}

func TestServiceDeleteSpace(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	saved := mustCreateSpace(t, h.store, channelID)

	require.Nil(t, h.svc.DeleteSpace(saved.Id))

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
			created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Locked Title"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, created.EditAt, false, userID,
		)
		require.Nil(t, err)
		require.Equal(t, "Locked Title", updated.Title)

		// Second update with stale baseEditAt — must conflict
		_, err2 := h.svc.UpdatePage(
			updated.Id, updated.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Conflict Title"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, created.EditAt, false, userID,
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
		created.Id, created.SpaceId, &model.PagePatch{SearchText: mmmodel.NewPointer("some text")}, created.EditAt, false, userID,
	)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.patch.search_text_body_mismatch.app_error", err.Id)
}

// TestServiceUpdatePageBodyWithoutSearchText covers the inverse: a Body change with no
// accompanying SearchText would strand the GIN index on the page's old content, so the
// update-path guard rejects it too (both or neither).
func TestServiceUpdatePageBodyWithoutSearchText(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	_, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer("new body")}, created.EditAt, false, userID,
	)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.patch.search_text_body_mismatch.app_error", err.Id)
}

// TestServiceUpdatePageSearchTextWithBodyCleared verifies that clearing Body to "" while
// setting a non-empty SearchText is rejected: SearchText is the body's plain-text projection
// and must not survive an emptied body (mirrors the create-path rule).
func TestServiceUpdatePageSearchTextWithBodyCleared(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	_, err := h.svc.UpdatePage(
		created.Id, created.SpaceId,
		&model.PagePatch{Body: mmmodel.NewPointer(""), SearchText: mmmodel.NewPointer("some text")},
		created.EditAt, false, userID,
	)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.patch.search_text_without_content.app_error", err.Id)
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
		created.EditAt, false, userID,
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
		created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("First"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, created.EditAt, false, userID,
	)
	require.Nil(t, firstErr, "first update must succeed to establish a stale baseEditAt")
	require.Greater(t, first.EditAt, created.EditAt, "EditAt must advance after the first update")

	// Force update with the now-stale baseEditAt must still succeed.
	forced, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Forced"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("")}, created.EditAt, true, userID,
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

	err := h.svc.DeleteSpace("not-an-id")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
}

func TestServiceUpdatePageInvalidUserID(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	t.Run("UpdatePage rejects malformed userID", func(t *testing.T) {
		_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Title")}, created.EditAt, false, "not-an-id")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})
}

func TestServiceCreatePageDuplicateIDConflict(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	pageID := mmmodel.NewId()

	_, err := h.svc.CreatePage(space.Id, "", "First", `{"type":"doc","content":[]}`, "", userID, pageID)
	require.Nil(t, err)

	// Re-using the same caller-supplied page Id must surface as a 409 conflict, not a 500.
	_, err = h.svc.CreatePage(space.Id, "", "Second", `{"type":"doc","content":[]}`, "", userID, pageID)
	require.NotNil(t, err)
	require.Equal(t, http.StatusConflict, err.StatusCode)
}

func TestServiceGetPageChildren(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

	children, err := h.svc.GetPageChildren(parent.Id, space.Id, 0, 0)
	require.Nil(t, err)
	require.Len(t, children, 1)
	require.Equal(t, child.Id, children[0].Id)
}

func TestServiceGetPageAncestors(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	root := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, root.Id)

	ancestors, err := h.svc.GetPageAncestors(child.Id, space.Id)
	require.Nil(t, err)
	require.Len(t, ancestors, 1)
	require.Equal(t, root.Id, ancestors[0].Id)
}

func TestServiceGetPageDescendants(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	root := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, root.Id)
	_ = mustCreatePage(t, h.store, space.Id, channelID, userID, child.Id)

	descendants, err := h.svc.GetPageDescendants(root.Id, space.Id)
	require.Nil(t, err)
	require.Len(t, descendants, 2)
}

func TestServiceGetSpacePages(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	for range 3 {
		mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	}

	pages, err := h.svc.GetSpacePages(space.Id, 0, 0)
	require.Nil(t, err)
	require.Len(t, pages, 3)

	t.Run("pages are isolated by space, not by reused channel", func(t *testing.T) {
		// Soft-delete the first space (which cascades its 3 pages to soft-deleted),
		// then create a second live space on the SAME channel. GetSpacePages for the
		// new space must not leak the old space's pages.
		require.Nil(t, h.svc.DeleteSpace(space.Id))

		space2 := mustCreateSpace(t, h.store, channelID)
		mustCreatePage(t, h.store, space2.Id, channelID, userID, "")

		pages2, err := h.svc.GetSpacePages(space2.Id, 0, 0)
		require.Nil(t, err)
		require.Len(t, pages2, 1, "new space must only see its own page, not the prior space's pages on the reused channel")
		require.Equal(t, space2.Id, pages2[0].SpaceId)
	})
}

// TestServiceGetSpacePagesInvalidID verifies that GetSpacePages with a malformed space ID
// returns 400 with the expected error key.
func TestServiceGetSpacePagesInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.GetSpacePages("not-a-valid-id", 0, 0)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.space.get_pages.invalid_space_id.app_error", err.Id)
}

func TestServiceGetTeamSpaces(t *testing.T) {
	h := openTestService(t)

	teamID := mmmodel.NewId()
	for range 2 {
		sp := &model.Space{ChannelId: mmmodel.NewId(), TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "Test Space"}
		_, err := h.store.CreateSpace(sp)
		require.NoError(t, err)
	}

	spaces, err := h.svc.GetSpacesForTeam(teamID, 0, 0)
	require.Nil(t, err)
	require.Len(t, spaces, 2)
}

// TestServiceGetTeamSpacesInvalidID verifies a malformed team id is rejected with 400.
func TestServiceGetTeamSpacesInvalidID(t *testing.T) {
	h := openTestService(t)
	_, err := h.svc.GetSpacesForTeam("not-a-valid-id", 0, 0)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.space.get_for_team.invalid_team_id.app_error", err.Id)
}

// TestServiceGetPageAncestorsLimitExceeded verifies the store's ErrLimitExceeded for an
// over-deep ancestor chain maps to HTTP 422 at the service layer. The chain is seeded
// directly through the store, since the app-layer CreatePage depth cap is far lower.
func TestServiceGetPageAncestorsLimitExceeded(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parentID := ""
	var leaf *model.Page
	for range store.MaxPageHierarchyDepth + 2 {
		leaf = mustCreatePage(t, h.store, space.Id, channelID, userID, parentID)
		parentID = leaf.Id
	}

	_, err := h.svc.GetPageAncestors(leaf.Id, space.Id)
	require.NotNil(t, err)
	require.Equal(t, http.StatusUnprocessableEntity, err.StatusCode)
}

// TestServiceCreatePageDerivesChannelFromSpace verifies the page's ChannelId is
// derived from its space's backing channel, not supplied by the caller.
func TestServiceCreatePageDerivesChannelFromSpace(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	created, err := h.svc.CreatePage(space.Id, "", "My Page", "", "", userID, "")
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

	t.Run("rejects invalid page id", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, "", "Title", "", "", userID, "not-a-valid-id")
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects invalid space id", func(t *testing.T) {
		_, err := h.svc.CreatePage("not-a-valid-id", "", "Title", "", "", userID, "")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.create.invalid_space_id.app_error", err.Id)
	})

	t.Run("rejects invalid user id", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, "", "Title", "", "", "not-a-valid-id", "")
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.create.invalid_user_id.app_error", err.Id)
	})

	t.Run("rejects empty title", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, "", "   ", "", "", userID, "")
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects title too long", func(t *testing.T) {
		long := strings.Repeat("x", model.PageTitleMaxRunes+1)
		_, err := h.svc.CreatePage(space.Id, "", long, "", "", userID, "")
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects nonexistent parent", func(t *testing.T) {
		_, err := h.svc.CreatePage(space.Id, mmmodel.NewId(), "Title", "", "", userID, "")
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("rejects parent in a different space", func(t *testing.T) {
		otherChannelID := mmmodel.NewId()
		otherSpace := mustCreateSpace(t, h.store, otherChannelID)
		parent := mustCreatePage(t, h.store, otherSpace.Id, otherChannelID, userID, "")
		_, err := h.svc.CreatePage(space.Id, parent.Id, "Title", "", "", userID, "")
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
		for range app.MaxPageDepth {
			p, err := h.svc.CreatePage(depthSpace.Id, parentID, "d", "", "", userID, "")
			require.Nil(t, err)
			parentID = p.Id
		}
		_, err := h.svc.CreatePage(depthSpace.Id, parentID, "too deep", "", "", userID, "")
		require.NotNil(t, err)
		require.Equal(t, 400, err.StatusCode)
	})

	t.Run("creates a valid page", func(t *testing.T) {
		created, err := h.svc.CreatePage(space.Id, "", "My Page", "", "", userID, "")
		require.Nil(t, err)
		require.Equal(t, "My Page", created.Title)
	})
}

func TestServiceGetSpaceForChannelNotFound(t *testing.T) {
	h := openTestService(t)
	_, err := h.svc.GetSpaceForChannel(mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, 404, err.StatusCode)
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

// TestServiceGetSpaceForChannelInvalidID verifies that GetSpaceForChannel with a malformed
// ID returns 400 with the expected error key.
func TestServiceGetSpaceForChannelInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.GetSpaceForChannel("not-a-valid-id")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.space.get_for_channel.invalid_channel_id.app_error", err.Id)
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

	_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{}, created.EditAt, false, mmmodel.NewId())
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
	_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer(oversized), SearchText: mmmodel.NewPointer("")}, created.EditAt, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.is_valid.body.app_error", err.Id)
}

// TestServiceUpdatePageOversizedSearchText verifies that the update path rejects oversized
// searchText with 400.
func TestServiceUpdatePageOversizedSearchText(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	oversized := strings.Repeat("x", model.PageSearchTextMaxBytes+1)
	_, err := h.svc.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: mmmodel.NewPointer("Title"), Body: mmmodel.NewPointer("body"), SearchText: mmmodel.NewPointer(oversized)}, created.EditAt, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.is_valid.search_text.app_error", err.Id)
}

// TestServiceCreatePageOversizedBody verifies that CreatePage rejects a body that exceeds
// PageBodyMaxBytes with 400.
func TestServiceCreatePageOversizedBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)

	oversized := strings.Repeat("x", model.PageBodyMaxBytes+1)
	_, err := h.svc.CreatePage(space.Id, "", "Title", oversized, "", mmmodel.NewId(), "")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "model.page.is_valid.body.app_error", err.Id)
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
		created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("text")}, created.EditAt, false, userID,
	)
	require.Nil(t, err)
	require.NotEqual(t, "", withBody.Body)

	emptied, err := h.svc.UpdatePage(
		created.Id, created.SpaceId, &model.PagePatch{Body: mmmodel.NewPointer(""), SearchText: mmmodel.NewPointer("")}, withBody.EditAt, false, userID,
	)
	require.Nil(t, err)
	require.Equal(t, "", emptied.Body, "an explicit empty Body must clear the stored body")
}

// TestServiceCreatePageSearchTextWithoutBody verifies CreatePage rejects searchText
// supplied without a body, matching the update path's rule.
func TestServiceCreatePageSearchTextWithoutBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)

	_, err := h.svc.CreatePage(space.Id, "", "Title", "", "searchtext", mmmodel.NewId(), "")
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.create.search_text_without_content.app_error", err.Id)
}

// TestServiceGetPageWithDeleted verifies the with-deleted reader returns a soft-deleted
// page that the live reader hides, and rejects an invalid id.
func TestServiceGetPageWithDeleted(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")
	require.NoError(t, h.store.DeletePage(created.Id, created.SpaceId))

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

		require.Nil(t, h.svc.DeletePage(created.Id, created.SpaceId))

		_, err := h.svc.GetPage(created.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusNotFound, err.StatusCode)
	})

	t.Run("reparents live children to the deleted page's parent", func(t *testing.T) {
		parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

		require.Nil(t, h.svc.DeletePage(parent.Id, parent.SpaceId))

		gotChild, err := h.svc.GetPage(child.Id)
		require.Nil(t, err)
		require.Equal(t, parent.ParentId, gotChild.ParentId)
	})

	t.Run("missing page returns 404", func(t *testing.T) {
		err := h.svc.DeletePage(mmmodel.NewId(), space.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusNotFound, err.StatusCode)
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		err := h.svc.DeletePage("not-an-id", space.Id)
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
		require.NoError(t, h.store.DeletePage(created.Id, created.SpaceId))

		restored, err := h.svc.RestorePage(created.Id, created.SpaceId)
		require.Nil(t, err)
		require.Zero(t, restored.DeleteAt)

		got, err := h.svc.GetPage(created.Id)
		require.Nil(t, err)
		require.Zero(t, got.DeleteAt)
	})

	t.Run("leaves promoted children in place on restore", func(t *testing.T) {
		parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

		require.Nil(t, h.svc.DeletePage(parent.Id, parent.SpaceId))
		_, err := h.svc.RestorePage(parent.Id, parent.SpaceId)
		require.Nil(t, err)

		// Matching Confluence: the promoted child stays put after restore, not pulled back.
		gotChild, err := h.svc.GetPage(child.Id)
		require.Nil(t, err)
		require.Equal(t, parent.ParentId, gotChild.ParentId)
	})

	t.Run("a non-deleted page returns 400", func(t *testing.T) {
		live := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		_, err := h.svc.RestorePage(live.Id, live.SpaceId)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.restore.not_deleted.app_error", err.Id)
	})

	t.Run("a version snapshot is not restorable", func(t *testing.T) {
		snap := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		_, rawErr := h.db.Exec(
			"UPDATE DOCS_Page SET OriginalId = $2, DeleteAt = $3 WHERE Id = $1",
			snap.Id, mmmodel.NewId(), mmmodel.GetMillis())
		require.NoError(t, rawErr)

		_, err := h.svc.RestorePage(snap.Id, snap.SpaceId)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		require.Equal(t, "app.page.restore.not_restorable.app_error", err.Id)
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		_, err := h.svc.RestorePage("not-an-id", space.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})
}

// TestServiceRestoreSpace verifies the app restore path: a soft-deleted space becomes live, a
// live (not-deleted) space → 400, an invalid id → 400, and restoring over a backing channel a
// new live space now owns → 409.
func TestServiceRestoreSpace(t *testing.T) {
	h := openTestService(t)

	t.Run("restores a soft-deleted space", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		require.Nil(t, h.svc.DeleteSpace(space.Id))

		got, err := h.svc.RestoreSpace(space.Id)
		require.Nil(t, err)
		require.Zero(t, got.DeleteAt)
	})

	t.Run("a non-deleted space returns 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		_, err := h.svc.RestoreSpace(space.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
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
		require.Nil(t, h.svc.DeleteSpace(original.Id))

		// A new live space now owns the backing channel; restoring the original would breach
		// the partial unique index uq_docs_space_channel_id.
		mustCreateSpace(t, h.store, channelID)

		_, err := h.svc.RestoreSpace(original.Id)
		require.NotNil(t, err)
		require.Equal(t, http.StatusConflict, err.StatusCode)
	})
}
