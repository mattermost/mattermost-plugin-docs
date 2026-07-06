// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// DuplicatePage's default (includeChildren=false, targetSpaceID="", targetParentID=nil) is a
// same-space, same-parent, single-page copy with a fixed "Copy of" title. includeChildren copies
// the whole live subtree; targetSpaceID/targetParentID redirect the copy elsewhere. Per-page
// restriction handling is out of scope.

// TestServiceDuplicatePage copies a page in place: a new id, "Copy of" title, identical body,
// and the same space and parent as the source.
func TestServiceDuplicatePage(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	parent := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	source := mustCreatePage(t, h.store, space.Id, channelID, userID, parent.Id)

	dup, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", nil)
	require.Nil(t, appErr)
	require.NotEqual(t, source.Id, dup.Id)
	require.Equal(t, "Copy of "+source.Title, dup.Title)
	require.Equal(t, source.Body, dup.Body)
	require.Equal(t, source.SpaceId, dup.SpaceId)
	require.Equal(t, source.ParentId, dup.ParentId)
	require.Equal(t, channelID, dup.ChannelId)
	require.Equal(t, userID, dup.UserId)
	require.Equal(t, userID, dup.LastModifiedBy)

	// The duplicate is a real, fetchable live page distinct from the source.
	got, appErr := h.svc.GetPage(dup.Id)
	require.Nil(t, appErr)
	require.Equal(t, dup.Id, got.Id)
}

// TestServiceDuplicatePage_CopiesSearchText verifies the duplicate carries the source's SearchText
// verbatim (alongside the body) so it is immediately searchable like its source.
func TestServiceDuplicatePage_CopiesSearchText(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	source, appErr := h.svc.CreatePage(space.Id, "", "Searchable", "body text", "search text", userID, "")
	require.Nil(t, appErr)
	require.Equal(t, "search text", source.SearchText)

	dup, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", nil)
	require.Nil(t, appErr)
	require.Equal(t, source.SearchText, dup.SearchText)
}

// TestServiceDuplicatePage_CopiesProps verifies the duplicate carries a deep copy of the source's
// Props: same contents, but a distinct map so mutating one side can't alias the other.
func TestServiceDuplicatePage_CopiesProps(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	source, appErr := h.svc.CreatePage(space.Id, "", "Has Props", "body text", "", userID, "")
	require.Nil(t, appErr)

	// "nested" round-trips through the store as a plain map[string]any (JSON decoding never
	// reconstructs the named mmmodel.StringInterface type), so it's written that way here too.
	props := mmmodel.StringInterface{"pinned": true, "nested": map[string]any{"order": float64(1)}}
	source, appErr = h.svc.UpdatePage(source.Id, space.Id, &model.PagePatch{Props: &props}, source.EditAt, false, userID)
	require.Nil(t, appErr)
	require.Equal(t, props, source.Props)

	dup, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", nil)
	require.Nil(t, appErr)
	require.Equal(t, source.Props, dup.Props)

	dup.Props["pinned"] = false
	require.Equal(t, true, source.Props["pinned"], "mutating the duplicate's Props must not alias the source's")
}

// TestServiceDuplicatePage_TruncatesLongTitle verifies copyTitle truncates "Copy of <title>" to the
// page-title cap so the duplicate always passes CreatePage validation, even when the source title is
// already at the maximum length.
func TestServiceDuplicatePage_TruncatesLongTitle(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	longTitle := strings.Repeat("x", model.PageTitleMaxRunes)
	source, appErr := h.svc.CreatePage(space.Id, "", longTitle, "", "", userID, "")
	require.Nil(t, appErr)

	dup, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", nil)
	require.Nil(t, appErr)
	require.LessOrEqual(t, utf8.RuneCountInString(dup.Title), model.PageTitleMaxRunes)
	require.True(t, strings.HasPrefix(dup.Title, "Copy of "))
}

// TestServiceDuplicatePage_NotFound returns 404 for a missing source page.
func TestServiceDuplicatePage_NotFound(t *testing.T) {
	h := openTestService(t)
	_, appErr := h.svc.DuplicatePage(mmmodel.NewId(), mmmodel.NewId(), mmmodel.NewId(), false, "", nil)
	require.NotNil(t, appErr)
	require.Equal(t, 404, appErr.StatusCode)
}

// TestServiceDuplicatePage_InvalidID returns 400 for a malformed id.
func TestServiceDuplicatePage_InvalidID(t *testing.T) {
	h := openTestService(t)
	_, appErr := h.svc.DuplicatePage("not-an-id", mmmodel.NewId(), mmmodel.NewId(), false, "", nil)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
}

