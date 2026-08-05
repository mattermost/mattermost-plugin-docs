// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sq "github.com/mattermost/squirrel"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// deletePageErr calls Store.DeletePage and discards the returned channel id, for call sites
// that only assert the error.
func deletePageErr(s *store.Store, pageID, spaceID, userID string) error {
	_, err := s.DeletePage(pageID, spaceID, userID)
	return err
}

// restorePageErr calls Store.RestorePage and discards the returned page, for call sites that
// only assert the error.
func restorePageErr(s *store.Store, pageID, spaceID, userID string, maxDepth int) error {
	_, err := s.RestorePage(pageID, spaceID, userID, maxDepth)
	return err
}

// testDefaultMaxDepth is the maxDepth passed to CreatePage by tests that aren't exercising the
// depth cap itself (see testutil.UncappedMaxDepth).
const testDefaultMaxDepth = testutil.UncappedMaxDepth

// testDraftListLimit is a limit large enough that a draft listing in these tests is never a
// partial page, so a test can assert on the whole set.
const testDraftListLimit = 100

// openTestDB opens an isolated Postgres schema for this test run, runs migrations into it, and
// returns the Store. The schema is dropped in t.Cleanup so parallel package runs never share
// tables.
func openTestDB(t *testing.T) *store.Store {
	t.Helper()
	s, _ := testutil.OpenTestStore(t)
	return s
}

func newSpace(channelID string) *model.Space {
	return testutil.NewSpace(channelID, mmmodel.NewId())
}

func newPage(spaceID, channelID, userID, parentID string) *model.Page {
	return testutil.NewPage(spaceID, channelID, userID, parentID)
}

// mustChildren returns a page's live child summaries, failing the test on error.
func mustChildren(t *testing.T, s *store.Store, pageID, spaceID string) []*model.PageSummary {
	t.Helper()
	children, err := s.GetPageChildren(pageID, spaceID, 0, 100)
	require.NoError(t, err)
	return children
}

// idsOf extracts the ids of a full page slice, preserving order.
func idsOf(pages []*model.Page) []string {
	ids := make([]string, len(pages))
	for i, p := range pages {
		ids[i] = p.Id
	}
	return ids
}

// summaryIDs extracts the ids of a page-summary slice, preserving order.
func summaryIDs(pages []*model.PageSummary) []string {
	ids := make([]string, len(pages))
	for i, p := range pages {
		ids[i] = p.Id
	}
	return ids
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

		got, err := s.GetSpace(saved.Id, false)
		require.NoError(t, err)
		require.Equal(t, saved.Id, got.Id)
		require.Equal(t, saved.Title, got.Title)
		require.Equal(t, channelID, got.ChannelId)
	})

	t.Run("update persists changed fields", func(t *testing.T) {
		s := openTestDB(t)

		saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		title := "Updated Title"
		updated, err := s.UpdateSpace(saved.Id, &model.SpacePatch{Title: &title}, saved.UpdateAt, false)
		require.NoError(t, err)
		require.Equal(t, "Updated Title", updated.Title)
	})

	t.Run("delete makes space not found", func(t *testing.T) {
		s := openTestDB(t)

		saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpace(saved.Id))

		_, err = s.GetSpace(saved.Id, false)
		require.True(t, store.IsErrNotFound(err), "expected not-found after delete, got %v", err)
	})

	t.Run("get nonexistent space returns not-found", func(t *testing.T) {
		s := openTestDB(t)

		_, err := s.GetSpace(mmmodel.NewId(), false)
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
	created, err := s.CreatePage(p, testDefaultMaxDepth)
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
	c1, err := s.CreatePage(p1, testDefaultMaxDepth)
	require.NoError(t, err)

	p2 := newPage(savedSpace.Id, channelID, userID, "")
	p2.Title = "Page 2"
	c2, err := s.CreatePage(p2, testDefaultMaxDepth)
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
	createdParent, err := s.CreatePage(parent, testDefaultMaxDepth)
	require.NoError(t, err)

	child := newPage(savedSpace.Id, channelID, userID, createdParent.Id)
	child.Title = "Child"
	_, err = s.CreatePage(child, testDefaultMaxDepth)
	require.NoError(t, err)

	children, err := s.GetPageChildren(createdParent.Id, savedSpace.Id, 0, 100)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, "Child", children[0].Title)
}

