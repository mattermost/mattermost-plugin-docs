// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package app_test contains integration-style tests for the space plugin service layer.
// Tests require a real Postgres database; the DSN comes from MM_SQLSETTINGS_DATASOURCE
// or TEST_DATABASE_DSN, defaulting to the standard local dev Postgres. They never skip —
// a missing database fails the run rather than passing on a skip.
package app_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// --- harness ---

// testHarness holds both the service and the underlying store so tests can seed
// data directly without opening a second DB connection.
type testHarness struct {
	svc   *app.Service
	store *store.Store
}

// defaultTestDSN matches the Mattermost convention (storetest.MakeSqlSettings):
// the standard local dev Postgres. Tests default to it rather than skipping.
const defaultTestDSN = "postgres://mmuser:mostest@localhost:5432/mattermost_test?sslmode=disable" //nolint:gosec // G101: well-known local test DSN (same as MM-core storetest), not a secret

// openTestService opens an isolated Postgres schema, runs migrations, and returns
// the harness. The schema is dropped by t.Cleanup.
func openTestService(t *testing.T) *testHarness {
	t.Helper()

	dsn := os.Getenv("MM_SQLSETTINGS_DATASOURCE")
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_DSN")
	}
	if dsn == "" {
		// No env override: fall back to the standard local dev Postgres. These tests
		// must never pass by skipping — a green run that exercised nothing is worse than
		// a red one — so a missing DB fails the connection checks below, it never skips.
		dsn = defaultTestDSN
	}

	schema := "docs_svc_" + mmmodel.NewId()

	// Create the schema in the base DB.
	baseDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open base postgres")
	t.Cleanup(func() { _ = baseDB.Close() })
	require.NoError(t, baseDB.Ping(), "ping base postgres")
	_, err = baseDB.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err, "create test schema")
	// Register schema teardown immediately so it still runs if a later setup step fails.
	t.Cleanup(func() {
		dropDB, dropErr := sql.Open("postgres", dsn)
		if dropErr == nil {
			_, _ = dropDB.Exec("DROP SCHEMA IF EXISTS " + pq.QuoteIdentifier(schema) + " CASCADE")
			_ = dropDB.Close()
		}
	})

	schemaDSN := addSearchPath(dsn, schema)

	db, err := sql.Open("postgres", schemaDSN)
	require.NoError(t, err, "open schema-scoped postgres")
	require.NoError(t, db.Ping(), "ping schema-scoped postgres")

	s, err := store.New(db, "postgres")
	require.NoError(t, err, "create store")
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.RunMigrations(), "run migrations")

	// Service is created without a pluginapi client: all tests that need
	// pluginapi (e.g. CreateChannel for Type='W') mock via the service's
	// channel-creation path. For store-only tests nil client is fine.
	svc := app.New(s, nil)

	return &testHarness{svc: svc, store: s}
}

