package store_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// defaultTestDSN matches the Mattermost convention (storetest.MakeSqlSettings):
// the standard local dev Postgres. Tests default to it rather than skipping, so a
// run always attempts the DB and fails — never skips — when none is reachable.
const defaultTestDSN = "postgres://mmuser:mostest@localhost:5432/mattermost_test?sslmode=disable" //nolint:gosec // G101: well-known local test DSN (same as MM-core storetest), not a secret

// openTestDB opens a Postgres test DB from the environment, creates an isolated
// schema for this test run, runs migrations into it, and returns the Store.
// The schema is dropped in t.Cleanup so parallel package runs never share tables.
func openTestDB(t *testing.T) *store.Store {
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

	schema := "docs_test_" + mmmodel.NewId()

	// Connect to the base DSN to create the schema.
	baseDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open base postgres")
	t.Cleanup(func() { _ = baseDB.Close() })
	require.NoError(t, baseDB.Ping(), "ping base postgres")
	_, err = baseDB.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err, "create test schema")
	// Register schema teardown immediately so it still runs if a later setup step fails.
	t.Cleanup(func() {
		// Drop the isolated schema using a fresh connection so it always runs.
		dropDB, dropErr := sql.Open("postgres", dsn)
		if dropErr == nil {
			_, _ = dropDB.Exec("DROP SCHEMA IF EXISTS " + pq.QuoteIdentifier(schema) + " CASCADE")
			_ = dropDB.Close()
		}
	})

	// Rebuild DSN with search_path set so every pooled connection uses the schema.
	schemaDSN := addSearchPath(dsn, schema)

	db, err := sql.Open("postgres", schemaDSN)
	require.NoError(t, err, "open schema-scoped postgres")
	require.NoError(t, db.Ping(), "ping schema-scoped postgres")

	s, err := store.New(db, "postgres")
	require.NoError(t, err, "create store")
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.RunMigrations(), "run migrations")

	return s
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

func newSpace(channelID string) *model.Space {
	return &model.Space{
		ChannelId: channelID,
		TeamId:    mmmodel.NewId(),
		CreatorId: mmmodel.NewId(),
		Title:     "Test Space",
	}
}

func newPage(spaceID, channelID, userID, parentID string) *model.Page {
	return &model.Page{
		SpaceId:   spaceID,
		ChannelId: channelID,
		UserId:    userID,
		ParentId:  parentID,
		Type:      model.PageTypePage,
		Title:     "Test Page",
		Body:      `{"type":"doc","content":[]}`,
	}
}

// --- Space tests ---

func TestSpace(t *testing.T) {
	t.Run("save and get by id returns stored space", func(t *testing.T) {
		s := openTestDB(t)

		channelID := mmmodel.NewId()
		w := newSpace(channelID)
		saved, err := s.CreateSpace(w)
		require.NoError(t, err)
		require.NotEmpty(t, saved.Id)

		got, err := s.GetSpace(saved.Id)
		require.NoError(t, err)
		require.Equal(t, saved.Id, got.Id)
		require.Equal(t, saved.Title, got.Title)
		require.Equal(t, channelID, got.ChannelId)
	})

	t.Run("get for channel returns the space linked to that channel", func(t *testing.T) {
		s := openTestDB(t)

		channelID := mmmodel.NewId()
		saved, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)

		got, err := s.GetSpaceForChannel(channelID)
		require.NoError(t, err)
		require.Equal(t, saved.Id, got.Id)
	})

	t.Run("update persists changed fields", func(t *testing.T) {
		s := openTestDB(t)

		saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		saved.Title = "Updated Title"
		updated, err := s.UpdateSpace(saved)
		require.NoError(t, err)
		require.Equal(t, "Updated Title", updated.Title)
	})

	t.Run("delete makes space not found", func(t *testing.T) {
		s := openTestDB(t)

		saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpace(saved.Id))

		_, err = s.GetSpace(saved.Id)
		require.True(t, store.IsErrNotFound(err), "expected not-found after delete, got %v", err)
	})

	t.Run("get nonexistent space returns not-found", func(t *testing.T) {
		s := openTestDB(t)

		_, err := s.GetSpace(mmmodel.NewId())
		require.True(t, store.IsErrNotFound(err))
	})
}