// TestGetPageChildren_NonPositiveLimit verifies that GetPageChildren rejects limit <= 0
// with ErrInvalidInput instead of silently returning an unbounded result.
func TestGetPageChildren_NonPositiveLimit(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	parent, err := s.CreatePage(newPage(space.Id, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
	require.NoError(t, err)

	for _, limit := range []int{0, -1} {
		_, err := s.GetPageChildren(parent.Id, space.Id, 0, limit)
		require.Error(t, err)
		require.True(t, store.IsErrInvalidInput(err), "limit=%d must return ErrInvalidInput; got %v", limit, err)
	}
}

func TestGetPageChildrenOrderedBySortOrder(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	savedSpace, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	userID := mmmodel.NewId()
	parent, err := s.CreatePage(newPage(savedSpace.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	first := newPage(savedSpace.Id, channelID, userID, parent.Id)
	first.Title = "First"
	createdFirst, err := s.CreatePage(first, testDefaultMaxDepth)
	require.NoError(t, err)

	second := newPage(savedSpace.Id, channelID, userID, parent.Id)
	second.Title = "Second"
	_, err = s.CreatePage(second, testDefaultMaxDepth)
	require.NoError(t, err)

	// Children come back in SortOrder order (here, creation order), not newest-first.
	children, err := s.GetPageChildren(parent.Id, savedSpace.Id, 0, 100)
	require.NoError(t, err)
	require.Len(t, children, 2)
	require.Equal(t, "First", children[0].Title)
	require.Equal(t, "Second", children[1].Title)

	// Reordering by SortOrder alone (CreateAt unchanged) reorders the listing, proving SortOrder
	// — not CreateAt — drives the order.
	// SortOrder is not a generic-patch field (reorder is a dedicated concern), so set it directly.
	newSortOrder := children[1].SortOrder + 1
	_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Page").Set("SortOrder", newSortOrder).Where(sq.Eq{"Id": createdFirst.Id}))
	require.NoError(t, err)

	reordered, err := s.GetPageChildren(parent.Id, savedSpace.Id, 0, 100)
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
	parent, err := s.CreatePage(newPage(savedSpace.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	createdChild, err := s.CreatePage(newPage(savedSpace.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	require.NoError(t, s.DeleteSpace(savedSpace.Id))

	// The space and all of its pages are soft-deleted together: none are fetchable by ID.
	_, err = s.GetSpace(savedSpace.Id, false)
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

	_, err = s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.Error(t, err, "creating a page in a soft-deleted space must fail")
	require.True(t, store.IsErrNotFound(err), "deleted space must map to ErrNotFound; got %T: %v", err, err)
}

// TestRestoreSpaceUncascadesCascadedPages verifies RestoreSpace un-deletes the space and the pages
// its delete cascaded, while leaving alone a page that was already deleted before the space was.
func TestRestoreSpaceUncascadesCascadedPages(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	child, err := s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// A page deleted before the space must stay deleted after restore.
	preDeleted, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	require.NoError(t, deletePageErr(s, preDeleted.Id, preDeleted.SpaceId, userID))

	require.NoError(t, s.DeleteSpace(space.Id))
	require.NoError(t, s.RestoreSpace(space.Id))

	_, err = s.GetSpace(space.Id, false)
	require.NoError(t, err, "space should be live after restore")

	_, err = s.GetPage(parent.Id, false)
	require.NoError(t, err, "cascade-deleted parent should be restored")
	_, err = s.GetPage(child.Id, false)
	require.NoError(t, err, "cascade-deleted child should be restored")

	_, err = s.GetPage(preDeleted.Id, false)
	require.True(t, store.IsErrNotFound(err), "page deleted before the space must stay deleted")
}

// TestRestoreSpaceChannelTakenConflict verifies RestoreSpace fails with ErrConflict when a new
// live space has claimed the deleted space's backing channel (uq_docs_space_channel_id).
func TestRestoreSpaceChannelTakenConflict(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	require.NoError(t, s.DeleteSpace(space.Id))

	// A new space reuses the now-free channel.
	_, err = s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	err = s.RestoreSpace(space.Id)
	require.Error(t, err)
	require.True(t, store.IsErrConflict(err), "expected ErrConflict, got %T: %v", err, err)
}

// TestRestoreSpaceNotDeleted verifies RestoreSpace distinguishes a live space (invalid input,
// nothing to restore) from a nonexistent one (not-found), deciding both under the row lock.
func TestRestoreSpaceNotDeleted(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	require.True(t, store.IsErrInvalidInput(s.RestoreSpace(space.Id)), "restoring a live space is invalid input")
	require.True(t, store.IsErrNotFound(s.RestoreSpace(mmmodel.NewId())), "restoring a missing space is not-found")
}

// TestRestoreSpaceAdvancesUpdateAtMonotonically verifies the delete→restore lifecycle advances
// the space's UpdateAt CAS token (used by UpdateSpace's optimistic lock) past any prior value,
// so a stale client baseline cannot stay valid across the round trip. A synthetic future
// UpdateAt makes the regression deterministic: a raw now-based restore would write a smaller value.
func TestRestoreSpaceAdvancesUpdateAtMonotonically(t *testing.T) {
	s := openTestDB(t)
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	// Simulate a prior synthetic monotonic UpdateAt above the wall clock.
	future := mmmodel.GetMillis() + 1_000_000
	_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Space").Set("UpdateAt", future).Where(sq.Eq{"Id": space.Id}))
	require.NoError(t, err)

	require.NoError(t, s.DeleteSpace(space.Id))
	require.NoError(t, s.RestoreSpace(space.Id))

	restored, err := s.GetSpace(space.Id, false)
	require.NoError(t, err)
	require.Greater(t, restored.UpdateAt, future, "delete→restore must advance UpdateAt past any prior value")
}

// TestRestoreSpaceStampExceedsExistingDeleteAt verifies that DeleteSpace stamps its cascade
// strictly above any DeleteAt already on the space's pages, so RestoreSpace never sweeps in a
// page deleted individually at the same millisecond as the space. The pre-deleted page's
// DeleteAt is forced above the wall clock to make the collision deterministic; the old
// now-based stamp would equal-or-trail it and restore the wrong page.
func TestRestoreSpaceStampExceedsExistingDeleteAt(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	cascaded, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	individual, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// Delete one page individually, then force its DeleteAt above the wall clock so a now-based
	// cascade stamp would collide with (or trail) it.
	require.NoError(t, deletePageErr(s, individual.Id, individual.SpaceId, userID))
	collisionStamp := mmmodel.GetMillis() + 1_000_000
	_, rawErr := s.RawExecForTest("UPDATE DOCS_Page SET DeleteAt = $2 WHERE Id = $1", individual.Id, collisionStamp)
	require.NoError(t, rawErr)

	require.NoError(t, s.DeleteSpace(space.Id))

	// The cascade stamp (carried by the cascaded page) must be strictly greater than the
	// individually-deleted page's DeleteAt.
	cascadedDeleted, err := s.GetPage(cascaded.Id, true)
	require.NoError(t, err)
	require.Greater(t, cascadedDeleted.DeleteAt, collisionStamp, "cascade stamp must exceed any existing page DeleteAt")

	require.NoError(t, s.RestoreSpace(space.Id))

	_, err = s.GetPage(cascaded.Id, false)
	require.NoError(t, err, "cascade-deleted page should be restored")
	_, err = s.GetPage(individual.Id, false)
	require.True(t, store.IsErrNotFound(err), "individually-deleted page must stay deleted")
}

func TestFetchDescendantRows(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()

	root := newPage(savedSpace.Id, channelID, userID, "")
	root.Title = "Root"
	createdRoot, err := s.CreatePage(root, testDefaultMaxDepth)
	require.NoError(t, err)

	child := newPage(savedSpace.Id, channelID, userID, createdRoot.Id)
	child.Title = "Child"
	createdChild, err := s.CreatePage(child, testDefaultMaxDepth)
	require.NoError(t, err)

	grandchild := newPage(savedSpace.Id, channelID, userID, createdChild.Id)
	grandchild.Title = "Grandchild"
	_, err = s.CreatePage(grandchild, testDefaultMaxDepth)
	require.NoError(t, err)

	descendants, err := s.FetchDescendantRowsForTest(createdRoot.Id)
	require.NoError(t, err)
	require.Len(t, descendants, 2) // child + grandchild (root excluded)
}

func TestDepthBoundaryExact(t *testing.T) {
	const maxDepth = model.MaxPageDepth // the store CTE uses the larger MaxPageHierarchyDepth (50)

	s := openTestDB(t)

	channelID := mmmodel.NewId()
	w := newSpace(channelID)
	savedSpace, err := s.CreateSpace(w)
	require.NoError(t, err)

	userID := mmmodel.NewId()

	// Build a chain of maxDepth pages (root at depth 1), passing maxDepth as CreatePage's cap
	// so the chain itself exercises the boundary rather than just bypassing it.
	parentID := ""
	var lastPage *model.Page
	for depth := 1; depth <= maxDepth; depth++ {
		pg := newPage(savedSpace.Id, channelID, userID, parentID)
		pg.Title = fmt.Sprintf("depth-%d", depth)
		created, createErr := s.CreatePage(pg, maxDepth)
		require.NoError(t, createErr, "create page at depth %d", depth)
		parentID = created.Id
		lastPage = created
	}
	require.NotNil(t, lastPage)

	// One level past the cap must be rejected atomically by CreatePage itself, with the same
	// limit-exceeded error type every other depth-cap path returns.
	tooDeep := newPage(savedSpace.Id, channelID, userID, parentID)
	_, createErr := s.CreatePage(tooDeep, maxDepth)
	require.Error(t, createErr)
	var limErr *store.ErrLimitExceeded
	require.True(t, errors.As(createErr, &limErr), "expected ErrLimitExceeded, got %v", createErr)
	require.Equal(t, store.ReasonMaxDepthExceeded, limErr.Reason)
}

// TestFetchDescendantRowsDepthLimitExceeded verifies FetchDescendantRowsForTest returns
// ErrLimitExceeded rather than silently truncating when the subtree is deeper than
// MaxPageHierarchyDepth. The chain is built with testDefaultMaxDepth, well past
// MaxPageHierarchyDepth, so CreatePage's own cap doesn't interfere with reaching the
// read-side limit under test.
func TestFetchDescendantRowsDepthLimitExceeded(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	root, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	parentID := root.Id
	for range store.MaxPageHierarchyDepth + 2 {
		child, createErr := s.CreatePage(newPage(space.Id, channelID, userID, parentID), testDefaultMaxDepth)
		require.NoError(t, createErr)
		parentID = child.Id
	}

	_, err = s.FetchDescendantRowsForTest(root.Id)
	require.Error(t, err)
	require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %v", err)
}

// TestFetchDescendantRowsDepthAtCapAllowed verifies the depth cap counts edges below the
// requested page: a subtree exactly MaxPageHierarchyDepth levels deep is returned in full,
// guarding the off-by-one at the boundary.
func TestFetchDescendantRowsDepthAtCapAllowed(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	root, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// Build exactly MaxPageHierarchyDepth descendants chained below the root.
	parentID := root.Id
	for range store.MaxPageHierarchyDepth {
		child, createErr := s.CreatePage(newPage(space.Id, channelID, userID, parentID), testDefaultMaxDepth)
		require.NoError(t, createErr)
		parentID = child.Id
	}

	descendants, err := s.FetchDescendantRowsForTest(root.Id)
	require.NoError(t, err, "a subtree exactly at the depth cap must not error")
	require.Len(t, descendants, store.MaxPageHierarchyDepth)
}

// TestFetchDescendantRowsByteBudgetExceeded verifies FetchDescendantRowsForTest returns
// ErrLimitExceeded (with ReasonSubtreeTotalBytesExceeded) rather than materializing the full
// result set when the subtree's combined Body+SearchText size exceeds
// MaxPageDescendantsTotalBytes, even though the row count itself stays well under
// MaxPageDescendantsLimit.
func TestFetchDescendantRowsByteBudgetExceeded(t *testing.T) {
	s := openTestDB(t)
	channelID, root := seedSpaceAndPage(t, s)

	largeBody := strings.Repeat("a", model.PageBodyMaxBytes)
	largeSearchText := strings.Repeat("a", model.PageSearchTextMaxBytes)
	perPageBytes := len(largeBody) + len(largeSearchText)
	pageCount := store.MaxPageDescendantsTotalBytes/perPageBytes + 2

	require.Less(t, pageCount, store.MaxPageDescendantsLimit, "test must exceed the byte budget without also exceeding the row-count cap")

	for range pageCount {
		child := newPage(root.SpaceId, channelID, mmmodel.NewId(), root.Id)
		child.Body = largeBody
		child.SearchText = largeSearchText
		_, err := s.CreatePage(child, testDefaultMaxDepth)
		require.NoError(t, err)
	}

	_, err := s.FetchDescendantRowsForTest(root.Id)
	require.Error(t, err)
	require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %T: %v", err, err)
	var limErr *store.ErrLimitExceeded
	require.True(t, errors.As(err, &limErr))
	require.Equal(t, store.ReasonSubtreeTotalBytesExceeded, limErr.Reason)
}

// TestFetchDescendantRowsByteBudgetAllowed verifies a subtree comfortably under
// MaxPageDescendantsTotalBytes (and MaxPageDescendantsLimit) is returned in full, so the new
// byte guard does not interfere with ordinary subtree fetches.
func TestFetchDescendantRowsByteBudgetAllowed(t *testing.T) {
	s := openTestDB(t)
	channelID, root := seedSpaceAndPage(t, s)

	for i := 0; i < 3; i++ {
		child := newPage(root.SpaceId, channelID, mmmodel.NewId(), root.Id)
		child.Body = fmt.Sprintf("small body %d", i)
		_, err := s.CreatePage(child, testDefaultMaxDepth)
		require.NoError(t, err)
	}

	descendants, err := s.FetchDescendantRowsForTest(root.Id)
	require.NoError(t, err)
	require.Len(t, descendants, 3)
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
	created, err := s.CreatePage(p, testDefaultMaxDepth)
	require.NoError(t, err)

	// Update once to advance EditAt.
	firstTitle := "First Update"
	updated, err := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &firstTitle}, created.EditAt, false, userID)
	require.NoError(t, err)

	// Assert EditAt actually advanced before we try the stale path; if it didn't,
	// the conflict test below would pass trivially for the wrong reason.
	require.Greater(t, updated.EditAt, created.EditAt, "EditAt must advance after UpdatePage")

	// Try to update with the original (now-stale) EditAt as the CAS baseline.
	conflictTitle := "Conflict"
	_, conflictErr := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &conflictTitle}, created.EditAt, false, userID)
	require.Error(t, conflictErr, "update with stale EditAt must fail")
	require.True(t, store.IsErrConflict(conflictErr), "stale-EditAt update must return ErrConflict; got %v", conflictErr)

	freshTitle := "Fresh Update"
	_, freshErr := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &freshTitle}, updated.EditAt, false, userID)
	require.NoError(t, freshErr, "update with correct EditAt must succeed")

	forcedTitle := "Forced"
	forcedResult, forcedErr := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &forcedTitle}, created.EditAt, true, userID)
	require.NoError(t, forcedErr, "forced update with stale EditAt must overwrite, not conflict")
	require.Equal(t, "Forced", forcedResult.Title)
}

