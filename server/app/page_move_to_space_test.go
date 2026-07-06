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

// seedSpaceInTeam creates a space with a caller-chosen team id (mustCreateSpace randomizes it).
func seedSpaceInTeam(t *testing.T, s *store.Store, channelID, teamID string) *model.Space {
	t.Helper()
	space, err := s.CreateSpace(&model.Space{ChannelId: channelID, TeamId: teamID, CreatorId: mmmodel.NewId(), Title: "Test Space"})
	require.NoError(t, err)
	return space
}

func pageIDs(pages []*model.Page) map[string]bool {
	set := make(map[string]bool, len(pages))
	for _, p := range pages {
		set[p.Id] = true
	}
	return set
}

// MovePageToSpace moves a page and its subtree to another space in the same team; cross-team moves
// are rejected, and per-page restriction handling is out of scope.

// TestServiceMovePageToSpace moves a page and its child to another space in the same team: both
// rows are re-homed (SpaceId + ChannelId), the child stays under the moved root, the root lands at
// the target root, and the pages leave the source space's listing.
func TestServiceMovePageToSpace(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	root := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")
	child := mustCreatePage(t, h.store, spaceA.Id, chA, user, root.Id)

	moved, appErr := h.svc.MovePageToSpace(root.Id, spaceA.Id, spaceB.Id, nil, root.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, spaceB.Id, moved.SpaceId)
	require.Equal(t, chB, moved.ChannelId)
	require.Empty(t, moved.ParentId)

	gotChild, appErr := h.svc.GetPage(child.Id)
	require.Nil(t, appErr)
	require.Equal(t, spaceB.Id, gotChild.SpaceId, "child should follow the subtree to the target space")
	require.Equal(t, chB, gotChild.ChannelId)
	require.Equal(t, root.Id, gotChild.ParentId, "child stays under the moved root")

	pagesB, appErr := h.svc.GetSpacePages(spaceB.Id, 0, 100)
	require.Nil(t, appErr)
	idsB := pageIDs(pagesB)
	require.True(t, idsB[root.Id] && idsB[child.Id], "both pages now live in the target space")

	pagesA, appErr := h.svc.GetSpacePages(spaceA.Id, 0, 100)
	require.Nil(t, appErr)
	require.Empty(t, pagesA, "source space is left empty")
}

// TestServiceMovePageToSpace_ReturnsMovedPage verifies MovePageToSpace's return value directly
// reflects the new SpaceId/ChannelId/ParentId without a separate GetPage call: the returned page
// must already match what a fresh read of the moved row would show.
func TestServiceMovePageToSpace_ReturnsMovedPage(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	target := mustCreatePage(t, h.store, spaceB.Id, chB, user, "")
	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	parentID := target.Id
	moved, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, &parentID, page.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, spaceB.Id, moved.SpaceId)
	require.Equal(t, chB, moved.ChannelId)
	require.Equal(t, target.Id, moved.ParentId)

	refetched, appErr := h.svc.GetPage(page.Id)
	require.Nil(t, appErr)
	require.Equal(t, refetched.SpaceId, moved.SpaceId)
	require.Equal(t, refetched.ChannelId, moved.ChannelId)
	require.Equal(t, refetched.ParentId, moved.ParentId)
	require.Equal(t, refetched.UpdateAt, moved.UpdateAt, "the returned page must already carry the post-move UpdateAt")
}

// TestServiceMovePageToSpace_RejectsCrossTeam blocks a move to a space in another team.
func TestServiceMovePageToSpace_RejectsCrossTeam(t *testing.T) {
	h := openTestService(t)
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, mmmodel.NewId())
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, mmmodel.NewId())

	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")
	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, nil, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move_to_space.cross_team.app_error", appErr.Id)
}