// --- Page tests ---

func TestCreateAndGetPage(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()
	p := newPage(savedSpace.Id, channelID, userID, "")
	created, err := s.CreatePage(p)
	require.NoError(t, err)
	require.NotEmpty(t, created.Id)
	require.Equal(t, savedSpace.Id, created.SpaceId)

	got, err := s.GetPage(created.Id, false)
	require.NoError(t, err)
	require.Equal(t, created.Id, got.Id)
	require.Equal(t, "Test Page", got.Title)
}

func TestGetPageNotFound(t *testing.T) {
	s := openTestDB(t)

	_, err := s.GetPage(mmmodel.NewId(), false)
	require.True(t, store.IsErrNotFound(err))
}

func TestCreatePageSortOrder(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()
	p1 := newPage(savedSpace.Id, channelID, userID, "")
	p1.Title = "Page 1"
	c1, err := s.CreatePage(p1)
	require.NoError(t, err)

	p2 := newPage(savedSpace.Id, channelID, userID, "")
	p2.Title = "Page 2"
	c2, err := s.CreatePage(p2)
	require.NoError(t, err)

	require.Greater(t, c2.SortOrder, c1.SortOrder, "second page should have higher sort order")
}

func TestGetPageChildren(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()

	parent := newPage(savedSpace.Id, channelID, userID, "")
	parent.Title = "Parent"
	createdParent, err := s.CreatePage(parent)
	require.NoError(t, err)

	child := newPage(savedSpace.Id, channelID, userID, createdParent.Id)
	child.Title = "Child"
	_, err = s.CreatePage(child)
	require.NoError(t, err)

	children, err := s.GetPageChildren(createdParent.Id, 0, 0)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, "Child", children[0].Title)
}

func TestGetPageChildrenOrderedBySortOrder(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	savedSpace, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	userID := mmmodel.NewId()
	parent, err := s.CreatePage(newPage(savedSpace.Id, channelID, userID, ""))
	require.NoError(t, err)

	first := newPage(savedSpace.Id, channelID, userID, parent.Id)
	first.Title = "First"
	createdFirst, err := s.CreatePage(first)
	require.NoError(t, err)

	second := newPage(savedSpace.Id, channelID, userID, parent.Id)
	second.Title = "Second"
	_, err = s.CreatePage(second)
	require.NoError(t, err)

	// Children come back in SortOrder order (here, creation order), not newest-first.
	children, err := s.GetPageChildren(parent.Id, 0, 0)
	require.NoError(t, err)
	require.Len(t, children, 2)
	require.Equal(t, "First", children[0].Title)
	require.Equal(t, "Second", children[1].Title)

	// Reordering by SortOrder alone (CreateAt unchanged) reorders the listing,
	// proving SortOrder — not CreateAt — drives the order.
	createdFirst.SortOrder = children[1].SortOrder + 1
	_, err = s.UpdatePage(createdFirst)
	require.NoError(t, err)

	reordered, err := s.GetPageChildren(parent.Id, 0, 0)
	require.NoError(t, err)
	require.Len(t, reordered, 2)
	require.Equal(t, "Second", reordered[0].Title)
	require.Equal(t, "First", reordered[1].Title)
}