// TestUpdateSpaceCASConflict verifies UpdateSpace's optimistic locking: a second update
// carrying a stale UpdateAt is rejected with ErrConflict, and updating a soft-deleted
// space returns ErrNotFound (not ErrConflict).
func TestUpdateSpaceCASConflict(t *testing.T) {
	s := openTestDB(t)

	saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	// Capture the original (now-stale) baseline before advancing UpdateAt.
	staleBaseline := saved.UpdateAt

	firstTitle := "First"
	updated, err := s.UpdateSpace(saved.Id, &model.SpacePatch{Title: &firstTitle}, saved.UpdateAt, false)
	require.NoError(t, err)
	require.Greater(t, updated.UpdateAt, staleBaseline, "UpdateAt must advance")

	// DB round-trip: persisted Title and UpdateAt must match what was returned in-memory.
	persisted, err := s.GetSpace(updated.Id, false)
	require.NoError(t, err)
	require.Equal(t, "First", persisted.Title, "persisted Title must match returned struct")
	require.Equal(t, updated.UpdateAt, persisted.UpdateAt, "persisted UpdateAt must match returned struct")

	// Re-submitting with the stale baseline must conflict.
	staleTitle := "Stale"
	_, conflictErr := s.UpdateSpace(saved.Id, &model.SpacePatch{Title: &staleTitle}, staleBaseline, false)
	require.Error(t, conflictErr)
	require.True(t, store.IsErrConflict(conflictErr), "stale UpdateAt must return ErrConflict; got %v", conflictErr)

	// Deleting then updating must return NotFound, not Conflict.
	require.NoError(t, s.DeleteSpace(updated.Id))
	afterDeleteTitle := "After delete"
	_, delErr := s.UpdateSpace(updated.Id, &model.SpacePatch{Title: &afterDeleteTitle}, updated.UpdateAt, false)
	require.Error(t, delErr)
	require.True(t, store.IsErrNotFound(delErr), "updating a deleted space must return ErrNotFound; got %v", delErr)
}

// TestUpdateSpaceForceAndDefault verifies the optimistic-lock default is fail-safe: a zero
// baseline conflicts rather than silently overwriting, while force bypasses the check.
func TestUpdateSpaceForceAndDefault(t *testing.T) {
	s := openTestDB(t)

	saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	// A missing baseline (UpdateAt == 0) must conflict by default, not last-write-wins.
	noBaselineTitle := "No baseline"
	_, conflictErr := s.UpdateSpace(saved.Id, &model.SpacePatch{Title: &noBaselineTitle}, 0, false)
	require.Error(t, conflictErr)
	require.True(t, store.IsErrConflict(conflictErr), "zero baseline must conflict by default; got %v", conflictErr)

	// force overwrites unconditionally even with a zero baseline.
	forced, forceErr := s.UpdateSpace(saved.Id, &model.SpacePatch{Title: &noBaselineTitle}, 0, true)
	require.NoError(t, forceErr)
	require.Equal(t, "No baseline", forced.Title)
	require.Greater(t, forced.UpdateAt, saved.UpdateAt, "UpdateAt must advance under force")
}