// addSearchPath appends (or replaces) the search_path query parameter in a
// postgres DSN so that every connection in the pool uses the given schema.
// Handles both URL-form DSNs (postgres://…) and libpq key=value DSNs.
func addSearchPath(dsn, schema string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return dsn + " options='-c search_path=" + schema + "'"
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
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
	created, err := s.CreatePage(p)
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

func TestServiceUpdateSpace(t *testing.T) {
	h := openTestService(t)

	t.Run("successful update", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		space.Title = "Updated"
		updated, err := h.svc.UpdateSpace(space)
		require.Nil(t, err)
		require.Equal(t, "Updated", updated.Title)
	})

	t.Run("nil space rejected with 400", func(t *testing.T) {
		_, err := h.svc.UpdateSpace(nil)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("invalid id rejected with 400", func(t *testing.T) {
		_, err := h.svc.UpdateSpace(&model.Space{Id: "not-an-id", Title: "x"})
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("whitespace-only title rejected with 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		clone := *space
		clone.Title = "   "
		_, err := h.svc.UpdateSpace(&clone)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})

	t.Run("title over max runes rejected with 400", func(t *testing.T) {
		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		clone := *space
		clone.Title = strings.Repeat("a", model.SpaceTitleMaxRunes+1)
		_, err := h.svc.UpdateSpace(&clone)
		require.NotNil(t, err)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
	})
}

// TestServiceCreatePageParentDifferentSpace verifies that a parent living on the same
// backing channel but belonging to a different space (e.g. a surviving page from a prior,
// since-deleted space that reused the channel) is rejected, preventing cross-space
// hierarchy corruption.
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

	_, err := h.svc.UpdatePageWithOptimisticLocking(mmmodel.NewId(), &model.PagePatch{Title: mmmodel.NewPointer("Title")}, 0, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusNotFound, err.StatusCode)
}

func TestServiceUpdatePageInvalidID(t *testing.T) {
	h := openTestService(t)

	_, err := h.svc.UpdatePageWithOptimisticLocking("not-an-id", &model.PagePatch{Title: mmmodel.NewPointer("Title")}, 0, false, mmmodel.NewId())
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

func TestServiceUpdatePageWithOptimisticLocking(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	t.Run("first update succeeds", func(t *testing.T) {
		// First update — baseEditAt matches (0 since freshly created)
		updated, err := h.svc.UpdatePageWithOptimisticLocking(
			created.Id, &model.PagePatch{Title: mmmodel.NewPointer("Locked Title"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`)}, created.EditAt, false, userID,
		)
		require.Nil(t, err)
		require.Equal(t, "Locked Title", updated.Title)

		// Second update with stale baseEditAt — must conflict
		_, err2 := h.svc.UpdatePageWithOptimisticLocking(
			updated.Id, &model.PagePatch{Title: mmmodel.NewPointer("Conflict Title"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`)}, created.EditAt, false, userID,
		)
		require.NotNil(t, err2)
		require.Equal(t, 409, err2.StatusCode)
	})
}

func TestServiceUpdatePageWithOptimisticLockingForce(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	// First update to advance EditAt. Assert it succeeds: if it silently failed,
	// EditAt would stay at created.EditAt and the force path below would pass
	// trivially (nothing to override), proving nothing.
	first, firstErr := h.svc.UpdatePageWithOptimisticLocking(
		created.Id, &model.PagePatch{Title: mmmodel.NewPointer("First"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`)}, created.EditAt, false, userID,
	)
	require.Nil(t, firstErr, "first update must succeed to establish a stale baseEditAt")
	require.Greater(t, first.EditAt, created.EditAt, "EditAt must advance after the first update")

	// Force update with the now-stale baseEditAt must still succeed.
	forced, err := h.svc.UpdatePageWithOptimisticLocking(
		created.Id, &model.PagePatch{Title: mmmodel.NewPointer("Forced"), Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`)}, created.EditAt, true, userID,
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

	t.Run("UpdatePageWithOptimisticLocking rejects malformed userID", func(t *testing.T) {
		_, err := h.svc.UpdatePageWithOptimisticLocking(created.Id, &model.PagePatch{Title: mmmodel.NewPointer("Title")}, created.EditAt, false, "not-an-id")
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

	children, err := h.svc.GetPageChildren(parent.Id, 0, 0)
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

	ancestors, err := h.svc.GetPageAncestors(child.Id)
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

	descendants, err := h.svc.GetPageDescendants(root.Id)
	require.Nil(t, err)
	require.Len(t, descendants, 2)
}

func TestServiceGetSpaceIdForPage(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	spaceID, err := h.svc.GetSpaceIdForPage(created.Id)
	require.Nil(t, err)
	require.Equal(t, space.Id, spaceID)
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

// TestServiceUpdatePageWithOptimisticLockingNothingToUpdate verifies that the optimistic
// path rejects an empty-title + empty-content write with 400, rather than advancing edit
// metadata for a no-op.
func TestServiceUpdatePageWithOptimisticLockingNothingToUpdate(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	_, err := h.svc.UpdatePageWithOptimisticLocking(created.Id, &model.PagePatch{}, created.EditAt, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.update.nothing_to_update.app_error", err.Id)
}

// TestServiceUpdatePageOversizedBody verifies that the update path rejects a body that
// exceeds PageBodyMaxBytes with 400.
func TestServiceUpdatePageOversizedBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	oversized := strings.Repeat("x", model.PageBodyMaxBytes+1)
	_, err := h.svc.UpdatePageWithOptimisticLocking(created.Id, &model.PagePatch{Body: mmmodel.NewPointer(oversized)}, created.EditAt, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Contains(t, err.Id, "body_too_long")
}

// TestServiceUpdatePageOversizedSearchText verifies that the update path rejects oversized
// searchText with 400.
func TestServiceUpdatePageOversizedSearchText(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	created := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	oversized := strings.Repeat("x", model.PageSearchTextMaxBytes+1)
	_, err := h.svc.UpdatePageWithOptimisticLocking(created.Id, &model.PagePatch{Title: mmmodel.NewPointer("Title"), Body: mmmodel.NewPointer("body"), SearchText: mmmodel.NewPointer(oversized)}, created.EditAt, false, mmmodel.NewId())
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.update.search_text_too_long.app_error", err.Id)
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
	require.Contains(t, err.Id, "body_too_long")
}

// TestServiceUpdatePageWithOptimisticLockingSearchTextTooLong verifies the same
// searchText cap on the optimistic-locking update path.
func TestServiceUpdatePageWithOptimisticLockingSearchTextTooLong(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	oversized := strings.Repeat("x", model.PageSearchTextMaxBytes+1)
	_, err := h.svc.UpdatePageWithOptimisticLocking(
		created.Id, &model.PagePatch{Title: mmmodel.NewPointer("Title"), Body: mmmodel.NewPointer("body"), SearchText: mmmodel.NewPointer(oversized)}, created.EditAt, false, userID,
	)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, "app.page.update.search_text_too_long.app_error", err.Id)
}

// TestServiceUpdatePageCanSetEmptyBody verifies the patch contract: a non-nil empty Body
// explicitly clears the body, which the old ""-sentinel API could not express.
func TestServiceUpdatePageCanSetEmptyBody(t *testing.T) {
	h := openTestService(t)

	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	created := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	withBody, err := h.svc.UpdatePageWithOptimisticLocking(
		created.Id, &model.PagePatch{Body: mmmodel.NewPointer(`{"type":"doc","content":[]}`), SearchText: mmmodel.NewPointer("text")}, created.EditAt, false, userID,
	)
	require.Nil(t, err)
	require.NotEqual(t, "", withBody.Body)

	emptied, err := h.svc.UpdatePageWithOptimisticLocking(
		created.Id, &model.PagePatch{Body: mmmodel.NewPointer(""), SearchText: mmmodel.NewPointer("")}, withBody.EditAt, false, userID,
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