func TestDeleteSpaceCascadesPages(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	savedSpace, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	userID := mmmodel.NewId()
	parent, err := s.CreatePage(newPage(savedSpace.Id, channelID, userID, ""))
	require.NoError(t, err)

	createdChild, err := s.CreatePage(newPage(savedSpace.Id, channelID, userID, parent.Id))
	require.NoError(t, err)

	require.NoError(t, s.DeleteSpace(savedSpace.Id))

	// The space and all of its pages are soft-deleted together: none are fetchable by ID.
	_, err = s.GetSpace(savedSpace.Id)
	require.True(t, store.IsErrNotFound(err))

	_, err = s.GetPage(parent.Id, false)
	require.True(t, store.IsErrNotFound(err), "parent page should be soft-deleted with its space")

	_, err = s.GetPage(createdChild.Id, false)
	require.True(t, store.IsErrNotFound(err), "child page should be soft-deleted with its space")
}

// TestCreatePageInDeletedSpaceRejected verifies the transactional space guard in
// CreatePage: once a space is soft-deleted, a new page can no longer be inserted into
// it (the FOR UPDATE check finds no live space row), so no live page is ever left in a
// deleted space.
func TestCreatePageInDeletedSpaceRejected(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	require.NoError(t, s.DeleteSpace(space.Id))

	_, err = s.CreatePage(newPage(space.Id, channelID, userID, ""))
	require.Error(t, err, "creating a page in a soft-deleted space must fail")
	require.True(t, store.IsErrNotFound(err), "deleted space must map to ErrNotFound; got %T: %v", err, err)
}

func TestGetPageDescendants(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()

	root := newPage(savedSpace.Id, channelID, userID, "")
	root.Title = "Root"
	createdRoot, err := s.CreatePage(root)
	require.NoError(t, err)

	child := newPage(savedSpace.Id, channelID, userID, createdRoot.Id)
	child.Title = "Child"
	createdChild, err := s.CreatePage(child)
	require.NoError(t, err)

	grandchild := newPage(savedSpace.Id, channelID, userID, createdChild.Id)
	grandchild.Title = "Grandchild"
	_, err = s.CreatePage(grandchild)
	require.NoError(t, err)

	descendants, err := s.GetPageDescendants(createdRoot.Id)
	require.NoError(t, err)
	require.Len(t, descendants, 2) // child + grandchild (root excluded)
}

func TestGetPageAncestors(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()

	root := newPage(savedSpace.Id, channelID, userID, "")
	root.Title = "Root"
	createdRoot, err := s.CreatePage(root)
	require.NoError(t, err)

	child := newPage(savedSpace.Id, channelID, userID, createdRoot.Id)
	child.Title = "Child"
	createdChild, err := s.CreatePage(child)
	require.NoError(t, err)

	ancestors, err := s.GetPageAncestors(createdChild.Id)
	require.NoError(t, err)
	require.Len(t, ancestors, 1) // root (child excluded)
	require.Equal(t, createdRoot.Id, ancestors[0].Id)
}

func TestDepthBoundaryExact(t *testing.T) {
	const maxDepth = 10 // mirrors app.MaxPageDepth; the store CTE uses 50

	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()

	// Build a chain of maxDepth pages (root at depth 1).
	parentID := ""
	var lastPage *model.Page
	for depth := 1; depth <= maxDepth; depth++ {
		pg := newPage(savedSpace.Id, channelID, userID, parentID)
		pg.Title = fmt.Sprintf("depth-%d", depth)
		created, createErr := s.CreatePage(pg)
		require.NoError(t, createErr, "create page at depth %d", depth)
		parentID = created.Id
		lastPage = created
	}
	require.NotNil(t, lastPage)

	// Verify GetPageAncestors returns maxDepth-1 ancestors for the leaf.
	ancestors, ancestorErr := s.GetPageAncestors(lastPage.Id)
	require.NoError(t, ancestorErr)
	require.Len(t, ancestors, maxDepth-1, "leaf at depth %d should have %d ancestors", maxDepth, maxDepth-1)
}