// TestUpdateSpaceForceMergesPatch verifies that a forced update merges the patch into the row
// read under lock: fields the patch leaves nil keep a concurrent writer's value instead of
// being clobbered by the forcing caller's stale snapshot.
func TestUpdateSpaceForceMergesPatch(t *testing.T) {
	s := openTestDB(t)

	saved, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	description := "concurrent description"
	afterConcurrent, err := s.UpdateSpace(saved.Id, &model.SpacePatch{Description: &description}, saved.UpdateAt, false)
	require.NoError(t, err)

	// Force a title-only update from the pre-description baseline.
	title := "forced title"
	forced, forceErr := s.UpdateSpace(saved.Id, &model.SpacePatch{Title: &title}, saved.UpdateAt, true)
	require.NoError(t, forceErr)
	require.Equal(t, "forced title", forced.Title)
	require.Equal(t, "concurrent description", forced.Description, "force must not clobber fields the patch omits")
	require.Greater(t, forced.UpdateAt, afterConcurrent.UpdateAt)
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
	require.NotNil(t, p.IsValid(), "page with invalid ID must fail IsValid")
}

// TestFetchDescendantRows_ExcludesUnrelatedSubtrees verifies that
// FetchDescendantRowsForTest for a mid-tree node returns only its own subtree
// and not siblings or their children.
func TestFetchDescendantRows_ExcludesUnrelatedSubtrees(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Root with two children; childA has its own grandchild.
	root, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	childA, err := s.CreatePage(newPage(space.Id, channelID, userID, root.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	_, err = s.CreatePage(newPage(space.Id, channelID, userID, root.Id), testDefaultMaxDepth) // childB — unrelated subtree
	require.NoError(t, err)
	grandchild, err := s.CreatePage(newPage(space.Id, channelID, userID, childA.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// Descendants of childA must be only grandchild, not childB.
	descendants, err := s.FetchDescendantRowsForTest(childA.Id)
	require.NoError(t, err)
	require.Len(t, descendants, 1, "childA must have exactly one descendant (grandchild)")
	require.Equal(t, grandchild.Id, descendants[0].Id)
}

// TestFetchDescendantRows_ExcludesVersionSnapshots verifies the descendants CTE skips version
// snapshot rows. The snapshot is seeded in its schema-enforced shape (OriginalId != "" and
// soft-deleted, per chk_docs_page_snapshot_deleted).
func TestFetchDescendantRows_ExcludesVersionSnapshots(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	root, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	child, err := s.CreatePage(newPage(space.Id, channelID, userID, root.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	snap, err := s.CreatePage(newPage(space.Id, channelID, userID, root.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	_, rawErr := s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Page").
		Set("OriginalId", mmmodel.NewId()).
		Set("DeleteAt", mmmodel.GetMillis()).
		Where(sq.Eq{"Id": snap.Id}))
	require.NoError(t, rawErr)

	descendants, err := s.FetchDescendantRowsForTest(root.Id)
	require.NoError(t, err)
	require.Len(t, descendants, 1, "the snapshot row must be excluded from the descendant set")
	require.Equal(t, child.Id, descendants[0].Id)

	// A snapshot root itself yields no descendants (seed-arm exclusion).
	fromSnap, err := s.FetchDescendantRowsForTest(snap.Id)
	require.NoError(t, err)
	require.Empty(t, fromSnap, "a snapshot root must have no descendant set")
}

// TestFetchDescendantRows_LeafHasZeroDescendants verifies that a leaf page
// (no children) returns an empty descendant list.
func TestFetchDescendantRows_LeafHasZeroDescendants(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	leaf, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	descendants, err := s.FetchDescendantRowsForTest(leaf.Id)
	require.NoError(t, err)
	require.Empty(t, descendants, "leaf page must have zero descendants")
}

func TestGetSpacesForTeam(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	teamID := mmmodel.NewId()
	chVisible := mmmodel.NewId()
	chHidden := mmmodel.NewId()

	visible := newSpace(chVisible)
	visible.TeamId = teamID
	_, err := s.CreateSpace(visible)
	require.NoError(t, err)

	hidden := newSpace(chHidden)
	hidden.TeamId = teamID
	_, err = s.CreateSpace(hidden)
	require.NoError(t, err)

	// Space in a different team must not be returned even when the user is a member of its channel.
	otherChannel := mmmodel.NewId()
	other := newSpace(otherChannel)
	_, err = s.CreateSpace(other)
	require.NoError(t, err)

	memberOfAll := mmmodel.NewId()
	for _, ch := range []string{chVisible, chHidden, otherChannel} {
		testutil.MustAddChannelMember(t, db, ch, memberOfAll)
	}
	memberOfOne := mmmodel.NewId()
	testutil.MustAddChannelMember(t, db, chVisible, memberOfOne)

	t.Run("returns every team space whose backing channel the user belongs to", func(t *testing.T) {
		spaces, err := s.GetSpacesForTeam(teamID, memberOfAll, 0, 100)
		require.NoError(t, err)
		require.Len(t, spaces, 2)
	})

	t.Run("filters to the user's channel memberships", func(t *testing.T) {
		spaces, err := s.GetSpacesForTeam(teamID, memberOfOne, 0, 100)
		require.NoError(t, err)
		require.Len(t, spaces, 1)
		require.Equal(t, visible.Id, spaces[0].Id)
	})

	t.Run("user with no memberships gets an empty result", func(t *testing.T) {
		spaces, err := s.GetSpacesForTeam(teamID, mmmodel.NewId(), 0, 100)
		require.NoError(t, err)
		require.Empty(t, spaces)
	})

	t.Run("pagination excludes hidden spaces before offset/limit", func(t *testing.T) {
		// Only 1 visible space; with per_page=10 and 2 total, hidden must not count toward has_more.
		spaces, err := s.GetSpacesForTeam(teamID, memberOfOne, 0, 10)
		require.NoError(t, err)
		require.Len(t, spaces, 1)
	})

	t.Run("rejects empty userID", func(t *testing.T) {
		_, err := s.GetSpacesForTeam(teamID, "", 0, 100)
		require.Error(t, err)
		require.True(t, store.IsErrInvalidInput(err))
	})

	t.Run("rejects non-positive limit", func(t *testing.T) {
		for _, limit := range []int{0, -1} {
			_, err := s.GetSpacesForTeam(teamID, memberOfAll, 0, limit)
			require.Error(t, err)
			require.True(t, store.IsErrInvalidInput(err), "limit=%d must return ErrInvalidInput; got %v", limit, err)
		}
	})
}

// TestGetSpacePages_NonPositiveLimit verifies that GetSpacePages rejects limit <= 0
// with ErrInvalidInput instead of silently returning an unbounded result.
func TestGetSpacePages_NonPositiveLimit(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	for _, limit := range []int{0, -1} {
		_, err := s.GetSpacePages(space.Id, 0, limit)
		require.Error(t, err)
		require.True(t, store.IsErrInvalidInput(err), "limit=%d must return ErrInvalidInput; got %v", limit, err)
	}
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
	_, err = s.CreatePage(first, testDefaultMaxDepth)
	require.NoError(t, err)

	second := newPage(savedSpace.Id, channelID, userID, "")
	second.Id = pageID
	_, err = s.CreatePage(second, testDefaultMaxDepth)
	require.Error(t, err)
	require.True(t, store.IsErrConflict(err), "duplicate page Id must map to ErrConflict, got %T: %v", err, err)
}

func TestPageIsValidSelfParent(t *testing.T) {
	p := newPage(mmmodel.NewId(), mmmodel.NewId(), mmmodel.NewId(), "")
	p.PreSave()
	p.ParentId = p.Id
	require.NotNil(t, p.IsValid(), "a page that is its own parent must be invalid")
}

// TestCTECycleDetection verifies that the recursive CTE (FetchDescendantRowsForTest)
// terminates and returns bounded results even when a ParentId cycle is
// present in the database (which cannot be created via the public API but can occur from
// raw SQL or data corruption).
//
// The path array in the CTE accumulates visited IDs; NOT (p.Id = ANY(path)) stops recursion
// on any revisited node, preventing an infinite loop.
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
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// Inject the self-parent cycle directly into the DB, bypassing app-layer validation.
	_, rawErr := s.RawExecForTest("UPDATE DOCS_Page SET ParentId = $1 WHERE Id = $1", created.Id)
	require.NoError(t, rawErr, "raw SQL cycle injection must succeed")

	// FetchDescendantRowsForTest must terminate and return a bounded result. The self-cycle is
	// broken by the path guard and the root is excluded, so the result is empty.
	descendants, descErr := s.FetchDescendantRowsForTest(created.Id)
	require.NoError(t, descErr, "FetchDescendantRowsForTest must not error on a cycle")
	require.Empty(t, descendants, "a page must not appear in its own descendant set")
}

// TestFetchDescendantRows_EmptyID verifies that FetchDescendantRowsForTest rejects an empty pageID
// with ErrInvalidInput.
func TestFetchDescendantRows_EmptyID(t *testing.T) {
	s := openTestDB(t)

	_, err := s.FetchDescendantRowsForTest("")
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
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	newTitle := "Props Test"
	newProps := mmmodel.StringInterface{"myKey": "myValue"}
	updated, err := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &newTitle, Props: &newProps}, created.EditAt, false, userID)
	require.NoError(t, err)
	require.Equal(t, "myValue", updated.Props["myKey"])

	// DB round-trip: re-fetch and verify Props persisted correctly.
	persisted, err := s.GetPage(created.Id, false)
	require.NoError(t, err)
	require.Equal(t, "myValue", persisted.Props["myKey"], "Props must be persisted to DB via UpdatePage")
}

// TestDeletePage verifies a soft-delete: the row leaves the live view but is fetchable with
// includeDeleted, its live children are promoted to the page's parent, and bad ids are rejected.
func TestDeletePage(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	t.Run("soft-deletes a page: hidden from the live view, visible with includeDeleted", func(t *testing.T) {
		created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		// Two users hold drafts for this page; both must be hard-deleted when the page is.
		// UpsertDraft requires the EditAt baseline when the page row already exists.
		otherUserID := mmmodel.NewId()
		withBaseline := func(uid string) *model.Draft {
			d := newDraft(uid, space.Id, created.Id, "")
			d.BaseEditAt = created.EditAt
			return d
		}
		_, _, err = s.UpsertDraft(withBaseline(userID), nil, nil, nil)
		require.NoError(t, err)
		_, _, err = s.UpsertDraft(withBaseline(otherUserID), nil, nil, nil)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, created.Id, created.SpaceId, userID))

		_, err = s.GetPage(created.Id, false)
		require.True(t, store.IsErrNotFound(err), "expected not-found after delete")

		got, err := s.GetPage(created.Id, true)
		require.NoError(t, err)
		require.NotZero(t, got.DeleteAt)

		// The page's drafts are hard-deleted, across users.
		_, err = s.GetDraft(userID, created.Id)
		require.True(t, store.IsErrNotFound(err), "owner draft must be purged on page delete")
		_, err = s.GetDraft(otherUserID, created.Id)
		require.True(t, store.IsErrNotFound(err), "other user's draft must be purged on page delete")
	})

	t.Run("reparents live children to the deleted page's parent", func(t *testing.T) {
		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		child, err := s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, parent.Id, parent.SpaceId, userID))

		gotChild, err := s.GetPage(child.Id, false)
		require.NoError(t, err)
		require.Equal(t, parent.ParentId, gotChild.ParentId, "child must be reparented to the deleted page's parent")
	})

	t.Run("reparents draft UpdateAt monotonically", func(t *testing.T) {
		// reparentDraftsForPage uses GREATEST(now, UpdateAt+1) rather than a plain SET UpdateAt=now.
		// Without GREATEST, a reparent whose `now` was captured before a concurrent autosave could
		// move UpdateAt backward, letting a stale publish CAS-delete match a row it should not touch.
		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		// New-page draft whose pending parent is the published page.
		draftPageID := mmmodel.NewId()
		parentID := parent.Id
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, draftPageID, ""), &parentID, nil, nil)
		require.NoError(t, err)

		// Force the draft's stored UpdateAt ahead of wall clock, so a plain SET UpdateAt=now would move
		// it backward. Only the GREATEST(now, UpdateAt+1) bump keeps it strictly advancing — a
		// wall-clock-only reparent would fail the assertion below, which is what makes this a real guard.
		future := mmmodel.GetMillis() + 60*60*1000
		_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
			Update("DOCS_Draft").
			Set("UpdateAt", future).
			Where(sq.Eq{"UserId": userID, "PageId": draftPageID}))
		require.NoError(t, err)

		// Deleting the parent triggers reparentDraftsForPage on this draft.
		require.NoError(t, deletePageErr(s, parent.Id, space.Id, userID))

		after, err := s.GetDraft(userID, draftPageID)
		require.NoError(t, err)
		require.Equal(t, future+1, after.UpdateAt,
			"reparent must strictly advance UpdateAt to stored+1 so a stale publish CAS cannot match the reparented token")
	})

	t.Run("missing page returns not-found", func(t *testing.T) {
		require.True(t, store.IsErrNotFound(deletePageErr(s, mmmodel.NewId(), space.Id, userID)))
	})

	t.Run("empty id returns invalid-input", func(t *testing.T) {
		require.True(t, store.IsErrInvalidInput(deletePageErr(s, "", space.Id, userID)))
	})
}

