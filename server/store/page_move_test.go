// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// seedSpaceAndPage creates a space and one root page on it, returning the page and channel id.
func seedSpaceAndPage(t *testing.T, s *store.Store) (channelID string, page *model.Page) {
	t.Helper()
	channelID = mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	p, err := s.CreatePage(newPage(space.Id, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
	require.NoError(t, err)
	return channelID, p
}

// TestMovePage_StoreEdgeCases covers store.MovePage's error and edge branches. Error semantics are
// foundation-consistent: a stale optimistic-lock baseline is a Conflict (matching UpdatePage's
// EditAt CAS), not a not-found; a missing or locked parent is an invalid-input.
func TestMovePage_StoreEdgeCases(t *testing.T) {
	t.Run("stale expectedUpdateAt returns conflict", func(t *testing.T) {
		s := openTestDB(t)
		channelID, page := seedSpaceAndPage(t, s)
		parent, err := s.CreatePage(newPage(page.SpaceId, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
		require.NoError(t, err)

		_, _, err = s.MovePage(page.Id, page.SpaceId, &parent.Id, nil, page.UpdateAt-1, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrConflict(err), "expected ErrConflict, got %T: %v", err, err)
	})

	t.Run("self as parent returns circular-reference", func(t *testing.T) {
		s := openTestDB(t)
		_, page := seedSpaceAndPage(t, s)

		self := page.Id
		_, _, err := s.MovePage(page.Id, page.SpaceId, &self, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrCircularReference(err), "expected ErrCircularReference, got %T: %v", err, err)
	})

	t.Run("non-existent parent returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, page := seedSpaceAndPage(t, s)

		ghost := mmmodel.NewId()
		_, _, err := s.MovePage(page.Id, page.SpaceId, &ghost, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("empty pageID returns invalid-input on Id", func(t *testing.T) {
		s := openTestDB(t)
		_, _, err := s.MovePage("", mmmodel.NewId(), nil, nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
		require.Equal(t, "Id", inv.Field)
	})

	t.Run("missing page returns not-found", func(t *testing.T) {
		s := openTestDB(t)
		_, _, err := s.MovePage(mmmodel.NewId(), mmmodel.NewId(), nil, nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err), "expected ErrNotFound, got %T: %v", err, err)
	})

	t.Run("move to root sets empty parent id", func(t *testing.T) {
		s := openTestDB(t)
		channelID, parent := seedSpaceAndPage(t, s)
		child, err := s.CreatePage(newPage(parent.SpaceId, channelID, mmmodel.NewId(), parent.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		root := ""
		moved, _, err := s.MovePage(child.Id, child.SpaceId, &root, nil, child.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)
		require.Equal(t, "", moved.ParentId)
	})

	t.Run("reparent appends under the destination", func(t *testing.T) {
		s := openTestDB(t)
		channelID, page := seedSpaceAndPage(t, s)
		parent, err := s.CreatePage(newPage(page.SpaceId, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
		require.NoError(t, err)

		moved, _, err := s.MovePage(page.Id, page.SpaceId, &parent.Id, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)
		require.Equal(t, parent.Id, moved.ParentId)
		require.Contains(t, idsOf(mustChildren(t, s, parent.Id, parent.SpaceId)), page.Id)
	})
}

// TestMovePageToSpace_Store covers store.MovePageToSpace: the whole subtree's SpaceId/ChannelId is
// rewritten and the moved root lands at the target root, plus the input-validation branches.
func TestMovePageToSpace_Store(t *testing.T) {
	t.Run("re-homes the subtree to the target space", func(t *testing.T) {
		s := openTestDB(t)
		chA := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(chA))
		require.NoError(t, err)
		root, err := s.CreatePage(newPage(spaceA.Id, chA, mmmodel.NewId(), ""), testDefaultMaxDepth)
		require.NoError(t, err)
		child, err := s.CreatePage(newPage(spaceA.Id, chA, mmmodel.NewId(), root.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		movedRoot, err := s.MovePageToSpace(root.Id, spaceA.Id, spaceB.Id, nil, root.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, movedRoot.SpaceId, "returned page reflects the committed move")
		require.Equal(t, chB, movedRoot.ChannelId)
		require.Equal(t, "", movedRoot.ParentId, "moved root lands at the target root")

		gotRoot, err := s.GetPage(root.Id, false)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, gotRoot.SpaceId)
		require.Equal(t, chB, gotRoot.ChannelId)
		require.Equal(t, "", gotRoot.ParentId, "moved root lands at the target root")

		gotChild, err := s.GetPage(child.Id, false)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, gotChild.SpaceId, "child follows the subtree")
		require.Equal(t, chB, gotChild.ChannelId)
		require.Equal(t, root.Id, gotChild.ParentId, "child stays under the moved root")
	})

	t.Run("empty pageID returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.MovePageToSpace("", mmmodel.NewId(), mmmodel.NewId(), nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("empty sourceSpaceID returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.MovePageToSpace(mmmodel.NewId(), "", mmmodel.NewId(), nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("empty targetSpaceID returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.MovePageToSpace(mmmodel.NewId(), mmmodel.NewId(), "", nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("missing page returns not-found", func(t *testing.T) {
		s := openTestDB(t)
		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		_, err = s.MovePageToSpace(mmmodel.NewId(), mmmodel.NewId(), spaceB.Id, nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err), "expected ErrNotFound, got %T: %v", err, err)
	})
}

// TestMovePage_StoreUnderLockGuards exercises the cycle and depth re-checks that run inside the
// store transaction under the row lock. The app layer pre-checks the same rules on an unlocked
// read, so these call store.MovePage directly to prove the store's own guards reject independently.
func TestMovePage_StoreUnderLockGuards(t *testing.T) {
	t.Run("moving under own descendant returns circular-reference invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		channelID, root := seedSpaceAndPage(t, s)
		child, err := s.CreatePage(newPage(root.SpaceId, channelID, mmmodel.NewId(), root.Id), testDefaultMaxDepth)
		require.NoError(t, err)
		grandchild, err := s.CreatePage(newPage(root.SpaceId, channelID, mmmodel.NewId(), child.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		dest := grandchild.Id
		_, _, err = s.MovePage(root.Id, root.SpaceId, &dest, nil, root.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrCircularReference(err), "expected ErrCircularReference, got %T: %v", err, err)
	})

	t.Run("depth cap re-checked under lock returns limit-exceeded", func(t *testing.T) {
		s := openTestDB(t)
		channelID, a := seedSpaceAndPage(t, s)
		b, err := s.CreatePage(newPage(a.SpaceId, channelID, mmmodel.NewId(), a.Id), testDefaultMaxDepth)
		require.NoError(t, err)
		c, err := s.CreatePage(newPage(a.SpaceId, channelID, mmmodel.NewId(), b.Id), testDefaultMaxDepth)
		require.NoError(t, err)
		// c is at depth 3 (a=1, b=2, c=3). A separate root page moved under c would land at
		// depth 4, breaching a maxDepth of 3.
		p, err := s.CreatePage(newPage(a.SpaceId, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
		require.NoError(t, err)

		dest := c.Id
		_, _, err = s.MovePage(p.Id, p.SpaceId, &dest, nil, p.UpdateAt, false, 3)
		require.Error(t, err)
		require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %T: %v", err, err)
	})
}

// TestCreatePageSubtree_StoreUnderLockDepthGuard verifies CreatePageSubtree's own depth
// re-check (mirroring MovePage's under-lock guard above) fires independently of any app-layer
// pre-check: called directly against the store with a destination parent already at the depth
// limit, it must reject rather than insert.
func TestCreatePageSubtree_StoreUnderLockDepthGuard(t *testing.T) {
	s := openTestDB(t)
	channelID, a := seedSpaceAndPage(t, s)
	b, err := s.CreatePage(newPage(a.SpaceId, channelID, mmmodel.NewId(), a.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	c, err := s.CreatePage(newPage(a.SpaceId, channelID, mmmodel.NewId(), b.Id), testDefaultMaxDepth)
	require.NoError(t, err)
	// c is at depth 3 (a=1, b=2, c=3). A subtree root inserted under c would land at depth 4,
	// breaching a maxDepth of 3.
	root := newPage(a.SpaceId, channelID, mmmodel.NewId(), c.Id)
	root.Id = mmmodel.NewId()

	_, err = s.CreatePageSubtree([]*model.Page{root}, 3)
	require.Error(t, err)
	require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %T: %v", err, err)
}

// TODO: store.MovePage's page.SpaceId != spaceID branch (page_store.go) guards against a
// concurrent MovePageToSpace relocating the page between the unlocked space read and the locked
// page read. Triggering it deterministically requires a goroutine racing a real transaction
// against this call, which the existing single-threaded test helpers (openTestDB, sequential
// store calls) cannot express without flakiness. Left as a known coverage gap rather than adding
// a timing-dependent test.

// TestMovePage_StoreReindexClampSingleSibling positions a page within a one-element sibling group
// with an index past the end; the reindex must clamp it down to the group's only slot and keep the
// page as its sole, correctly-ordered child.
func TestMovePage_StoreReindexClampSingleSibling(t *testing.T) {
	s := openTestDB(t)
	channelID, parent := seedSpaceAndPage(t, s)
	child, err := s.CreatePage(newPage(parent.SpaceId, channelID, mmmodel.NewId(), parent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	sameParent := parent.Id
	large := int64(5)
	moved, _, err := s.MovePage(child.Id, child.SpaceId, &sameParent, &large, child.UpdateAt, false, store.MaxPageHierarchyDepth)
	require.NoError(t, err)
	require.Equal(t, parent.Id, moved.ParentId)
	require.Equal(t, []string{child.Id}, idsOf(mustChildren(t, s, parent.Id, parent.SpaceId)))
}

// TestCreatePage_SiblingCapEnforced verifies CreatePage rejects once a parent's live child count
// reaches MaxPageSiblingsLimit. The group is bulk-seeded with raw SQL rather than one CreatePage
// call per row, since running MaxPageSiblingsLimit inserts through the full store API would make
// the test itself as slow as the unbounded-group growth this cap prevents.
func TestCreatePage_SiblingCapEnforced(t *testing.T) {
	s := openTestDB(t)
	channelID, parent := seedSpaceAndPage(t, s)

	_, rawErr := s.RawExecForTest(
		`INSERT INTO DOCS_Page (Id, SpaceId, ChannelId, ParentId, Type, UserId, SortOrder, CreateAt, UpdateAt)
		 SELECT 'sib' || lpad(gs::text, 23, '0'), $1, $2, $3, 'page', $4, gs, gs, gs
		 FROM generate_series(1, $5) AS gs`,
		parent.SpaceId, channelID, parent.Id, mmmodel.NewId(), store.MaxPageSiblingsLimit)
	require.NoError(t, rawErr)

	_, err := s.CreatePage(newPage(parent.SpaceId, channelID, mmmodel.NewId(), parent.Id), testDefaultMaxDepth)
	require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %v", err)
}

// TestPageMutations_ScopedToSpace verifies the {Id, SpaceId} scoping the store mutations enforce: a
// mutation addressed with the wrong (but live) space id finds no row and reads as not-found, leaving
// the page untouched. This closes the TOCTOU where a page relocated by a concurrent move-to-space
// could still be mutated through its stale {space_id, page_id} URL.
func TestPageMutations_ScopedToSpace(t *testing.T) {
	s := openTestDB(t)
	channelA, page := seedSpaceAndPage(t, s)

	// A second live space whose id stands in for the "wrong" space in each call.
	spaceB, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	user := mmmodel.NewId()

	t.Run("update with wrong space is not found and leaves the page unchanged", func(t *testing.T) {
		newTitle := "Should Not Apply"
		_, uErr := s.UpdatePage(page.Id, spaceB.Id, &model.PagePatch{Title: &newTitle}, page.EditAt, false, user)
		require.True(t, store.IsErrNotFound(uErr))

		fresh, gErr := s.GetPage(page.Id, false)
		require.NoError(t, gErr)
		require.Equal(t, page.Title, fresh.Title)
	})

	t.Run("move with wrong space is not found", func(t *testing.T) {
		root := ""
		_, _, mErr := s.MovePage(page.Id, spaceB.Id, &root, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.True(t, store.IsErrNotFound(mErr))
	})

	t.Run("move-to-space with wrong source space is not found", func(t *testing.T) {
		_, mErr := s.MovePageToSpace(page.Id, spaceB.Id, page.SpaceId, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.True(t, store.IsErrNotFound(mErr))
	})

	t.Run("delete with wrong space is not found and leaves the page live", func(t *testing.T) {
		require.True(t, store.IsErrNotFound(s.DeletePage(page.Id, spaceB.Id, user)))

		fresh, gErr := s.GetPage(page.Id, false)
		require.NoError(t, gErr)
		require.Zero(t, fresh.DeleteAt)
	})

	t.Run("restore with wrong space is not found", func(t *testing.T) {
		require.NoError(t, s.DeletePage(page.Id, page.SpaceId, user))
		require.True(t, store.IsErrNotFound(s.RestorePage(page.Id, spaceB.Id, user, testDefaultMaxDepth)))

		// A correctly-scoped restore then succeeds, confirming the row was only shielded by the scope.
		require.NoError(t, s.RestorePage(page.Id, page.SpaceId, user, testDefaultMaxDepth))
	})

	t.Run("get for duplicate with wrong source space is not found", func(t *testing.T) {
		_, _, gErr := s.GetPageForDuplicate(page.Id, spaceB.Id, false)
		require.True(t, store.IsErrNotFound(gErr))
	})

	t.Run("get for duplicate with correct source space returns the page and descendants", func(t *testing.T) {
		child, cErr := s.CreatePage(&model.Page{SpaceId: page.SpaceId, ChannelId: channelA, UserId: user, ParentId: page.Id, Type: model.PageTypePage, Title: "Child"}, testDefaultMaxDepth)
		require.NoError(t, cErr)

		got, descendants, gErr := s.GetPageForDuplicate(page.Id, page.SpaceId, true)
		require.NoError(t, gErr)
		require.Equal(t, page.Id, got.Id)
		require.Len(t, descendants, 1)
		require.Equal(t, child.Id, descendants[0].Id)
	})
}