// TestServiceDuplicatePage_InvalidUserID returns 400 for a malformed userID, mirroring CreatePage.
func TestServiceDuplicatePage_InvalidUserID(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	source := mustCreatePage(t, h.store, space.Id, channelID, mmmodel.NewId(), "")

	_, appErr := h.svc.DuplicatePage(source.Id, space.Id, "not-an-id", false, "", nil)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.duplicate.invalid_user_id.app_error", appErr.Id)
}

// TestServiceDuplicatePage_IncludeChildren copies the source's whole live subtree: descendants
// keep their original titles (only the root gets "Copy of "), the same relative parent structure,
// and the source tree is left untouched.
func TestServiceDuplicatePage_IncludeChildren(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	source := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	child := mustCreatePage(t, h.store, space.Id, channelID, userID, source.Id)
	grandchild := mustCreatePage(t, h.store, space.Id, channelID, userID, child.Id)

	dup, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, true, "", nil)
	require.Nil(t, appErr)
	require.Equal(t, "Copy of "+source.Title, dup.Title)

	dupChildren, appErr := h.svc.GetPageChildren(dup.Id, space.Id, 0, 0)
	require.Nil(t, appErr)
	require.Len(t, dupChildren, 1)
	require.Equal(t, child.Title, dupChildren[0].Title)
	require.NotEqual(t, child.Id, dupChildren[0].Id)

	dupGrandchildren, appErr := h.svc.GetPageChildren(dupChildren[0].Id, space.Id, 0, 0)
	require.Nil(t, appErr)
	require.Len(t, dupGrandchildren, 1)
	require.Equal(t, grandchild.Title, dupGrandchildren[0].Title)

	// The source subtree is untouched.
	sourceChildren, appErr := h.svc.GetPageChildren(source.Id, space.Id, 0, 0)
	require.Nil(t, appErr)
	require.Len(t, sourceChildren, 1)
	require.Equal(t, child.Id, sourceChildren[0].Id)
}

// TestServiceDuplicatePage_MaxDepthExceeded rejects a single-page copy (includeChildren=false)
// whose destination placement alone would breach the depth cap — the depth check runs
// unconditionally, not only when includeChildren is set (see DuplicatePage's doc comment).
// Distinct from TestServiceDuplicatePage_IncludeChildren_MaxDepthExceeded, which breaches the cap
// via the copied subtree's own depth rather than the placement depth alone.
func TestServiceDuplicatePage_MaxDepthExceeded(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	// A chain of MaxPageDepth pages: the deepest has MaxPageDepth-1 ancestors, so a single page
	// copied under it would land at (MaxPageDepth-1)+2 = MaxPageDepth+1 > MaxPageDepth.
	var deepest *model.Page
	parentID := ""
	for range app.MaxPageDepth {
		deepest = mustCreatePage(t, h.store, space.Id, channelID, userID, parentID)
		parentID = deepest.Id
	}

	source := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	_, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", &deepest.Id)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.max_depth_exceeded.app_error", appErr.Id)
}

// TestServiceDuplicatePage_TargetParentNotFound rejects a targetParentID that doesn't exist,
// mirroring MovePage/MovePageToSpace's equivalent "non-existent parent" rejection.
func TestServiceDuplicatePage_TargetParentNotFound(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	source := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	ghost := mmmodel.NewId()
	_, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", &ghost)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.invalid_parent.app_error", appErr.Id)
}

// TestServiceDuplicatePage_TargetParentIsSourceItself nests the copy directly under the source page.
// This is not a cycle: the copy gets a brand-new id, so a targetParentID equal to the source's own
// id must succeed rather than being rejected as a self-parent circular reference.
func TestServiceDuplicatePage_TargetParentIsSourceItself(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	source := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	dup, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, false, "", &source.Id)
	require.Nil(t, appErr)
	require.NotEqual(t, source.Id, dup.Id)
	require.Equal(t, source.Id, dup.ParentId)
}