// TestRestorePage verifies restore: the page becomes live again, children promoted at delete
// time stay put, and a live page is not restorable.
func TestRestorePage(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	t.Run("restores a soft-deleted page", func(t *testing.T) {
		created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, created.Id, created.SpaceId, userID))
		require.NoError(t, restorePageErr(s, created.Id, created.SpaceId, userID, testDefaultMaxDepth))

		got, err := s.GetPage(created.Id, false)
		require.NoError(t, err)
		require.Zero(t, got.DeleteAt)
	})

	t.Run("leaves promoted children under the grandparent on restore", func(t *testing.T) {
		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		child, err := s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, parent.Id, parent.SpaceId, userID))
		promoted, err := s.GetPage(child.Id, false)
		require.NoError(t, err)
		require.Equal(t, parent.ParentId, promoted.ParentId)

		require.NoError(t, restorePageErr(s, parent.Id, parent.SpaceId, userID, testDefaultMaxDepth))

		restored, err := s.GetPage(parent.Id, false)
		require.NoError(t, err)
		require.Zero(t, restored.DeleteAt)

		// Matching Confluence: un-deleting the parent does not pull the child back; it stays promoted.
		stillPromoted, err := s.GetPage(child.Id, false)
		require.NoError(t, err)
		require.Equal(t, parent.ParentId, stillPromoted.ParentId, "promoted child must stay under the grandparent after restore")
	})

	t.Run("a live page is not restorable", func(t *testing.T) {
		live, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		// Decided atomically under the page's row lock (see RestorePage), matching
		// RestoreSpace's already-live convention — not a generic not-found.
		require.True(t, store.IsErrInvalidInput(restorePageErr(s, live.Id, live.SpaceId, userID, testDefaultMaxDepth)))
	})

	t.Run("returns the restored page matching the persisted row", func(t *testing.T) {
		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		child, err := s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, child.Id, child.SpaceId, userID))
		require.NoError(t, deletePageErr(s, parent.Id, parent.SpaceId, userID))

		restorer := mmmodel.NewId()
		restored, err := s.RestorePage(child.Id, child.SpaceId, restorer, testDefaultMaxDepth)
		require.NoError(t, err)
		require.NotNil(t, restored)

		require.Equal(t, child.Id, restored.Id)
		require.Zero(t, restored.DeleteAt)
		require.Empty(t, restored.ParentId, "a deleted parent must fall back to the space root")
		require.Equal(t, restorer, restored.LastModifiedBy)
		require.Greater(t, restored.UpdateAt, child.UpdateAt)
		require.Greater(t, restored.EditAt, child.EditAt)

		got, err := s.GetPage(child.Id, false)
		require.NoError(t, err)
		require.Equal(t, got, restored, "the returned page must match the persisted row")
	})
}

