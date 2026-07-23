// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"errors"
	"sync"
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

		_, _, _, err = s.MovePage(page.Id, page.SpaceId, &parent.Id, nil, page.UpdateAt-1, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrConflict(err), "expected ErrConflict, got %T: %v", err, err)
	})

	t.Run("self as parent returns circular-reference", func(t *testing.T) {
		s := openTestDB(t)
		_, page := seedSpaceAndPage(t, s)

		self := page.Id
		_, _, _, err := s.MovePage(page.Id, page.SpaceId, &self, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrCircularReference(err), "expected ErrCircularReference, got %T: %v", err, err)
	})

	t.Run("non-existent parent returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, page := seedSpaceAndPage(t, s)

		ghost := mmmodel.NewId()
		_, _, _, err := s.MovePage(page.Id, page.SpaceId, &ghost, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("empty pageID returns invalid-input on Id", func(t *testing.T) {
		s := openTestDB(t)
		_, _, _, err := s.MovePage("", mmmodel.NewId(), nil, nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
		require.Equal(t, "Id", inv.Field)
	})

	t.Run("missing page returns not-found", func(t *testing.T) {
		s := openTestDB(t)
		_, _, _, err := s.MovePage(mmmodel.NewId(), mmmodel.NewId(), nil, nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err), "expected ErrNotFound, got %T: %v", err, err)
	})

	t.Run("move to root sets empty parent id", func(t *testing.T) {
		s := openTestDB(t)
		channelID, parent := seedSpaceAndPage(t, s)
		child, err := s.CreatePage(newPage(parent.SpaceId, channelID, mmmodel.NewId(), parent.Id), testDefaultMaxDepth)
		require.NoError(t, err)

		root := ""
		moved, _, _, err := s.MovePage(child.Id, child.SpaceId, &root, nil, child.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)
		require.Equal(t, "", moved.ParentId)
	})

	t.Run("reparent appends under the destination", func(t *testing.T) {
		s := openTestDB(t)
		channelID, page := seedSpaceAndPage(t, s)
		parent, err := s.CreatePage(newPage(page.SpaceId, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
		require.NoError(t, err)

		moved, _, _, err := s.MovePage(page.Id, page.SpaceId, &parent.Id, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)
		require.Equal(t, parent.Id, moved.ParentId)
		require.Contains(t, summaryIDs(mustChildren(t, s, parent.Id, parent.SpaceId)), page.Id)
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

		movedRoot, _, err := s.MovePageToSpace(root.Id, spaceA.Id, spaceB.Id, mmmodel.NewId(), nil, root.UpdateAt, false, store.MaxPageHierarchyDepth)
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

	// Draft reads require the draft's SpaceId to match its live page's SpaceId (applyDraftLivenessFilter),
	// so a draft left behind in the source space becomes unreadable once its page moves. rewriteSubtreeSpace
	// re-homes both the in-progress edit draft (matched by PageId) and a pending new-page draft parented
	// within the subtree (matched by ParentId) onto the target space.
	t.Run("re-homes the subtree's drafts to the target space", func(t *testing.T) {
		s := openTestDB(t)
		chA := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(chA))
		require.NoError(t, err)
		user := mmmodel.NewId()
		page, err := s.CreatePage(newPage(spaceA.Id, chA, user, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		// An in-progress edit draft on the page, and a pending new-page draft parented under it
		// (its own PageId has no page row yet).
		dEdit := newDraft(user, spaceA.Id, page.Id, "")
		dEdit.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(dEdit, nil, nil, nil)
		require.NoError(t, err)
		parentPageID := page.Id
		_, _, err = s.UpsertDraft(newDraft(user, spaceA.Id, mmmodel.NewId(), parentPageID), &parentPageID, nil, nil)
		require.NoError(t, err)

		sourceBefore, err := s.GetDraftsForSpace(user, spaceA.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, sourceBefore, 2, "both drafts are readable in the source space before the move")

		_, _, err = s.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, user, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)

		movedDraft, err := s.GetDraft(user, page.Id)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, movedDraft.SpaceId, "the edit draft follows the page and stays readable")

		targetDrafts, err := s.GetDraftsForSpace(user, spaceB.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, targetDrafts, 2, "both drafts now live in the target space")

		sourceAfter, err := s.GetDraftsForSpace(user, spaceA.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, sourceAfter, "no draft remains stranded in the source space")
	})

	// A cross-space move re-homes every owner's draft for the moved pages, not just the mover's:
	// a draft is unpublished work its owner never consented to lose, so the move preserves it and
	// lets the space-membership read gate hide it from an owner who cannot reach the target space.
	t.Run("re-homes another user's draft instead of deleting it", func(t *testing.T) {
		s := openTestDB(t)
		chA := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(chA))
		require.NoError(t, err)
		mover := mmmodel.NewId()
		other := mmmodel.NewId()
		page, err := s.CreatePage(newPage(spaceA.Id, chA, mover, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		// A second user holds an in-progress edit draft on the same page.
		otherDraft := newDraft(other, spaceA.Id, page.Id, "")
		otherDraft.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(otherDraft, nil, nil, nil)
		require.NoError(t, err)

		_, _, err = s.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, mover, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)

		moved, err := s.GetDraft(other, page.Id)
		require.NoError(t, err, "the other user's draft survives the move")
		require.Equal(t, spaceB.Id, moved.SpaceId, "and is re-homed to the target space, not deleted")

		targetDrafts, err := s.GetDraftsForSpace(other, spaceB.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, targetDrafts, 1, "the draft is readable in the target space")

		sourceDrafts, err := s.GetDraftsForSpace(other, spaceA.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, sourceDrafts, "nothing is stranded in the source space")
	})

	// The mover's re-homed drafts are quota-checked against the target space; other users' are not.
	t.Run("rejects the move when re-homing exceeds the mover's target-space quota", func(t *testing.T) {
		s := openTestDB(t)
		chA := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(chA))
		require.NoError(t, err)
		mover := mmmodel.NewId()
		page, err := s.CreatePage(newPage(spaceA.Id, chA, mover, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		// One mover draft on the page to be moved.
		editDraft := newDraft(mover, spaceA.Id, page.Id, "")
		editDraft.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(editDraft, nil, nil, nil)
		require.NoError(t, err)

		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)
		// Fill the mover's quota in the target space, so re-homing even one more trips the cap.
		for range store.MaxDraftsPerUserPerSpace {
			_, _, err = s.UpsertDraft(newDraft(mover, spaceB.Id, mmmodel.NewId(), ""), nil, nil, nil)
			require.NoError(t, err)
		}

		_, _, err = s.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, mover, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %T: %v", err, err)

		// The move rolled back: the page stays in the source space.
		stillA, err := s.GetPage(page.Id, false)
		require.NoError(t, err)
		require.Equal(t, spaceA.Id, stillA.SpaceId, "a rejected move leaves the page in the source space")
	})

	// A cross-space move must not leak presence: rewriteSubtreeSpace resets a re-homed draft's
	// LastActiveAt to 0, so an owner who was an active editor in the source space is not reported
	// as one in the target space until they edit the draft there again.
	t.Run("re-homed draft's presence does not leak into the target space", func(t *testing.T) {
		s := openTestDB(t)
		chA := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(chA))
		require.NoError(t, err)
		user := mmmodel.NewId()
		page, err := s.CreatePage(newPage(spaceA.Id, chA, user, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		editDraft := newDraft(user, spaceA.Id, page.Id, "")
		editDraft.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(editDraft, nil, nil, nil)
		require.NoError(t, err)

		windowStart := mmmodel.GetMillis() - 60*1000
		editorsBefore, err := s.GetPageActiveEditors(page.Id, spaceA.Id, windowStart)
		require.NoError(t, err)
		require.Equal(t, []string{user}, editorsBefore, "the user is an active editor in the source space before the move")

		_, _, err = s.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, user, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.NoError(t, err)

		movedDraft, err := s.GetDraft(user, page.Id)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, movedDraft.SpaceId, "the draft is re-homed to the target space")

		editorsAfter, err := s.GetPageActiveEditors(page.Id, spaceB.Id, windowStart)
		require.NoError(t, err)
		require.Empty(t, editorsAfter, "presence must not leak across the move: LastActiveAt was reset")
	})

	t.Run("empty pageID returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, _, err := s.MovePageToSpace("", mmmodel.NewId(), mmmodel.NewId(), mmmodel.NewId(), nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("empty sourceSpaceID returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, _, err := s.MovePageToSpace(mmmodel.NewId(), "", mmmodel.NewId(), mmmodel.NewId(), nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("empty targetSpaceID returns invalid-input", func(t *testing.T) {
		s := openTestDB(t)
		_, _, err := s.MovePageToSpace(mmmodel.NewId(), mmmodel.NewId(), "", mmmodel.NewId(), nil, 0, false, store.MaxPageHierarchyDepth)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
	})

	t.Run("missing page returns not-found", func(t *testing.T) {
		s := openTestDB(t)
		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		_, _, err = s.MovePageToSpace(mmmodel.NewId(), mmmodel.NewId(), spaceB.Id, mmmodel.NewId(), nil, 0, false, store.MaxPageHierarchyDepth)
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
		_, _, _, err = s.MovePage(root.Id, root.SpaceId, &dest, nil, root.UpdateAt, false, store.MaxPageHierarchyDepth)
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
		_, _, _, err = s.MovePage(p.Id, p.SpaceId, &dest, nil, p.UpdateAt, false, 3)
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
	moved, _, _, err := s.MovePage(child.Id, child.SpaceId, &sameParent, &large, child.UpdateAt, false, store.MaxPageHierarchyDepth)
	require.NoError(t, err)
	require.Equal(t, parent.Id, moved.ParentId)
	require.Equal(t, []string{child.Id}, summaryIDs(mustChildren(t, s, parent.Id, parent.SpaceId)))
}

// TestMovePage_ReturnsLockedPriorParent verifies the prior-parent return reflects the row read
// under lock, not the caller's last read: after another move reparents the page, a forced move
// carrying the original (stale) baseline must report the current parent as the one it displaced,
// since that is the subtree clients actually need to invalidate.
func TestMovePage_ReturnsLockedPriorParent(t *testing.T) {
	s := openTestDB(t)
	channelID, page := seedSpaceAndPage(t, s)
	parentA, err := s.CreatePage(newPage(page.SpaceId, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
	require.NoError(t, err)
	parentB, err := s.CreatePage(newPage(page.SpaceId, channelID, mmmodel.NewId(), ""), testDefaultMaxDepth)
	require.NoError(t, err)

	staleBaseline := page.UpdateAt
	_, priorParent, didMove, err := s.MovePage(page.Id, page.SpaceId, &parentA.Id, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
	require.NoError(t, err)
	require.True(t, didMove)
	require.Empty(t, priorParent, "the first move displaces the root parent")

	// Force past the stale baseline; the displaced parent is A (the locked row's parent), not
	// the root the stale caller last saw.
	forced, priorParent, didMove, err := s.MovePage(page.Id, page.SpaceId, &parentB.Id, nil, staleBaseline, true, store.MaxPageHierarchyDepth)
	require.NoError(t, err)
	require.True(t, didMove)
	require.Equal(t, parentA.Id, priorParent)
	require.Equal(t, parentB.Id, forced.ParentId)
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

// TestCreatePageSubtreeDuplicateIDConflict verifies the bulk-insert path maps a primary-key
// collision to ErrConflict, matching the single-row CreatePage path. This is the conflict surface
// reachable through DuplicatePage(includeChildren=true).
func TestCreatePageSubtreeDuplicateIDConflict(t *testing.T) {
	s := openTestDB(t)
	channelID, existing := seedSpaceAndPage(t, s)

	// A subtree root whose id collides with an already-live page must fail the insert as a conflict.
	root := newPage(existing.SpaceId, channelID, mmmodel.NewId(), "")
	root.Id = existing.Id
	_, err := s.CreatePageSubtree([]*model.Page{root}, testDefaultMaxDepth)
	require.True(t, store.IsErrConflict(err), "expected ErrConflict for a duplicate id, got %T: %v", err, err)
}

// TestCreatePageSubtreeSiblingCapEnforced verifies the bulk-insert path enforces
// MaxPageSiblingsLimit on the destination sibling group the root joins (via nextSortOrder),
// matching CreatePage. This is the cap surface reachable through DuplicatePage(includeChildren=true)
// into an already-full group. The group is bulk-seeded with raw SQL for the same reason as
// TestCreatePage_SiblingCapEnforced.
func TestCreatePageSubtreeSiblingCapEnforced(t *testing.T) {
	s := openTestDB(t)
	channelID, parent := seedSpaceAndPage(t, s)

	_, rawErr := s.RawExecForTest(
		`INSERT INTO DOCS_Page (Id, SpaceId, ChannelId, ParentId, Type, UserId, SortOrder, CreateAt, UpdateAt)
		 SELECT 'sib' || lpad(gs::text, 23, '0'), $1, $2, $3, 'page', $4, gs, gs, gs
		 FROM generate_series(1, $5) AS gs`,
		parent.SpaceId, channelID, parent.Id, mmmodel.NewId(), store.MaxPageSiblingsLimit)
	require.NoError(t, rawErr)

	root := newPage(parent.SpaceId, channelID, mmmodel.NewId(), parent.Id)
	root.Id = mmmodel.NewId()
	_, err := s.CreatePageSubtree([]*model.Page{root}, testDefaultMaxDepth)
	require.True(t, store.IsErrLimitExceeded(err), "expected ErrLimitExceeded, got %T: %v", err, err)
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
		_, _, _, mErr := s.MovePage(page.Id, spaceB.Id, &root, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.True(t, store.IsErrNotFound(mErr))
	})

	t.Run("move-to-space with wrong source space is not found", func(t *testing.T) {
		_, _, mErr := s.MovePageToSpace(page.Id, spaceB.Id, page.SpaceId, user, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		require.True(t, store.IsErrNotFound(mErr))
	})

	t.Run("delete with wrong space is not found and leaves the page live", func(t *testing.T) {
		require.True(t, store.IsErrNotFound(deletePageErr(s, page.Id, spaceB.Id, user)))

		fresh, gErr := s.GetPage(page.Id, false)
		require.NoError(t, gErr)
		require.Zero(t, fresh.DeleteAt)
	})

	t.Run("restore with wrong space is not found", func(t *testing.T) {
		require.NoError(t, deletePageErr(s, page.Id, page.SpaceId, user))
		require.True(t, store.IsErrNotFound(restorePageErr(s, page.Id, spaceB.Id, user, testDefaultMaxDepth)))

		// A correctly-scoped restore then succeeds, confirming the row was only shielded by the scope.
		require.NoError(t, restorePageErr(s, page.Id, page.SpaceId, user, testDefaultMaxDepth))
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

// TestMovePageToSpace_ConcurrentAutosaveInvariants exercises the space-row FOR UPDATE
// serialization that guards a cross-space move against a simultaneous autosave on a draft of the
// moving page. MovePageToSpace and UpsertDraft both take lockLiveSpace on the source space, so the
// two transactions serialize on the same row. The
// test does not assert which one wins — it asserts that whichever ordering the lock produces, the
// committed state is one of the legal outcomes: the page reaches the target space, and its draft is
// re-homed there exactly once, never duplicated across spaces, orphaned in the source, or left
// pointing at the wrong space. Both orderings satisfy these invariants, so the test never flakes; a
// broken lock (a lost move, a torn or duplicated draft) would fail an invariant on some interleaving.
func TestMovePageToSpace_ConcurrentAutosaveInvariants(t *testing.T) {
	s := openTestDB(t)

	// Repeat so scheduling exercises both commit orderings (move-first and autosave-first) across runs.
	const iterations = 12
	for i := range iterations {
		chA := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(chA))
		require.NoError(t, err)
		chB := mmmodel.NewId()
		spaceB, err := s.CreateSpace(newSpace(chB))
		require.NoError(t, err)

		user := mmmodel.NewId()
		page, err := s.CreatePage(newPage(spaceA.Id, chA, user, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		// An in-progress edit draft on the page, baselined at the page's current EditAt.
		seed := newDraft(user, spaceA.Id, page.Id, "")
		seed.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(seed, nil, nil, nil)
		require.NoError(t, err)

		// Race the move against an autosave on the same draft, released together. Both calls open
		// their own transactions and contend on the source-space row. Errors are ignored here: an
		// autosave that loses to the move is rejected (its page no longer lives in the source space),
		// which is a legal outcome — the invariants below hold either way. Assertions run only on the
		// main goroutine, after both finish.
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _, _ = s.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, user, nil, page.UpdateAt, false, store.MaxPageHierarchyDepth)
		}()
		go func() {
			defer wg.Done()
			<-start
			autosave := newDraft(user, spaceA.Id, page.Id, "")
			autosave.BaseEditAt = page.EditAt
			_, _, _ = s.UpsertDraft(autosave, nil, nil, nil)
		}()
		close(start)
		wg.Wait()

		// The move always commits: the autosave never touches the page row's UpdateAt, so the move's
		// optimistic-lock CAS holds regardless of ordering.
		gotPage, err := s.GetPage(page.Id, false)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, gotPage.SpaceId, "iter %d: the move must commit the page to the target space", i)

		// The draft is re-homed to the target space exactly once — never duplicated and never orphaned
		// in the source.
		targetDrafts, err := s.GetDraftsForSpace(user, spaceB.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, targetDrafts, 1, "iter %d: the draft must be re-homed to the target exactly once", i)
		require.Equal(t, page.Id, targetDrafts[0].PageId, "iter %d: the re-homed draft is the page's draft", i)

		sourceDrafts, err := s.GetDraftsForSpace(user, spaceA.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, sourceDrafts, "iter %d: no draft may be left behind in the source space", i)

		// The re-homed draft's SpaceId matches its page's space, so it stays readable rather than orphaned.
		moved, err := s.GetDraft(user, page.Id)
		require.NoError(t, err)
		require.Equal(t, spaceB.Id, moved.SpaceId, "iter %d: the draft SpaceId must match the moved page", i)
	}
}