// TestOptimisticLockConflict verifies that Update with a stale EditAt fails as ErrConflict.
func TestOptimisticLockConflict(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()
	p := newPage(savedSpace.Id, channelID, userID, "")
	created, err := s.CreatePage(p)
	require.NoError(t, err)

	// Update once to advance EditAt.
	first := created.Clone()
	first.Title = "First Update"
	first.LastModifiedBy = userID
	updated, err := s.UpdatePage(first)
	require.NoError(t, err)

	// Assert EditAt actually advanced before we try the stale path; if it didn't,
	// the conflict test below would pass trivially for the wrong reason.
	require.Greater(t, updated.EditAt, created.EditAt, "EditAt must advance after UpdatePage")

	// Try to update with the original (stale) EditAt via s.UpdatePage.
	stale := created.Clone()
	stale.Title = "Conflict"
	stale.LastModifiedBy = userID
	// stale.EditAt is still the original create value, which is now stale.
	_, conflictErr := s.UpdatePage(stale)
	require.Error(t, conflictErr, "update with stale EditAt must fail")
	require.True(t, store.IsErrConflict(conflictErr), "stale-EditAt update must return ErrConflict; got %v", conflictErr)

	// Update with correct EditAt must succeed.
	fresh := updated.Clone()
	fresh.Title = "Fresh Update"
	fresh.LastModifiedBy = userID
	_, freshErr := s.UpdatePage(fresh)
	require.NoError(t, freshErr, "update with correct EditAt must succeed")
}

// TestUpdateSpaceCASConflict verifies UpdateSpace's optimistic locking: a second update
// carrying a stale UpdateAt is rejected with ErrConflict, and updating a soft-deleted
// space returns ErrNotFound (not ErrConflict).
func TestUpdateSpaceCASConflict(t *testing.T) {
	s := openTestDB(t)

	saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	// Capture the original (now-stale) baseline before advancing UpdateAt.
	stale := *saved

	saved.Title = "First"
	updated, err := s.UpdateSpace(saved)
	require.NoError(t, err)
	require.Greater(t, updated.UpdateAt, stale.UpdateAt, "UpdateAt must advance")

	// DB round-trip: persisted Title and UpdateAt must match what was returned in-memory.
	persisted, err := s.GetSpace(updated.Id)
	require.NoError(t, err)
	require.Equal(t, "First", persisted.Title, "persisted Title must match returned struct")
	require.Equal(t, updated.UpdateAt, persisted.UpdateAt, "persisted UpdateAt must match returned struct")

	// Re-submitting with the stale baseline must conflict.
	stale.Title = "Stale"
	_, conflictErr := s.UpdateSpace(&stale)
	require.Error(t, conflictErr)
	require.True(t, store.IsErrConflict(conflictErr), "stale UpdateAt must return ErrConflict; got %v", conflictErr)

	// Deleting then updating must return NotFound, not Conflict.
	require.NoError(t, s.DeleteSpace(updated.Id))
	updated.Title = "After delete"
	_, delErr := s.UpdateSpace(updated)
	require.Error(t, delErr)
	require.True(t, store.IsErrNotFound(delErr), "updating a deleted space must return ErrNotFound; got %v", delErr)
}

// TestCreatePageInvalidID verifies that an invalid caller-supplied pageID is rejected at IsValid.
func TestCreatePageInvalidID(t *testing.T) {
	p := &model.Page{
		SpaceId:   mmmodel.NewId(),
		ChannelId: mmmodel.NewId(),
		UserId:    mmmodel.NewId(),
		ParentId:  "",
		Type:      model.PageTypePage,
		Title:     "Test",
		Body:      `{"type":"doc","content":[]}`,
		Id:        "not-a-valid-26-char-id!!",
	}

	// PreSave only assigns Id if empty; our supplied Id is preserved.
	p.PreSave()
	// The supplied invalid Id should not survive IsValid.
	require.NotNil(t, p.IsValid(), "page with invalid ID must fail IsValid")
}