// TestRestorePageRejectsDeletedSpace verifies a page cannot be restored once its space is
// deleted — there is nowhere live to restore it into.
func TestRestorePageRejectsDeletedSpace(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, page.Id, page.SpaceId, userID))
	require.NoError(t, s.DeleteSpace(space.Id))

	require.True(t, store.IsErrNotFound(restorePageErr(s, page.Id, page.SpaceId, userID, testDefaultMaxDepth)), "must not restore into a deleted space")
}

// TestRestorePageFallsBackToRootWhenParentDeleted verifies that if the original parent is
// gone, restore lands the page at the space root instead of failing (matching Confluence).
func TestRestorePageFallsBackToRootWhenParentDeleted(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	child, err := s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// Delete the child, then the parent; restoring the child now falls back to root.
	require.NoError(t, deletePageErr(s, child.Id, child.SpaceId, userID))
	require.NoError(t, deletePageErr(s, parent.Id, parent.SpaceId, userID))

	require.NoError(t, restorePageErr(s, child.Id, child.SpaceId, userID, testDefaultMaxDepth), "restore must succeed by falling back to root")

	restored, err := s.GetPage(child.Id, false)
	require.NoError(t, err)
	require.Zero(t, restored.DeleteAt)
	require.Empty(t, restored.ParentId, "child must be restored at the space root when its parent is gone")
}

// TestRestorePageFallsBackToRootWhenParentTooDeep verifies that if restoring under the original
// parent would exceed maxDepth, restore lands the page at the space root instead of failing —
// the same "never fail" fallback as a deleted parent, re-checked atomically under the parent's
// lock since the parent may have moved deeper since this page was deleted.
func TestRestorePageFallsBackToRootWhenParentTooDeep(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	child, err := s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, child.Id, child.SpaceId, userID))

	// parent is live at depth 1, so restoring the child under it would land at depth 2 — over a
	// cap of 1.
	require.NoError(t, restorePageErr(s, child.Id, child.SpaceId, userID, 1), "restore must succeed by falling back to root")

	restored, err := s.GetPage(child.Id, false)
	require.NoError(t, err)
	require.Zero(t, restored.DeleteAt)
	require.Empty(t, restored.ParentId, "child must be restored at the space root when its parent is too deep")
}

// TestRestorePageAppendsAtEndOfSiblingGroup verifies a restored page is appended at the end of
// its destination group, not given back its stale pre-delete SortOrder (now held by a promoted child).
func TestRestorePageAppendsAtEndOfSiblingGroup(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Grandparent g with children a, p (SortOrder 1, 2). p has children c1, c2.
	g, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	a, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	p, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	c1, err := s.CreatePage(newPage(space.Id, channelID, userID, p.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	c2, err := s.CreatePage(newPage(space.Id, channelID, userID, p.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// Delete p: c1, c2 are promoted into p's old slot, so g's children become a, c1, c2.
	require.NoError(t, deletePageErr(s, p.Id, p.SpaceId, userID))
	require.Equal(t, []string{a.Id, c1.Id, c2.Id}, summaryIDs(mustChildren(t, s, g.Id, space.Id)),
		"sanity: promoted children take the deleted page's slot")

	// Restore p: it must append at the end (a, c1, c2, p), not reclaim its stale slot 2.
	require.NoError(t, restorePageErr(s, p.Id, p.SpaceId, userID, testDefaultMaxDepth))
	children := mustChildren(t, s, g.Id, space.Id)
	require.Equal(t, []string{a.Id, c1.Id, c2.Id, p.Id}, summaryIDs(children),
		"restored page must be appended at the end of the sibling group")

	// No two live siblings may share a SortOrder, or the listing order would be unstable.
	seen := make(map[int64]bool, len(children))
	for _, c := range children {
		require.False(t, seen[c.SortOrder], "SortOrder %d reused across live siblings", c.SortOrder)
		seen[c.SortOrder] = true
	}
}

// TestDeleteRestoreAdvancesEditAt verifies the CAS token (EditAt) advances across a delete+restore
// cycle, so a client holding the pre-delete token still hits a conflict instead of clobbering state.
func TestDeleteRestoreAdvancesEditAt(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, created.Id, created.SpaceId, userID))
	require.NoError(t, restorePageErr(s, created.Id, created.SpaceId, userID, testDefaultMaxDepth))

	restored, err := s.GetPage(created.Id, false)
	require.NoError(t, err)
	require.Greater(t, restored.EditAt, created.EditAt, "EditAt must advance across delete+restore")

	// A write carrying the pre-delete EditAt is now stale and must conflict.
	staleTitle := "Stale Through Restore"
	_, conflictErr := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &staleTitle}, created.EditAt, false, userID)
	require.True(t, store.IsErrConflict(conflictErr),
		"update with pre-delete EditAt must return ErrConflict; got %v", conflictErr)
}

// TestDeletePageCreateChildConcurrency stresses the create-vs-delete race: a page is deleted
// while a child is concurrently created under it. The FOR UPDATE lock on the target row holds
// the "no live page under a deleted parent" invariant — the create either loses or is promoted.
func TestDeletePageCreateChildConcurrency(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	for i := range 25 {
		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		var wg sync.WaitGroup
		var delErr, createErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			delErr = deletePageErr(s, parent.Id, parent.SpaceId, userID)
		}()
		go func() {
			defer wg.Done()
			_, createErr = s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
		}()
		wg.Wait()

		// The delete must always succeed, else the invariant check passes trivially.
		require.NoError(t, delErr, "delete must succeed (iteration %d)", i)
		// The create either won the race or lost it (parent already gone → invalid input).
		if createErr != nil {
			require.True(t, store.IsErrInvalidInput(createErr),
				"losing create must return invalid-parent; got %v (iteration %d)", createErr, i)
		}

		children, err := s.GetPageChildren(parent.Id, space.Id, 0, 100)
		require.NoError(t, err)
		require.Empty(t, children, "a live child must never remain under a deleted parent (iteration %d)", i)
	}
}

// TestCreatePageConcurrentSortOrderUnique verifies the advisory lock keyed on
// (channelId, parentId) serializes concurrent CreatePage calls so no two siblings end up
// with the same SortOrder.
func TestCreatePageConcurrentSortOrderUnique(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			_, errs[i] = s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
		}()
	}
	wg.Wait()

	for i, cErr := range errs {
		require.NoError(t, cErr, "concurrent create %d must succeed", i)
	}

	children, err := s.GetPageChildren(parent.Id, space.Id, 0, 100)
	require.NoError(t, err)
	require.Len(t, children, n)
	seen := make(map[int64]bool, len(children))
	for _, c := range children {
		require.False(t, seen[c.SortOrder], "duplicate SortOrder %d — advisory lock failed to serialize", c.SortOrder)
		seen[c.SortOrder] = true
	}
}