// TestServiceDuplicatePage_TargetParentInWrongSpace rejects a targetParentID that exists but lives
// in a space other than the resolved destination space, mirroring MovePageToSpace's equivalent
// rejection.
func TestServiceDuplicatePage_TargetParentInWrongSpace(t *testing.T) {
	h := openTestService(t)
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()

	sourceSpace := seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	source := mustCreatePage(t, h.store, sourceSpace.Id, sourceSpace.ChannelId, userID, "")

	targetSpace := seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	// parentInSource lives in sourceSpace, not targetSpace — an invalid destination parent for a
	// copy landing in targetSpace.
	parentInSource := mustCreatePage(t, h.store, sourceSpace.Id, sourceSpace.ChannelId, userID, "")

	_, appErr := h.svc.DuplicatePage(source.Id, sourceSpace.Id, userID, false, targetSpace.Id, &parentInSource.Id)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.move.parent_different_space.app_error", appErr.Id)
}

// TestServiceDuplicatePage_TargetSpaceAndParent copies the source into a different space on the
// same team, under a specific existing page there.
func TestServiceDuplicatePage_TargetSpaceAndParent(t *testing.T) {
	h := openTestService(t)
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()

	sourceSpace := seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	source := mustCreatePage(t, h.store, sourceSpace.Id, sourceSpace.ChannelId, userID, "")

	targetSpace := seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	targetParent := mustCreatePage(t, h.store, targetSpace.Id, targetSpace.ChannelId, userID, "")

	dup, appErr := h.svc.DuplicatePage(source.Id, sourceSpace.Id, userID, false, targetSpace.Id, &targetParent.Id)
	require.Nil(t, appErr)
	require.Equal(t, targetSpace.Id, dup.SpaceId)
	require.Equal(t, targetSpace.ChannelId, dup.ChannelId)
	require.Equal(t, targetParent.Id, dup.ParentId)
}

// TestServiceDuplicatePage_CrossSpaceDefaultsToRoot copies the source into a different space on the
// same team with no explicit parent, landing at that space's root (the source's own parent lives in
// the wrong space, so it cannot be reused).
func TestServiceDuplicatePage_CrossSpaceDefaultsToRoot(t *testing.T) {
	h := openTestService(t)
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()

	sourceSpace := seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)
	sourceParent := mustCreatePage(t, h.store, sourceSpace.Id, sourceSpace.ChannelId, userID, "")
	source := mustCreatePage(t, h.store, sourceSpace.Id, sourceSpace.ChannelId, userID, sourceParent.Id)

	targetSpace := seedSpaceInTeam(t, h.store, mmmodel.NewId(), teamID)

	dup, appErr := h.svc.DuplicatePage(source.Id, sourceSpace.Id, userID, false, targetSpace.Id, nil)
	require.Nil(t, appErr)
	require.Equal(t, targetSpace.Id, dup.SpaceId)
	require.Equal(t, "", dup.ParentId)
}

// TestServiceDuplicatePage_CrossTeamRejected rejects duplicating into a space on a different team,
// mirroring MovePageToSpace's cross-team rejection.
func TestServiceDuplicatePage_CrossTeamRejected(t *testing.T) {
	h := openTestService(t)
	userID := mmmodel.NewId()

	sourceSpace := mustCreateSpace(t, h.store, mmmodel.NewId())
	source := mustCreatePage(t, h.store, sourceSpace.Id, sourceSpace.ChannelId, userID, "")

	targetSpace := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.DuplicatePage(source.Id, sourceSpace.Id, userID, false, targetSpace.Id, nil)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
	require.Equal(t, "app.page.duplicate.cross_team.app_error", appErr.Id)
}

// TestServiceDuplicatePage_IncludeChildren_MaxDepthExceeded rejects a subtree copy whose deepest
// descendant would breach the depth cap at the destination, before creating anything.
func TestServiceDuplicatePage_IncludeChildren_MaxDepthExceeded(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	// A chain of MaxPageDepth-1 pages: the deepest has MaxPageDepth-2 ancestors, so
	// destinationDepth = (MaxPageDepth-2)+2 = MaxPageDepth — one level short of the cap.
	var deepest *model.Page
	parentID := ""
	for range app.MaxPageDepth - 1 {
		deepest = mustCreatePage(t, h.store, space.Id, channelID, userID, parentID)
		parentID = deepest.Id
	}

	// Source has one child, so its subtree adds one more level — enough to breach the cap when
	// copied under the chain's deepest page.
	source := mustCreatePage(t, h.store, space.Id, channelID, userID, "")
	mustCreatePage(t, h.store, space.Id, channelID, userID, source.Id)

	_, appErr := h.svc.DuplicatePage(source.Id, space.Id, userID, true, "", &deepest.Id)
	require.NotNil(t, appErr)
	require.Equal(t, 400, appErr.StatusCode)
}
