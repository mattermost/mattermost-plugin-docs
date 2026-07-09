// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// buildPageChain creates a linear chain of `depth` pages rooted at the space root and returns them
// from root to deepest leaf (depth 1 → one root page). Built directly on the store so the chain can
// exceed the app-layer depth cap for depth-limit tests.
func buildPageChain(t *testing.T, s *store.Store, spaceID, channelID, userID string, depth int) []*model.Page {
	t.Helper()
	var chain []*model.Page
	var parentID string
	for range depth {
		p := mustCreatePage(t, s, spaceID, channelID, userID, parentID)
		chain = append(chain, p)
		parentID = p.Id
	}
	return chain
}

// TestServiceMovePage_Reparent moves a root page under another root page and verifies the new
// parent and that the destination's children now include the moved page.
func TestServiceMovePage_Reparent(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	moved, _, appErr := h.svc.MovePage(page.Id, page.SpaceId, &parent.Id, nil, page.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, parent.Id, moved.ParentId)

	children, appErr := h.svc.GetPageChildren(parent.Id, space.Id, 0, 100)
	require.Nil(t, appErr)
	require.Len(t, children, 1)
	require.Equal(t, page.Id, children[0].Id)
}

// TestServiceMovePage_SameParentNoOp passing the current parent is a no-op that succeeds.
func TestServiceMovePage_SameParentNoOp(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

	_, _, appErr := h.svc.MovePage(child.Id, child.SpaceId, &parent.Id, nil, child.UpdateAt, false)
	require.Nil(t, appErr)

	got, getErr := h.svc.GetPage(child.Id)
	require.Nil(t, getErr)
	require.Equal(t, parent.Id, got.ParentId)
}

// TestServiceMovePage_ToRootLevel moving with parent "" clears the parent.
func TestServiceMovePage_ToRootLevel(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

	empty := ""
	moved, _, appErr := h.svc.MovePage(child.Id, child.SpaceId, &empty, nil, child.UpdateAt, false)
	require.Nil(t, appErr)
	require.Empty(t, moved.ParentId)
}

// TestServiceMovePage_PageNotFound moving a non-existent page returns 404.
func TestServiceMovePage_PageNotFound(t *testing.T) {
	h := openTestService(t)
	_, _, appErr := h.svc.MovePage(mmmodel.NewId(), mmmodel.NewId(), nil, nil, 0, false)
	require.NotNil(t, appErr)
	require.Equal(t, 404, appErr.StatusCode)
}

