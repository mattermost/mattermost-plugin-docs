// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

func childOrder(t *testing.T, h *testHarness, parentID, spaceID string) []string {
	t.Helper()
	children, _, appErr := h.svc.GetPageChildren(parentID, spaceID, 0, 100)
	require.Nil(t, appErr)
	ids := make([]string, len(children))
	for i, c := range children {
		ids[i] = c.Id
	}
	return ids
}

// Reorder positions a page within its sibling group; these assert the resulting child order under
// the non-unique-SortOrder reindex model.

// TestServiceMovePage_PositionalReorder positions a page within its sibling group via newIndex
// (same parent), and verifies a large index clamps to the end.
func TestServiceMovePage_PositionalReorder(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	a := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)
	b := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)
	c := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

	require.Equal(t, []string{a.Id, b.Id, c.Id}, childOrder(t, h, parent.Id, space.Id), "initial creation order")

	// Move c to the front.
	front := int64(0)
	moved, appErr := h.svc.MovePage(c.Id, c.SpaceId, &parent.Id, &front, new(c.UpdateAt), false, mmmodel.NewId())
	require.Nil(t, appErr)
	require.Equal(t, parent.Id, moved.ParentId)
	require.Equal(t, []string{c.Id, a.Id, b.Id}, childOrder(t, h, parent.Id, space.Id))

	// Move c to a large index → clamps to the end.
	end := int64(100)
	cAfter, getErr := h.svc.GetPage(c.Id)
	require.Nil(t, getErr)
	_, appErr = h.svc.MovePage(c.Id, cAfter.SpaceId, &parent.Id, &end, new(cAfter.UpdateAt), false, mmmodel.NewId())
	require.Nil(t, appErr)
	require.Equal(t, []string{a.Id, b.Id, c.Id}, childOrder(t, h, parent.Id, space.Id))

	// Move b to a negative index → clamps to the front.
	bAfter, getErr := h.svc.GetPage(b.Id)
	require.Nil(t, getErr)
	negative := int64(-5)
	_, appErr = h.svc.MovePage(b.Id, bAfter.SpaceId, &parent.Id, &negative, new(bAfter.UpdateAt), false, mmmodel.NewId())
	require.Nil(t, appErr)
	require.Equal(t, []string{b.Id, a.Id, c.Id}, childOrder(t, h, parent.Id, space.Id))
}

// TestServiceMovePage_ReparentAtIndex reparents a page into another group at a specific position.
func TestServiceMovePage_ReparentAtIndex(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	source := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	moving := mustCreatePage(t, h.store, space.Id, channelID, userID, source.Id)

	dest := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	x := mustCreatePage(t, h.store, space.Id, channelID, userID, dest.Id)
	y := mustCreatePage(t, h.store, space.Id, channelID, userID, dest.Id)

	// Move `moving` under dest, between x and y.
	idx := int64(1)
	moved, appErr := h.svc.MovePage(moving.Id, moving.SpaceId, &dest.Id, &idx, new(moving.UpdateAt), false, mmmodel.NewId())
	require.Nil(t, appErr)
	require.Equal(t, dest.Id, moved.ParentId)
	require.Equal(t, []string{x.Id, moving.Id, y.Id}, childOrder(t, h, dest.Id, space.Id))
}