// TestServiceMovePageToSpace_InvalidIDs verifies the up-front id-format validation branches.
func TestServiceMovePageToSpace_InvalidIDs(t *testing.T) {
	h := openTestService(t)

	t.Run("invalid pageID", func(t *testing.T) {
		_, appErr := h.svc.MovePageToSpace("not-an-id", mmmodel.NewId(), mmmodel.NewId(), nil, 0, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move_to_space.invalid_id.app_error", appErr.Id)
	})

	t.Run("invalid sourceSpaceID", func(t *testing.T) {
		_, appErr := h.svc.MovePageToSpace(mmmodel.NewId(), "not-an-id", mmmodel.NewId(), nil, 0, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move_to_space.invalid_source_space.app_error", appErr.Id)
	})

	t.Run("invalid targetSpaceID", func(t *testing.T) {
		_, appErr := h.svc.MovePageToSpace(mmmodel.NewId(), mmmodel.NewId(), "not-an-id", nil, 0, false)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
		require.Equal(t, "app.page.move_to_space.invalid_target_space.app_error", appErr.Id)
	})
}

// TestServiceMovePageToSpace_RejectsParentInWrongSpace blocks a destination parent that lives in a
// space other than the target.
func TestServiceMovePageToSpace_RejectsParentInWrongSpace(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")
	parentInA := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	parentID := parentInA.Id
	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, &parentID, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.parent_different_space.app_error", appErr.Id)
}

// TestServiceMovePageToSpace_RejectsMissingParent blocks a destination parent that doesn't exist
// at all, distinct from RejectsParentInWrongSpace (which covers a parent that exists but lives in
// the wrong space).
func TestServiceMovePageToSpace_RejectsMissingParent(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	ghost := mmmodel.NewId()
	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, &ghost, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.invalid_parent.app_error", appErr.Id)
}

// TestServiceMovePageToSpace_RejectsDepthExceeded blocks a cross-space move that would nest the
// page past the depth cap (parity with MovePage's depth enforcement).
func TestServiceMovePageToSpace_RejectsDepthExceeded(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	// Build a chain in spaceB down to MaxPageDepth; a child under the deepest node would breach it.
	parentID := ""
	for range app.MaxPageDepth {
		p := mustCreatePage(t, h.store, spaceB.Id, chB, user, parentID)
		parentID = p.Id
	}

	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")
	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, &parentID, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.max_depth_exceeded.app_error", appErr.Id)
}

// TestServiceMovePageToSpace_RewritesSnapshots verifies a version snapshot (OriginalId set,
// soft-deleted) of a moved page is re-homed to the target space/channel alongside the live rows.
func TestServiceMovePageToSpace_RewritesSnapshots(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	root := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	// Seed a version snapshot of root: OriginalId = root.Id, soft-deleted.
	snap := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")
	_, rawErr := h.db.Exec(
		"UPDATE DOCS_Page SET OriginalId = $2, DeleteAt = $3 WHERE Id = $1",
		snap.Id, root.Id, mmmodel.GetMillis())
	require.NoError(t, rawErr)

	_, appErr := h.svc.MovePageToSpace(root.Id, spaceA.Id, spaceB.Id, nil, 0, true)
	require.Nil(t, appErr)

	var spaceID, channelID string
	rawErr = h.db.QueryRow("SELECT SpaceId, ChannelId FROM DOCS_Page WHERE Id = $1", snap.Id).Scan(&spaceID, &channelID)
	require.NoError(t, rawErr)
	require.Equal(t, spaceB.Id, spaceID, "snapshot follows the moved subtree to the target space")
	require.Equal(t, chB, channelID)
}

// TestServiceMovePageToSpace_RejectsCycle blocks moving a page under one of its own descendants.
func TestServiceMovePageToSpace_RejectsCycle(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)

	root := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")
	child := mustCreatePage(t, h.store, spaceA.Id, chA, user, root.Id)

	childID := child.Id
	_, appErr := h.svc.MovePageToSpace(root.Id, spaceA.Id, spaceA.Id, &childID, 0, true)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.circular_reference.app_error", appErr.Id)
}

// TestServiceMovePageToSpace_StaleBaselineConflicts verifies the optimistic lock on the moved root:
// a stale expectedUpdateAt yields 409, and force overrides it. The store CASes on UpdateAt, mirroring
// MovePage.
func TestServiceMovePageToSpace_StaleBaselineConflicts(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	// A cross-space move falls through to the store CAS (the no-op short-circuit only applies when
	// the page is already in the target space under the requested parent).
	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, nil, page.UpdateAt-1, false)
	require.NotNil(t, appErr)
	require.Equal(t, 409, appErr.StatusCode)

	moved, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, nil, page.UpdateAt-1, true)
	require.Nil(t, appErr)
	require.Equal(t, spaceB.Id, moved.SpaceId)
}

// TestServiceMovePageToSpace_NoOpEnforcesBaseline verifies the no-op short-circuit (page already in
// the target space under the requested parent) still enforces the optimistic lock: a stale baseline
// yields 409, a current baseline succeeds unchanged, and force overrides a stale one. Without this
// the no-op would silently succeed against a stale baseline, unlike MovePage whose no-op routes
// through the store CAS.
func TestServiceMovePageToSpace_NoOpEnforcesBaseline(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)

	// A freshly created root page in spaceA: targeting spaceA with parentPageID nil is the genuine
	// no-op (already in the target space, already at the root).
	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceA.Id, nil, page.UpdateAt-1, false)
	require.NotNil(t, appErr)
	require.Equal(t, 409, appErr.StatusCode)

	same, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceA.Id, nil, page.UpdateAt, false)
	require.Nil(t, appErr)
	require.Equal(t, page.Id, same.Id)
	require.Equal(t, spaceA.Id, same.SpaceId)

	_, appErr = h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceA.Id, nil, page.UpdateAt-1, true)
	require.Nil(t, appErr)
}

// TestServiceMovePageToSpace_NoOpRejectsStaleSource verifies the no-op short-circuit does not fire
// for a stale route: if the page was concurrently moved out of sourceSpaceID before this call runs,
// landing it in targetSpaceID under the requested parent looks like a no-op by SpaceId/ParentId
// alone, but the caller's sourceSpaceID no longer matches. It must fall through to the store's
// scoped predicate (not found) rather than reporting a false success.
func TestServiceMovePageToSpace_NoOpRejectsStaleSource(t *testing.T) {
	h := openTestService(t)
	teamID := mmmodel.NewId()
	user := mmmodel.NewId()

	chA := mmmodel.NewId()
	spaceA := seedSpaceInTeam(t, h.store, chA, teamID)
	chB := mmmodel.NewId()
	spaceB := seedSpaceInTeam(t, h.store, chB, teamID)

	page := mustCreatePage(t, h.store, spaceA.Id, chA, user, "")

	// Concurrently relocate the page to spaceB, out from under the caller's stale spaceA route.
	_, appErr := h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, nil, page.UpdateAt, false)
	require.Nil(t, appErr)

	// The stale request still names spaceA as the source and spaceB as the target; the page is
	// already in spaceB at the root, which matches the no-op's SpaceId/ParentId check, but not its
	// sourceSpaceID.
	_, appErr = h.svc.MovePageToSpace(page.Id, spaceA.Id, spaceB.Id, nil, page.UpdateAt, false)
	require.NotNil(t, appErr)
	require.Equal(t, 404, appErr.StatusCode)
}