func TestGetPageAncestors_FourLevelChain(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Build root → L1 → L2 → L3.
	root, err := s.CreatePage(newPage(space.Id, channelID, userID, ""))
	require.NoError(t, err)
	l1, err := s.CreatePage(newPage(space.Id, channelID, userID, root.Id))
	require.NoError(t, err)
	l2, err := s.CreatePage(newPage(space.Id, channelID, userID, l1.Id))
	require.NoError(t, err)
	l3, err := s.CreatePage(newPage(space.Id, channelID, userID, l2.Id))
	require.NoError(t, err)

	ancestors, err := s.GetPageAncestors(l3.Id)
	require.NoError(t, err)
	require.Len(t, ancestors, 3, "l3 leaf must have exactly 3 ancestors")

	// CTE orders by CreateAt ascending, so root is first.
	require.Equal(t, root.Id, ancestors[0].Id, "first ancestor must be root")
	require.Equal(t, l1.Id, ancestors[1].Id, "second ancestor must be l1")
	require.Equal(t, l2.Id, ancestors[2].Id, "third ancestor must be l2")
}

// TestGetPageDescendants_ExcludesUnrelatedSubtrees verifies that
// GetPageDescendants for a mid-tree node returns only its own subtree
// and not siblings or their children.
func TestGetPageDescendants_ExcludesUnrelatedSubtrees(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Root with two children; childA has its own grandchild.
	root, err := s.CreatePage(newPage(space.Id, channelID, userID, ""))
	require.NoError(t, err)
	childA, err := s.CreatePage(newPage(space.Id, channelID, userID, root.Id))
	require.NoError(t, err)
	_, err = s.CreatePage(newPage(space.Id, channelID, userID, root.Id)) // childB — unrelated subtree
	require.NoError(t, err)
	grandchild, err := s.CreatePage(newPage(space.Id, channelID, userID, childA.Id))
	require.NoError(t, err)

	// Descendants of childA must be only grandchild, not childB.
	descendants, err := s.GetPageDescendants(childA.Id)
	require.NoError(t, err)
	require.Len(t, descendants, 1, "childA must have exactly one descendant (grandchild)")
	require.Equal(t, grandchild.Id, descendants[0].Id)
}

// TestGetPageDescendants_LeafHasZeroDescendants verifies that a leaf page
// (no children) returns an empty descendant list.
func TestGetPageDescendants_LeafHasZeroDescendants(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	leaf, err := s.CreatePage(newPage(space.Id, channelID, userID, ""))
	require.NoError(t, err)

	descendants, err := s.GetPageDescendants(leaf.Id)
	require.NoError(t, err)
	require.Empty(t, descendants, "leaf page must have zero descendants")
}

func TestGetSpacesForTeam(t *testing.T) {
	s := openTestDB(t)

	teamID := mmmodel.NewId()
	for range 2 {
		sp := newSpace(mmmodel.NewId())
		sp.TeamId = teamID
		_, err := s.CreateSpace(sp)
		require.NoError(t, err)
	}
	// A space in a different team must not be returned.
	_, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	spaces, err := s.GetSpacesForTeam(teamID, false, 0, 0)
	require.NoError(t, err)
	require.Len(t, spaces, 2)
}

func TestCreateSpaceDuplicateChannel(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	_, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	_, err = s.CreateSpace(newSpace(channelID))
	require.Error(t, err, "unique partial index on (ChannelId) WHERE DeleteAt=0 must reject a duplicate")
	require.True(t, store.IsErrConflict(err), "duplicate active channel must map to ErrConflict, got %T: %v", err, err)
}

func TestCreatePageDuplicateIDConflict(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	savedSpace, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	userID := mmmodel.NewId()
	pageID := mmmodel.NewId()

	first := newPage(savedSpace.Id, channelID, userID, "")
	first.Id = pageID
	_, err = s.CreatePage(first)
	require.NoError(t, err)

	second := newPage(savedSpace.Id, channelID, userID, "")
	second.Id = pageID
	_, err = s.CreatePage(second)
	require.Error(t, err)
	require.True(t, store.IsErrConflict(err), "duplicate page Id must map to ErrConflict, got %T: %v", err, err)
}