// TestDeletePagePromotedChildrenTakeDeletedPosition verifies promoted children take the deleted
// page's slot (in their original order), not their old SortOrder from the removed sibling group.
func TestDeletePagePromotedChildrenTakeDeletedPosition(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Grandparent with three children created in order a, b, p (SortOrder 1, 2, 3).
	g, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	a, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	b, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	p, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// p's own children (SortOrder 1, 2). Their low order is exactly what would hoist them
	// above b if it leaked into g's sibling group.
	c1, err := s.CreatePage(newPage(space.Id, channelID, userID, p.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	c2, err := s.CreatePage(newPage(space.Id, channelID, userID, p.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, p.Id, p.SpaceId, userID))

	children, err := s.GetPageChildren(g.Id, space.Id, 0, 100)
	require.NoError(t, err)
	got := make([]string, len(children))
	for i, c := range children {
		got[i] = c.Id
	}
	// Expect a, b, then c1, c2 at p's old position — not a, c1, c2, b.
	require.Equal(t, []string{a.Id, b.Id, c1.Id, c2.Id}, got,
		"promoted children must take the deleted page's position in their original relative order")
}

// TestDeletePagePreservesReorderedChildBlock verifies a manual child reordering survives the
// parent's deletion: the promoted children land as a block at the deleted slot in that order.
func TestDeletePagePreservesReorderedChildBlock(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Grandparent with children a, p, d (SortOrder 1, 2, 3); d sits after the page to be
	// deleted, so it must shift to make room for p's child block.
	g, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	a, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	p, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	d, err := s.CreatePage(newPage(space.Id, channelID, userID, g.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// p's children c1, c2 created in order, then manually reordered so c2 precedes c1.
	c1, err := s.CreatePage(newPage(space.Id, channelID, userID, p.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	c2, err := s.CreatePage(newPage(space.Id, channelID, userID, p.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	pChildren, err := s.GetPageChildren(p.Id, space.Id, 0, 100)
	require.NoError(t, err)
	require.Equal(t, []string{c1.Id, c2.Id}, summaryIDs(pChildren), "sanity: creation order before reorder")
	// Move c1 after c2 by raising its SortOrder directly (reorder is not a generic-patch
	// field), leaving CreateAt untouched.
	newSortOrder := pChildren[1].SortOrder + 1
	_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Page").Set("SortOrder", newSortOrder).Where(sq.Eq{"Id": pChildren[0].Id}))
	require.NoError(t, err)
	require.Equal(t, []string{c2.Id, c1.Id}, summaryIDs(mustChildren(t, s, p.Id, space.Id)), "sanity: reordered to c2, c1")

	require.NoError(t, deletePageErr(s, p.Id, p.SpaceId, userID))

	// The reordered block (c2, c1) lands at p's old slot, and d is shifted after it.
	require.Equal(t, []string{a.Id, c2.Id, c1.Id, d.Id}, summaryIDs(mustChildren(t, s, g.Id, space.Id)),
		"reordered children must keep their order as a block at the deleted page's position")
}

// TestDeletePageDeleteSpaceNoDeadlock runs DeletePage and DeleteSpace concurrently many times.
// Both lock space-before-page, so neither should ever deadlock; the only outcomes are success
// or not-found. A lock-order regression would surface as a "deadlock detected" error.
func TestDeletePageDeleteSpaceNoDeadlock(t *testing.T) {
	s := openTestDB(t)
	userID := mmmodel.NewId()

	for i := range 25 {
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		// Load-bearing: the child makes DeletePage lock child rows — the same rows
		// DeleteSpace's cascade locks, the precise contention a regression would deadlock on.
		// Do not remove it.
		_, err = s.CreatePage(newPage(space.Id, channelID, userID, parent.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs[0] = deletePageErr(s, parent.Id, parent.SpaceId, userID) }()
		go func() { defer wg.Done(); errs[1] = s.DeleteSpace(space.Id) }()
		wg.Wait()

		for idx, e := range errs {
			if e != nil {
				require.Truef(t, store.IsErrNotFound(e), "iteration %d op %d: expected nil or not-found (no deadlock), got %v", i, idx, e)
			}
		}
	}
}

// TestUpdatePageForceConcurrentMonotonic verifies concurrent forced updates each acquire the
// SELECT ... FOR UPDATE row lock, so every write succeeds and EditAt stays strictly monotonic
// (no two forced writes land on the same EditAt).
func TestUpdatePageForceConcurrentMonotonic(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	const n = 10
	var wg sync.WaitGroup
	editAts := make([]int64, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			// Each goroutine starts from the original (now-stale) EditAt and forces the write.
			forcedTitle := "Forced"
			updated, uErr := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &forcedTitle}, created.EditAt, true, userID)
			errs[i] = uErr
			if uErr == nil {
				editAts[i] = updated.EditAt
			}
		}()
	}
	wg.Wait()

	for i, uErr := range errs {
		require.NoError(t, uErr, "forced update %d must succeed", i)
	}
	seen := make(map[int64]bool, n)
	for _, ea := range editAts {
		require.Greater(t, ea, created.EditAt, "forced EditAt must advance past the original")
		require.False(t, seen[ea], "duplicate EditAt %d — FOR UPDATE failed to serialize forced writes", ea)
		seen[ea] = true
	}
}

// TestUpdatePageForcePreservesUnpatchedFields verifies that a force save merges the patch into
// the live row under the lock, so a field the patch leaves nil keeps a concurrent edit instead
// of being clobbered by a stale snapshot. A title-only force save must not revert a body edit
// that landed after the caller's baseEditAt.
func TestUpdatePageForcePreservesUnpatchedFields(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// A concurrent writer advances the body; baseEditAt (created.EditAt) is now stale. Body and
	// SearchText must move together, so the patch carries both (this doc has no text → "").
	concurrentBody := `{"type":"doc","content":[{"type":"paragraph"}]}`
	concurrentSearch := ""
	afterBodyEdit, err := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Body: &concurrentBody, SearchText: &concurrentSearch}, created.EditAt, false, userID)
	require.NoError(t, err)
	require.Equal(t, concurrentBody, afterBodyEdit.Body)

	// A title-only force save with the stale baseEditAt must keep the concurrent body.
	forcedTitle := "Forced Title"
	forced, err := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Title: &forcedTitle}, created.EditAt, true, userID)
	require.NoError(t, err)
	require.Equal(t, forcedTitle, forced.Title)
	require.Equal(t, concurrentBody, forced.Body, "force save must not clobber the concurrent body edit")

	// Confirm it persisted, not just the returned struct.
	persisted, err := s.GetPage(created.Id, false)
	require.NoError(t, err)
	require.Equal(t, forcedTitle, persisted.Title)
	require.Equal(t, concurrentBody, persisted.Body)
}

// TestUpdatePageRejectsBodyWithoutSearchText verifies the Body/SearchText coupling is enforced
// at the store boundary, not only in the service: a body-only patch would strand a stale
// SearchText in the search index, so the store must reject it.
func TestUpdatePageRejectsBodyWithoutSearchText(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	newBody := `{"type":"doc","content":[{"type":"paragraph"}]}`
	_, err = s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{Body: &newBody}, created.EditAt, false, userID)
	require.Error(t, err)
	require.True(t, store.IsErrInvalidInput(err), "store must reject a body-only patch, got %v", err)
}

// TestUpdatePageRejectsNilAndEmptyPatch verifies the store rejects a nil patch (without
// panicking) and an all-nil no-op patch, instead of locking the row and bumping
// UpdateAt/EditAt/LastModifiedBy with no content change.
func TestUpdatePageRejectsNilAndEmptyPatch(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	_, nilErr := s.UpdatePage(created.Id, created.SpaceId, nil, created.EditAt, false, userID)
	require.True(t, store.IsErrInvalidInput(nilErr), "store must reject a nil patch, got %v", nilErr)

	_, emptyErr := s.UpdatePage(created.Id, created.SpaceId, &model.PagePatch{}, created.EditAt, false, userID)
	require.True(t, store.IsErrInvalidInput(emptyErr), "store must reject an all-nil patch, got %v", emptyErr)

	// The row must be untouched: EditAt unchanged proves no no-op write happened.
	after, err := s.GetPage(created.Id, false)
	require.NoError(t, err)
	require.Equal(t, created.EditAt, after.EditAt, "a rejected patch must not bump EditAt")
}

// TestCreatePageSubtreeMissingParent verifies CreatePageSubtree rejects a root whose ParentId does
// not resolve to a live page in the given space, rather than inserting an orphaned subtree.
func TestCreatePageSubtreeMissingParent(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	root := newPage(space.Id, channelID, userID, mmmodel.NewId())
	root.Id = mmmodel.NewId()
	_, err = s.CreatePageSubtree([]*model.Page{root}, 0)
	require.True(t, store.IsErrInvalidInput(err), "expected ErrInvalidInput for a missing parent, got %v", err)
}

// TestCreatePageSubtreeMissingSpace verifies CreatePageSubtree rejects a root targeting a
// nonexistent (or soft-deleted) space.
func TestCreatePageSubtreeMissingSpace(t *testing.T) {
	s := openTestDB(t)

	userID := mmmodel.NewId()
	root := newPage(mmmodel.NewId(), mmmodel.NewId(), userID, "")
	root.Id = mmmodel.NewId()
	_, err := s.CreatePageSubtree([]*model.Page{root}, 0)
	require.True(t, store.IsErrNotFound(err), "expected ErrNotFound for a missing space, got %v", err)
}

// TestGetSpacePages verifies GetSpacePages returns live pages for a space and excludes pages from
// other spaces and soft-deleted pages.
func TestGetSpacePages(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	otherSpace, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	// Create two pages in the target space.
	p1, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	p2, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// A page in a different space must not appear.
	_, err = s.CreatePage(newPage(otherSpace.Id, mmmodel.NewId(), userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// A soft-deleted page in the target space must not appear.
	deleted, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	require.NoError(t, deletePageErr(s, deleted.Id, deleted.SpaceId, userID))

	pages, err := s.GetSpacePages(space.Id, 0, 100)
	require.NoError(t, err)
	require.Len(t, pages, 2, "must return exactly the two live pages in the space")

	ids := summaryIDs(pages)
	require.Contains(t, ids, p1.Id)
	require.Contains(t, ids, p2.Id)
	require.NotContains(t, ids, deleted.Id)
}

// TestCreatePageSubtreeSuccess verifies that CreatePageSubtree inserts a root plus children and
// returns all created rows with their assigned IDs.
func TestCreatePageSubtreeSuccess(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	// Build a two-level subtree: root → child → grandchild.
	root := newPage(space.Id, channelID, userID, "")
	root.PreSave()
	child := newPage(space.Id, channelID, userID, root.Id)
	child.PreSave()
	grandchild := newPage(space.Id, channelID, userID, child.Id)
	grandchild.PreSave()

	created, err := s.CreatePageSubtree([]*model.Page{root, child, grandchild}, testDefaultMaxDepth)
	require.NoError(t, err)
	require.Len(t, created, 3, "must return all three created pages")

	ids := idsOf(created)
	require.Contains(t, ids, root.Id)
	require.Contains(t, ids, child.Id)
	require.Contains(t, ids, grandchild.Id)

	// Verify they are live in the DB.
	for _, id := range ids {
		got, getErr := s.GetPage(id, false)
		require.NoError(t, getErr, "page %s must be fetchable after subtree create", id)
		require.Zero(t, got.DeleteAt)
	}
}

// TestSpaceDeleteRestoreKeepsPageTimestampsMonotonic verifies the delete/restore cascades never
// regress a page's UpdateAt/EditAt CAS tokens, even when a prior structural operation advanced
// them past wall clock — a regressed token would make a stale client baseline read as current.
func TestSpaceDeleteRestoreKeepsPageTimestampsMonotonic(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// Simulate prior structural operations having pushed the CAS tokens past wall clock.
	future := mmmodel.GetMillis() + 60_000
	_, err = s.RawExecForTest("UPDATE DOCS_Page SET UpdateAt = $1, EditAt = $1 WHERE Id = $2", future, page.Id)
	require.NoError(t, err)

	require.NoError(t, s.DeleteSpace(space.Id))
	deleted, err := s.GetPage(page.Id, true)
	require.NoError(t, err)
	require.Greater(t, deleted.UpdateAt, future, "delete cascade must advance UpdateAt, never regress it")
	require.Greater(t, deleted.EditAt, future, "delete cascade must advance EditAt, never regress it")

	require.NoError(t, s.RestoreSpace(space.Id))
	restored, err := s.GetPage(page.Id, false)
	require.NoError(t, err)
	require.Greater(t, restored.UpdateAt, deleted.UpdateAt, "restore cascade must advance UpdateAt, never regress it")
	require.Greater(t, restored.EditAt, deleted.EditAt, "restore cascade must advance EditAt, never regress it")
}

// TestWithSpaceMembershipLockSerializes verifies lock-holders for the same space are mutually
// exclusive: no two callbacks are ever inside the lock at once, and callbacks run to completion
// even when their work spans multiple scheduling points.
func TestWithSpaceMembershipLockSerializes(t *testing.T) {
	s := openTestDB(t)
	spaceID := mmmodel.NewId()

	const n = 20
	var active atomic.Int32
	var overlaps atomic.Int32
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			errs[i] = s.WithSpaceMembershipLock(spaceID, func() error {
				if active.Add(1) > 1 {
					overlaps.Add(1)
				}
				// Widen the hold window across a scheduling point, mimicking the multi-call
				// guard the lock exists to protect.
				runtime.Gosched()
				active.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()

	for i, lErr := range errs {
		require.NoError(t, lErr, "lock call %d", i)
	}
	require.Zero(t, overlaps.Load(), "concurrent holders observed — advisory lock failed to serialize")
}

// TestWithSpaceMembershipLockAcquireTimeout verifies a waiter gives up with a retryable
// ErrConflict once the acquisition timeout elapses, instead of blocking indefinitely on a
// pooled connection while another holder is inside the lock.
func TestWithSpaceMembershipLockAcquireTimeout(t *testing.T) {
	s := openTestDB(t)
	spaceID := mmmodel.NewId()

	holderIn := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- s.WithSpaceMembershipLock(spaceID, func() error {
			close(holderIn)
			<-releaseHolder
			return nil
		})
	}()
	<-holderIn

	err := s.WithSpaceMembershipLockTimeoutForTest(spaceID, 300*time.Millisecond, func() error {
		t.Error("callback must not run when the lock was never acquired")
		return nil
	})
	require.Error(t, err)
	require.True(t, store.IsErrConflict(err), "lock acquisition timeout must surface as a retryable conflict; got %v", err)

	close(releaseHolder)
	require.NoError(t, <-holderDone)

	// With the holder gone, the same call must acquire immediately and run the callback.
	ran := false
	require.NoError(t, s.WithSpaceMembershipLockTimeoutForTest(spaceID, 300*time.Millisecond, func() error {
		ran = true
		return nil
	}))
	require.True(t, ran)
}