// TestServiceMovePage_MalformedIDs covers the pageID/spaceID validation guards, which run before
// any store lookup.
func TestServiceMovePage_MalformedIDs(t *testing.T) {
	t.Run("malformed page id", func(t *testing.T) {
		h := openTestService(t)
		_, _, appErr := h.svc.MovePage("not-an-id", mmmodel.NewId(), nil, nil, 0, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.invalid_id.app_error", appErr.Id)
	})

	t.Run("malformed space id", func(t *testing.T) {
		h := openTestService(t)
		_, _, appErr := h.svc.MovePage(mmmodel.NewId(), "not-an-id", nil, nil, 0, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.invalid_space_id.app_error", appErr.Id)
	})
}

// TestServiceMovePage_ErrorPaths covers the destination-validation branches: a parent in a
// different space (a docs space owns exactly one channel) and the depth thresholds, which follow
// CreatePage's depth rule (a child's depth is its parent's ancestor count + 2).
func TestServiceMovePage_ErrorPaths(t *testing.T) {
	t.Run("parent in a different space", func(t *testing.T) {
		h := openTestService(t)
		userID := mmmodel.NewId()

		channelA := mmmodel.NewId()
		spaceA := mustCreateSpace(t, h.store, channelA)
		page := mustCreatePage(t, h.store, spaceA.Id, channelA, userID, "")

		channelB := mmmodel.NewId()
		spaceB := mustCreateSpace(t, h.store, channelB)
		parentInB := mustCreatePage(t, h.store, spaceB.Id, channelB, userID, "")

		newParent := parentInB.Id
		_, _, appErr := h.svc.MovePage(page.Id, page.SpaceId, &newParent, nil, page.UpdateAt, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.parent_different_space.app_error", appErr.Id)
	})

	t.Run("non-existent parent", func(t *testing.T) {
		h := openTestService(t)
		channelID := mmmodel.NewId()
		space := mustCreateSpace(t, h.store, channelID)
		page := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

		ghost := mmmodel.NewId()
		_, _, appErr := h.svc.MovePage(page.Id, page.SpaceId, &ghost, nil, page.UpdateAt, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.invalid_parent.app_error", appErr.Id)
	})

	t.Run("self as parent is a circular reference", func(t *testing.T) {
		h := openTestService(t)
		channelID := mmmodel.NewId()
		space := mustCreateSpace(t, h.store, channelID)
		page := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

		self := page.Id
		_, _, appErr := h.svc.MovePage(page.Id, page.SpaceId, &self, nil, page.UpdateAt, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.circular_reference.app_error", appErr.Id)
	})

	t.Run("moving under own descendant is a circular reference", func(t *testing.T) {
		h := openTestService(t)
		channelID := mmmodel.NewId()
		space := mustCreateSpace(t, h.store, channelID)
		userID := mmmodel.NewId()
		root := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		child := mustCreatePage(t, h.store, space.Id, channelID, userID, root.Id)
		grandchild := mustCreatePage(t, h.store, space.Id, channelID, userID, child.Id)

		newParent := grandchild.Id
		_, _, appErr := h.svc.MovePage(root.Id, root.SpaceId, &newParent, nil, root.UpdateAt, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.circular_reference.app_error", appErr.Id)
	})

	t.Run("new depth would exceed MaxPageDepth", func(t *testing.T) {
		h := openTestService(t)
		channelID := mmmodel.NewId()
		space := mustCreateSpace(t, h.store, channelID)
		userID := mmmodel.NewId()

		// A chain of MaxPageDepth pages: the deepest has MaxPageDepth-1 ancestors, so a leaf
		// moved under it would land at depth (MaxPageDepth-1)+2 = MaxPageDepth+1 > MaxPageDepth.
		chain := buildPageChain(t, h.store, space.Id, channelID, userID, app.MaxPageDepth)
		deepest := chain[len(chain)-1]
		leaf := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

		newParent := deepest.Id
		_, _, appErr := h.svc.MovePage(leaf.Id, leaf.SpaceId, &newParent, nil, leaf.UpdateAt, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.max_depth_exceeded.app_error", appErr.Id)
	})

	t.Run("subtree would push past MaxPageDepth", func(t *testing.T) {
		h := openTestService(t)
		channelID := mmmodel.NewId()
		space := mustCreateSpace(t, h.store, channelID)
		userID := mmmodel.NewId()

		// Anchor at depth MaxPageDepth-1 (MaxPageDepth-2 ancestors): a moved page lands at
		// (MaxPageDepth-2)+2 = MaxPageDepth (passes the per-page check), but its one child would
		// be one deeper, MaxPageDepth+1, tripping the subtree check.
		chain := buildPageChain(t, h.store, space.Id, channelID, userID, app.MaxPageDepth-1)
		anchor := chain[len(chain)-1]

		subtreeRoot := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
		mustCreatePage(t, h.store, space.Id, channelID, userID, subtreeRoot.Id)

		newParent := anchor.Id
		_, _, appErr := h.svc.MovePage(subtreeRoot.Id, subtreeRoot.SpaceId, &newParent, nil, subtreeRoot.UpdateAt, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move.subtree_max_depth_exceeded.app_error", appErr.Id)
	})
}

// TestServiceMovePage_StaleBaselineConflicts verifies the optimistic lock: a stale
// expectedUpdateAt yields 409, and force overrides it. The store CASes on UpdateAt (like
// UpdatePage CASes on EditAt), so a stale baseline is a Conflict, not a not-found.
func TestServiceMovePage_StaleBaselineConflicts(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	parentID := parent.Id
	_, _, appErr := h.svc.MovePage(page.Id, page.SpaceId, &parentID, nil, page.UpdateAt-1, false)
	require.NotNil(t, appErr)
	require.Equal(t, 409, appErr.StatusCode)

	moved, _, appErr := h.svc.MovePage(page.Id, page.SpaceId, &parentID, nil, page.UpdateAt-1, true)
	require.Nil(t, appErr)
	require.Equal(t, parent.Id, moved.ParentId)
}