func TestPageIsValidSelfParent(t *testing.T) {
	p := newPage(mmmodel.NewId(), mmmodel.NewId(), mmmodel.NewId(), "")
	p.PreSave()
	p.ParentId = p.Id
	require.NotNil(t, p.IsValid(), "a page that is its own parent must be invalid")
}

// TestCTECycleDetection verifies that the recursive CTEs (GetPageDescendants and
// GetPageAncestors) terminate and return bounded results even when a ParentId cycle is
// present in the database (which cannot be created via the public API but can occur from
// raw SQL or data corruption).
//
// The CYCLE clause in the CTE marks each revisited node with is_cycle=true and stops
// recursing that branch; the WHERE NOT is_cycle filter then drops the sentinel row.
// This test creates a self-referential cycle (page.ParentId = page.Id) via raw SQL —
// bypassing the IsValid check — and asserts that both hierarchy queries return bounded
// results rather than looping.
func TestCTECycleDetection(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Create a valid page normally, then corrupt ParentId via raw SQL to create a
	// self-referential cycle (page → itself). The store's CreatePage calls IsValid
	// which rejects self-parent, so we must bypass it at the DB level.
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""))
	require.NoError(t, err)

	// Inject the self-parent cycle directly into the DB, bypassing app-layer validation.
	_, rawErr := s.RawExecForTest("UPDATE DOCS_Page SET ParentId = $1 WHERE Id = $1", created.Id)
	require.NoError(t, rawErr, "raw SQL cycle injection must succeed")

	// GetPageDescendants must terminate and return a bounded (possibly empty) result.
	descendants, descErr := s.GetPageDescendants(created.Id)
	// The self-cycle row is filtered by NOT is_cycle, so the result is empty.
	// We only care that it did NOT hang or panic — an empty result is correct.
	require.NoError(t, descErr, "GetPageDescendants must not error on a cycle")
	// descendants may be empty (cycle filtered) but must not loop forever.
	_ = descendants

	// GetPageAncestors must also terminate.
	ancestors, ancErr := s.GetPageAncestors(created.Id)
	require.NoError(t, ancErr, "GetPageAncestors must not error on a cycle")
	_ = ancestors
}

// TestGetPageDescendants_EmptyID verifies that GetPageDescendants rejects an empty pageID
// with ErrInvalidInput.
func TestGetPageDescendants_EmptyID(t *testing.T) {
	s := openTestDB(t)

	_, err := s.GetPageDescendants("")
	require.Error(t, err)
	require.True(t, store.IsErrInvalidInput(err), "empty pageID must return ErrInvalidInput; got %v", err)
}

// TestGetPage_EmptyID verifies that GetPage rejects an empty pageID with ErrInvalidInput.
func TestGetPage_EmptyID(t *testing.T) {
	s := openTestDB(t)

	_, err := s.GetPage("", false)
	require.Error(t, err)
	require.True(t, store.IsErrInvalidInput(err), "empty pageID must return ErrInvalidInput; got %v", err)
}

// TestUpdatePageWritesProps verifies that UpdatePage persists Props changes to the DB
// (not just to the in-memory struct).
func TestUpdatePageWritesProps(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""))
	require.NoError(t, err)

	// Set Props on the page and update via UpdatePage.
	toUpdate := created.Clone()
	toUpdate.Title = "Props Test"
	toUpdate.LastModifiedBy = userID
	toUpdate.Props = mmmodel.StringInterface{"myKey": "myValue"}

	updated, err := s.UpdatePage(toUpdate)
	require.NoError(t, err)
	require.Equal(t, "myValue", updated.Props["myKey"])

	// DB round-trip: re-fetch and verify Props persisted correctly.
	persisted, err := s.GetPage(created.Id, false)
	require.NoError(t, err)
	require.Equal(t, "myValue", persisted.Props["myKey"], "Props must be persisted to DB via UpdatePage")
}
