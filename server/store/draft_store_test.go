// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	sq "github.com/mattermost/squirrel"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// --- Draft tests ---

func newDraft(userID, spaceID, pageID, parentID string) *model.Draft {
	return &model.Draft{
		UserId:   userID,
		SpaceId:  spaceID,
		PageId:   pageID,
		ParentId: parentID,
		Title:    "Test Draft",
		Body:     `{"type":"doc","content":[]}`,
	}
}

func TestDraft(t *testing.T) {
	t.Run("upsert then get returns the stored draft", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		saved, _, err := s.UpsertDraft(newDraft(userID, spaceID, pageID, ""), nil, nil, nil)
		require.NoError(t, err)
		require.NotZero(t, saved.CreateAt)

		got, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.Equal(t, pageID, got.PageId)
		require.Equal(t, "Test Draft", got.Title)
		require.Equal(t, spaceID, got.SpaceId)
	})

	t.Run("upsert replaces existing row and preserves CreateAt", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		first, _, err := s.UpsertDraft(newDraft(userID, spaceID, pageID, ""), nil, nil, nil)
		require.NoError(t, err)

		second := newDraft(userID, spaceID, pageID, "")
		second.CreateAt = first.CreateAt
		second.Title = "Updated"
		_, _, err = s.UpsertDraft(second, nil, nil, nil)
		require.NoError(t, err)

		got, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.Equal(t, "Updated", got.Title)
		require.Equal(t, first.CreateAt, got.CreateAt, "CreateAt preserved across upsert")
	})

	t.Run("an autosave that omits a field keeps the stored value", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)

		full := newDraft(userID, space.Id, pageID, "")
		full.Title = "Original title"
		full.Body = `{"type":"doc","content":[{"type":"paragraph"}]}`
		full.BaseEditAt = 1234
		stored, _, err := s.UpsertDraft(full, nil, nil, nil)
		require.NoError(t, err)

		// A body-only heartbeat: no title, no props. Neither may be wiped.
		bodyOnly := newDraft(userID, space.Id, pageID, "")
		bodyOnly.Title = ""
		bodyOnly.Body = `{"type":"doc","content":[{"type":"paragraph"},{"type":"paragraph"}]}`
		bodyOnly.Props = nil
		saved, _, err := s.UpsertDraft(bodyOnly, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "Original title", saved.Title, "an omitted title must not wipe the stored one")
		require.Equal(t, bodyOnly.Body, saved.Body, "the sent body must be written")
		require.Equal(t, int64(1234), saved.BaseEditAt,
			"an omitted baseline must not drop the stored optimistic-lock baseline")
		require.Equal(t, stored.CreateAt, saved.CreateAt, "CreateAt preserved across upsert")

		// A title-only heartbeat: no body. The body just written must survive.
		titleOnly := newDraft(userID, space.Id, pageID, "")
		titleOnly.Title = "Renamed"
		titleOnly.Body = ""
		saved, _, err = s.UpsertDraft(titleOnly, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "Renamed", saved.Title)
		require.Equal(t, bodyOnly.Body, saved.Body, "an omitted body must not wipe the stored one")
	})

	t.Run("two users can draft the same page id", func(t *testing.T) {
		s := openTestDB(t)
		pageID := mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id
		userA, userB := mmmodel.NewId(), mmmodel.NewId()

		_, _, err := s.UpsertDraft(newDraft(userA, spaceID, pageID, ""), nil, nil, nil)
		require.NoError(t, err)
		_, _, err = s.UpsertDraft(newDraft(userB, spaceID, pageID, ""), nil, nil, nil)
		require.NoError(t, err)

		gotA, err := s.GetDraft(userA, pageID)
		require.NoError(t, err)
		require.Equal(t, userA, gotA.UserId)
		gotB, err := s.GetDraft(userB, pageID)
		require.NoError(t, err)
		require.Equal(t, userB, gotB.UserId)
	})

	t.Run("delete makes draft not found", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		_, _, err := s.UpsertDraft(newDraft(userID, spaceID, pageID, ""), nil, nil, nil)
		require.NoError(t, err)

		require.NoError(t, s.DeleteDraft(userID, pageID))

		_, err = s.GetDraft(userID, pageID)
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("delete nonexistent draft returns not-found", func(t *testing.T) {
		s := openTestDB(t)
		err := s.DeleteDraft(mmmodel.NewId(), mmmodel.NewId())
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("get nonexistent draft returns not-found", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.GetDraft(mmmodel.NewId(), mmmodel.NewId())
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("drafts for space lists new-page drafts most-recent-first", func(t *testing.T) {
		s := openTestDB(t)
		userID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)
		second, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		drafts, err := s.GetDraftsForSpace(userID, space.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, drafts, 2)
		require.Equal(t, second.PageId, drafts[0].PageId, "most-recently-updated first")
	})

	t.Run("drafts for soft-deleted space are not listed but survive for restore", func(t *testing.T) {
		s := openTestDB(t)
		userID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		draft, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpace(space.Id))

		// While the space is soft-deleted both reads are gated to nothing...
		drafts, err := s.GetDraftsForSpace(userID, space.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, drafts, "a soft-deleted space lists no drafts")

		_, err = s.GetDraft(userID, draft.PageId)
		require.True(t, store.IsErrNotFound(err), "a soft-deleted space gates GetDraft too")

		// ...but the draft row is kept (not purged), so it reappears once the space is restored.
		require.NoError(t, s.RestoreSpace(space.Id))
		drafts, err = s.GetDraftsForSpace(userID, space.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, drafts, 1, "restoring the space brings its drafts back")

		kept, err := s.GetDraft(userID, draft.PageId)
		require.NoError(t, err, "draft is readable again after restore")
		require.Equal(t, draft.PageId, kept.PageId)
	})

	t.Run("a draft on a live page is hidden while its space is soft-deleted", func(t *testing.T) {
		// The subtest above drafts against a page id that has no page row, so the filter's
		// p.Id IS NULL branch carries it. Here the draft sits on a live page row, so only the
		// space-liveness JOIN can hide it.
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		d := newDraft(userID, space.Id, page.Id, "")
		d.BaseEditAt = page.EditAt
		saved, _, err := s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpace(space.Id))

		_, err = s.GetDraft(userID, page.Id)
		require.True(t, store.IsErrNotFound(err), "the draft must be hidden while its space is soft-deleted")

		require.NoError(t, s.RestoreSpace(space.Id))

		got, err := s.GetDraft(userID, page.Id)
		require.NoError(t, err, "the draft must survive the space delete+restore round trip")
		require.Equal(t, saved.Body, got.Body)
		require.Equal(t, saved.UpdateAt, got.UpdateAt)
	})

	t.Run("drafts for space excludes a draft whose page lives in another space", func(t *testing.T) {
		s := openTestDB(t)
		userID := mmmodel.NewId()

		spaceA, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		spaceB, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		// A live page in space B. UpsertDraft refuses to attach a space-A draft to it (see the
		// write-path test below), so insert the cross-space row directly to exercise the
		// read-path guard against a corrupt or legacy row.
		pageInB, err := s.CreatePage(newPage(spaceB.Id, spaceB.ChannelId, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		now := mmmodel.GetMillis()
		_, rawErr := s.RawExecForTest(
			"INSERT INTO DOCS_Draft (UserId, SpaceId, PageId, ParentId, Title, Body, FileIds, Props, CreateAt, UpdateAt) VALUES ($1, $2, $3, '', '', '', '[]', '{}', $4, $4)",
			userID, spaceA.Id, pageInB.Id, now)
		require.NoError(t, rawErr)

		drafts, err := s.GetDraftsForSpace(userID, spaceA.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, drafts, "a draft whose page belongs to another space must not be listed")
	})

	t.Run("upsert rejects a draft whose page lives in another space", func(t *testing.T) {
		s := openTestDB(t)
		userID := mmmodel.NewId()

		spaceA, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		spaceB, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		pageInB, err := s.CreatePage(newPage(spaceB.Id, spaceB.ChannelId, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		_, _, err = s.UpsertDraft(newDraft(userID, spaceA.Id, pageInB.Id, ""), nil, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "expected invalid input for a cross-space page, got %v", err)
	})

	t.Run("drafts for space excludes drafts on soft-deleted pages", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		// A draft editing a live page is included.
		live, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		dLive := newDraft(userID, space.Id, live.Id, "")
		dLive.BaseEditAt = live.EditAt
		_, _, err = s.UpsertDraft(dLive, nil, nil, nil)
		require.NoError(t, err)

		// A draft whose page is soft-deleted is excluded.
		deleted, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		dDeleted := newDraft(userID, space.Id, deleted.Id, "")
		dDeleted.BaseEditAt = deleted.EditAt
		_, _, err = s.UpsertDraft(dDeleted, nil, nil, nil)
		require.NoError(t, err)
		require.NoError(t, deletePageErr(s, deleted.Id, deleted.SpaceId, userID))

		drafts, err := s.GetDraftsForSpace(userID, space.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, drafts, 1)
		require.Equal(t, live.Id, drafts[0].PageId)
	})

	t.Run("drafts for space excludes drafts on version snapshots", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		// Draft a live page, then turn that page into a version snapshot (OriginalId set,
		// soft-deleted) directly. UpsertDraft refuses to attach to a snapshot, so the draft is
		// written while the page is still live; the read path must then exclude it: the LEFT
		// JOIN matches the snapshot row, OriginalId != '' fails the live-page predicate, and
		// p.Id IS NULL is false.
		snap, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		dSnap := newDraft(userID, space.Id, snap.Id, "")
		dSnap.BaseEditAt = snap.EditAt
		_, _, err = s.UpsertDraft(dSnap, nil, nil, nil)
		require.NoError(t, err)
		_, rawErr := s.ExecBuilderForTest(s.QueryBuilderForTest().
			Update("DOCS_Page").
			Set("OriginalId", mmmodel.NewId()).
			Set("DeleteAt", mmmodel.GetMillis()).
			Where(sq.Eq{"Id": snap.Id}))
		require.NoError(t, rawErr)

		drafts, err := s.GetDraftsForSpace(userID, space.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, drafts, "a draft on a version snapshot must be excluded")
	})

	t.Run("upsert rejects a draft for a soft-deleted page", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		require.NoError(t, deletePageErr(s, page.Id, page.SpaceId, userID))

		// An autosave landing after the page was deleted must not recreate a draft for it.
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, page.Id, ""), nil, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "expected invalid input for a deleted page, got %v", err)
	})

	t.Run("upsert rejects a draft in a soft-deleted space", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		require.NoError(t, s.DeleteSpace(space.Id))

		_, _, err = s.UpsertDraft(newDraft(mmmodel.NewId(), space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.True(t, store.IsErrNotFound(err), "expected not-found for a deleted space, got %v", err)
	})

	t.Run("upsert accepts a new-page draft under a live parent", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		parentID := parent.Id
		saved, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), parentID), &parentID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, parent.Id, saved.ParentId)
	})

	t.Run("upsert rejects a draft whose parent does not exist", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		missingParentID := mmmodel.NewId()
		_, _, err = s.UpsertDraft(newDraft(mmmodel.NewId(), space.Id, mmmodel.NewId(), missingParentID), &missingParentID, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "expected invalid input for a missing parent, got %v", err)
	})

	t.Run("upsert rejects a draft whose parent is soft-deleted", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		require.NoError(t, deletePageErr(s, parent.Id, parent.SpaceId, userID))

		parentID := parent.Id
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), parentID), &parentID, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "expected invalid input for a deleted parent, got %v", err)
	})

	t.Run("upsert rejects a draft whose parent lives in another space", func(t *testing.T) {
		s := openTestDB(t)
		userID := mmmodel.NewId()

		spaceA, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		spaceB, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		parentInB, err := s.CreatePage(newPage(spaceB.Id, spaceB.ChannelId, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		parentID := parentInB.Id
		_, _, err = s.UpsertDraft(newDraft(userID, spaceA.Id, mmmodel.NewId(), parentID), &parentID, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "expected invalid input for a cross-space parent, got %v", err)
	})

	t.Run("upsert accepts a parent that is the user's own draft in the same space", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		parentDraft, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		parentPageID := parentDraft.PageId
		saved, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), parentPageID), &parentPageID, nil, nil)
		require.NoError(t, err)
		require.Equal(t, parentDraft.PageId, saved.ParentId)
	})

	t.Run("upsert rejects a parent that is another user's draft", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userA, userB := mmmodel.NewId(), mmmodel.NewId()

		otherDraft, _, err := s.UpsertDraft(newDraft(userA, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		otherPageID := otherDraft.PageId
		_, _, err = s.UpsertDraft(newDraft(userB, space.Id, mmmodel.NewId(), otherPageID), &otherPageID, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "expected invalid input for another user's draft parent, got %v", err)
	})

	// TestDraft/"upsert rejects a draft whose parent chain cycles back to itself" exercises
	// checkNoDraftCycle's cycle branch: a root new-page draft, a second draft parented under it,
	// then re-parenting the root under the second draft closes the loop root -> child -> root.
	t.Run("upsert rejects a draft whose parent chain cycles back to itself", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		rootDraft, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		rootPageID := rootDraft.PageId
		childDraft, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), rootPageID), &rootPageID, nil, nil)
		require.NoError(t, err)

		childPageID := childDraft.PageId
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, rootDraft.PageId, childPageID), &childPageID, nil, nil)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
		require.Equal(t, store.ReasonDraftCycle, inv.Reason)
	})

	// TestDraft/"upsert rejects a draft whose parent chain exceeds the max depth" exercises
	// checkNoDraftCycle's too-deep branch. Each draft added to the chain is itself parent-chain
	// validated, so a chain of exactly model.MaxPageDepth new-page drafts is the deepest one
	// that can be built without tripping the cap; a further draft parented under the deepest one
	// is rejected as too deep.
	t.Run("upsert rejects a draft whose parent chain exceeds the max depth", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		parentID := ""
		for range model.MaxPageDepth {
			pageID := mmmodel.NewId()
			var parentParam *string
			if parentID != "" {
				p := parentID
				parentParam = &p
			}
			_, _, err = s.UpsertDraft(newDraft(userID, space.Id, pageID, parentID), parentParam, nil, nil)
			require.NoError(t, err)
			parentID = pageID
		}

		deepestParentID := parentID
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), deepestParentID), &deepestParentID, nil, nil)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
		require.Equal(t, store.ReasonDraftTooDeep, inv.Reason)
	})

	t.Run("drafts for space is scoped to the user", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userA, userB := mmmodel.NewId(), mmmodel.NewId()

		_, _, err = s.UpsertDraft(newDraft(userA, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)
		_, _, err = s.UpsertDraft(newDraft(userB, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		drafts, err := s.GetDraftsForSpace(userA, space.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, drafts, 1)
		require.Equal(t, userA, drafts[0].UserId)
	})

	t.Run("body, file_ids and props round-trip through the database", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		d := newDraft(userID, spaceID, pageID, "")
		d.Body = `{"type":"doc","content":[{"type":"paragraph"}]}`
		d.FileIds = mmmodel.StringArray{mmmodel.NewId(), mmmodel.NewId()}
		d.Props = mmmodel.StringInterface{"k": float64(1700000000123)}
		_, _, err := s.UpsertDraft(d, nil, &d.FileIds, &d.Props)
		require.NoError(t, err)

		got, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.Equal(t, d.Body, got.Body)
		require.Equal(t, d.FileIds, got.FileIds, "StringArray must round-trip through the TEXT column")
		require.Equal(t, float64(1700000000123), got.Props["k"], "Props must round-trip through the jsonb column")
	})

	t.Run("empty props default to an empty map on read", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		_, _, err := s.UpsertDraft(newDraft(userID, spaceID, pageID, ""), nil, nil, nil)
		require.NoError(t, err)

		got, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.NotNil(t, got.Props)
		require.Empty(t, got.Props)
	})

	t.Run("upsert overwrites parent id", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		// Parents must be live pages in the space (UpsertDraft validates ParentId liveness).
		firstPage, err := s.CreatePage(newPage(spaceID, space.ChannelId, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		secondPage, err := s.CreatePage(newPage(spaceID, space.ChannelId, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		firstParent, secondParent := firstPage.Id, secondPage.Id

		_, _, err = s.UpsertDraft(newDraft(userID, spaceID, pageID, firstParent), &firstParent, nil, nil)
		require.NoError(t, err)
		got, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.Equal(t, firstParent, got.ParentId)

		_, _, err = s.UpsertDraft(newDraft(userID, spaceID, pageID, secondParent), &secondParent, nil, nil)
		require.NoError(t, err)
		got, err = s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.Equal(t, secondParent, got.ParentId, "second upsert must overwrite ParentId")
	})

	t.Run("title-only empty body round-trips", func(t *testing.T) {
		s := openTestDB(t)
		userID, pageID := mmmodel.NewId(), mmmodel.NewId()
		space, spaceErr := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, spaceErr)
		spaceID := space.Id

		d := newDraft(userID, spaceID, pageID, "")
		d.Title = "Title Only"
		d.Body = ""
		_, _, err := s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		got, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		require.Equal(t, "Title Only", got.Title)
		require.Equal(t, "", got.Body, "empty body must round-trip as empty string")
	})

	t.Run("drafts for space excludes other spaces for the same user", func(t *testing.T) {
		s := openTestDB(t)
		userID := mmmodel.NewId()
		spaceA, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		spaceB, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)

		_, _, err = s.UpsertDraft(newDraft(userID, spaceA.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)
		_, _, err = s.UpsertDraft(newDraft(userID, spaceA.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)
		_, _, err = s.UpsertDraft(newDraft(userID, spaceB.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)

		drafts, err := s.GetDraftsForSpace(userID, spaceA.Id, 0, testDraftListLimit)
		require.NoError(t, err)
		require.Len(t, drafts, 2)
		for _, d := range drafts {
			require.Equal(t, spaceA.Id, d.SpaceId)
		}
	})

	t.Run("drafts for space returns empty when user has none", func(t *testing.T) {
		s := openTestDB(t)
		drafts, err := s.GetDraftsForSpace(mmmodel.NewId(), mmmodel.NewId(), 0, testDraftListLimit)
		require.NoError(t, err)
		require.Empty(t, drafts)
	})

	t.Run("store rejects invalid ids", func(t *testing.T) {
		s := openTestDB(t)
		valid := mmmodel.NewId()

		// Upsert runs the full model IsValid, so a malformed (non-empty) id is rejected as
		// invalid input.
		_, _, err := s.UpsertDraft(newDraft("bad", valid, valid, ""), nil, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "upsert with bad user id, got %v", err)

		// Upsert with nil draft must return ErrInvalidInput.
		_, _, err = s.UpsertDraft(nil, nil, nil, nil)
		require.True(t, store.IsErrInvalidInput(err), "upsert nil draft, got %v", err)

		// Get/Delete guard only against empty ids (matching the page/space store convention);
		// a non-empty but unknown id falls through to the query and returns not-found.
		_, err = s.GetDraft("", valid)
		require.True(t, store.IsErrInvalidInput(err), "get with empty user id, got %v", err)

		_, err = s.GetDraft(valid, "")
		require.True(t, store.IsErrInvalidInput(err), "get with empty page id, got %v", err)

		err = s.DeleteDraft("", valid)
		require.True(t, store.IsErrInvalidInput(err), "delete with empty user id, got %v", err)

		err = s.DeleteDraft(valid, "")
		require.True(t, store.IsErrInvalidInput(err), "delete with empty page id, got %v", err)
	})

	t.Run("GetDraftsForSpace rejects empty userID", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.GetDraftsForSpace("", mmmodel.NewId(), 0, testDraftListLimit)
		require.True(t, store.IsErrInvalidInput(err), "got %v", err)
	})

	t.Run("GetDraftsForSpace rejects empty spaceID", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.GetDraftsForSpace(mmmodel.NewId(), "", 0, testDraftListLimit)
		require.True(t, store.IsErrInvalidInput(err), "got %v", err)
	})

	t.Run("GetDraftsForSpace rejects non-positive limit", func(t *testing.T) {
		s := openTestDB(t)
		_, err := s.GetDraftsForSpace(mmmodel.NewId(), mmmodel.NewId(), 0, 0)
		require.True(t, store.IsErrInvalidInput(err), "zero limit must be rejected, got %v", err)
	})
}

// TestDeletePageReparentsPendingDrafts verifies that deleting a page reparents the new-page
// drafts pending under it to the deleted page's parent — mirroring live-child promotion — so a
// draft never dangles under a soft-deleted parent and stays publishable.
func TestDeletePageReparentsPendingDrafts(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	grandparent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	parent, err := s.CreatePage(newPage(space.Id, channelID, userID, grandparent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	// A new-page draft (its own page not yet created) pending as a child of parent.
	newPageID := mmmodel.NewId()
	parentID := parent.Id
	_, _, err = s.UpsertDraft(newDraft(userID, space.Id, newPageID, parentID), &parentID, nil, nil)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, parent.Id, parent.SpaceId, userID))

	// The draft survives and is reparented to the deleted page's parent (the grandparent),
	// which the invariant guarantees is live.
	got, err := s.GetDraft(userID, newPageID)
	require.NoError(t, err, "pending draft must survive its parent's deletion")
	require.Equal(t, grandparent.Id, got.ParentId, "draft must be reparented to the deleted page's parent")

	// The reparented draft is publishable: CreatePage with its parent now succeeds.
	_, err = s.CreatePage(newPage(space.Id, channelID, userID, got.ParentId), testDefaultMaxDepth)
	require.NoError(t, err, "draft's reparented parent must be a valid live parent")
}

// TestDeletePagePreservesEditDraftAndReparentsChildDraft verifies the two draft cascades DeletePage
// runs stay isolated when both apply to the same delete: the deleted page's own edit draft (keyed
// PageId = pageID) is preserved-but-hidden, while a pending new-page draft parented under that page
// (ParentId = pageID) is reparented to the deleted page's parent. reparentDraftsForPage matches only
// on ParentId, never PageId, which is why it cannot touch the edit draft row.
func TestDeletePagePreservesEditDraftAndReparentsChildDraft(t *testing.T) {
	s := openTestDB(t)
	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	grandparent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	page, err := s.CreatePage(newPage(space.Id, channelID, userID, grandparent.Id), testDefaultMaxDepth)
	require.NoError(t, err)

	editDraft := newDraft(userID, space.Id, page.Id, "")
	editDraft.BaseEditAt = page.EditAt
	_, _, err = s.UpsertDraft(editDraft, nil, nil, nil)
	require.NoError(t, err)

	childPageID := mmmodel.NewId()
	parentID := page.Id
	_, _, err = s.UpsertDraft(newDraft(userID, space.Id, childPageID, parentID), &parentID, nil, nil)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, page.Id, space.Id, userID))

	_, err = s.GetDraft(userID, page.Id)
	require.True(t, store.IsErrNotFound(err), "the edit draft must be hidden while its page is deleted")

	child, err := s.GetDraft(userID, childPageID)
	require.NoError(t, err, "the pending child draft must survive as a reparented new-page draft")
	require.Equal(t, grandparent.Id, child.ParentId, "child draft must be reparented to the deleted page's parent")

	// Restore brings the edit draft back; the child's reparent is a structural rewrite that
	// RestorePage does not undo, since RestorePage never touches draft rows. Full field parity
	// across the round trip is pinned by TestDeletePage in store_test.go.
	_, err = s.RestorePage(page.Id, space.Id, userID, testDefaultMaxDepth)
	require.NoError(t, err)

	_, err = s.GetDraft(userID, page.Id)
	require.NoError(t, err, "the edit draft must reappear after restore")

	stillReparented, err := s.GetDraft(userID, childPageID)
	require.NoError(t, err)
	require.Equal(t, grandparent.Id, stillReparented.ParentId, "the child draft's reparent survives the page's own restore")
}

// TestUpsertDraftQuotaCountsDraftsHiddenByPageDelete pins the quota consequence of preserving a
// draft whose page was deleted: countDraftsForUser has no liveness join, so the hidden row still
// consumes a slot even though no read path lists it. This bounds total row growth, but it also means
// the owner can be refused a new draft while their visible listing is short of the cap.
func TestUpsertDraftQuotaCountsDraftsHiddenByPageDelete(t *testing.T) {
	s := openTestDB(t)
	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	d := newDraft(userID, space.Id, page.Id, "")
	d.BaseEditAt = page.EditAt
	_, _, err = s.UpsertDraft(d, nil, nil, nil)
	require.NoError(t, err)

	require.NoError(t, deletePageErr(s, page.Id, space.Id, userID))

	// One hidden draft exists; fill the rest of the quota with visible new-page drafts.
	for range model.MaxDraftsPerUserPerSpace - 1 {
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
		require.NoError(t, err)
	}

	visible, err := s.GetDraftsForSpace(userID, space.Id, 0, testDraftListLimit)
	require.NoError(t, err)
	require.Len(t, visible, model.MaxDraftsPerUserPerSpace-1, "the page-deleted draft stays excluded from the visible listing")

	// The store holds the full quota (1 hidden + Max-1 visible), so one more upsert must be refused.
	// A liveness-filtered count would wrongly allow it, since only Max-1 drafts are visible.
	_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), ""), nil, nil, nil)
	require.True(t, store.IsErrLimitExceeded(err), "expected the hidden draft to count against the quota, got %T: %v", err, err)
}

// TestRestoreSpaceLeavesIndividuallyDeletedPageDraftHidden carries RestoreSpace's stamp-scoped
// un-cascade through to draft visibility: RestoreSpace only revives pages carrying the space's own
// DeleteAt stamp, so a page deleted individually beforehand keeps its earlier stamp and stays
// deleted — and its draft stays hidden even though the space is live again.
func TestRestoreSpaceLeavesIndividuallyDeletedPageDraftHidden(t *testing.T) {
	s := openTestDB(t)
	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	userID := mmmodel.NewId()

	individual, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)
	cascaded, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	dIndividual := newDraft(userID, space.Id, individual.Id, "")
	dIndividual.BaseEditAt = individual.EditAt
	_, _, err = s.UpsertDraft(dIndividual, nil, nil, nil)
	require.NoError(t, err)

	dCascaded := newDraft(userID, space.Id, cascaded.Id, "")
	dCascaded.BaseEditAt = cascaded.EditAt
	_, _, err = s.UpsertDraft(dCascaded, nil, nil, nil)
	require.NoError(t, err)

	// Delete one page first so its stamp predates the space's cascade stamp, which DeleteSpace
	// computes to be strictly greater than any existing page DeleteAt.
	require.NoError(t, deletePageErr(s, individual.Id, space.Id, userID))

	require.NoError(t, s.DeleteSpace(space.Id))
	require.NoError(t, s.RestoreSpace(space.Id))

	_, err = s.GetDraft(userID, cascaded.Id)
	require.NoError(t, err, "a draft on a page the space delete cascaded must reappear after RestoreSpace")

	_, err = s.GetPage(individual.Id, false)
	require.True(t, store.IsErrNotFound(err), "individually-deleted page must stay deleted after RestoreSpace")
	_, err = s.GetDraft(userID, individual.Id)
	require.True(t, store.IsErrNotFound(err), "its draft must stay hidden after RestoreSpace")
}

// TestDiscardPathsReachDraftHiddenByPageDelete verifies the explicit discard paths are not gated by
// applyDraftLivenessFilter the way the read paths are, so a draft the reads currently hide is still
// discardable. This is the only route by which an owner reclaims a hidden draft's quota slot, and it
// requires knowing the page id — no read path will surface it.
func TestDiscardPathsReachDraftHiddenByPageDelete(t *testing.T) {
	t.Run("DeleteDraftVersion discards a draft hidden by its page's soft-delete", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()
		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		d := newDraft(userID, space.Id, page.Id, "")
		d.BaseEditAt = page.EditAt
		saved, _, err := s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, page.Id, space.Id, userID))
		_, err = s.GetDraft(userID, page.Id)
		require.True(t, store.IsErrNotFound(err), "the draft must be hidden while its page is deleted")

		discarded, delErr := s.DeleteDraftVersion(userID, page.Id, saved.UpdateAt)
		require.NoError(t, delErr)
		require.True(t, discarded, "the CAS delete must match the hidden row")

		// Restoring proves the row is gone rather than merely hidden: it does not come back.
		_, err = s.RestorePage(page.Id, space.Id, userID, testDefaultMaxDepth)
		require.NoError(t, err)
		_, err = s.GetDraft(userID, page.Id)
		require.True(t, store.IsErrNotFound(err), "the discarded draft must not reappear after restore")
	})

	t.Run("DeleteDraftReparenting discards a draft hidden by its page's soft-delete", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()
		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		d := newDraft(userID, space.Id, page.Id, "")
		d.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		childPageID := mmmodel.NewId()
		parentID := page.Id
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, childPageID, parentID), &parentID, nil, nil)
		require.NoError(t, err)

		require.NoError(t, deletePageErr(s, page.Id, space.Id, userID))

		// pageExistsInSpace reports false for a soft-deleted page, so the discard reads the page as
		// not-live and takes the new-page reparent branch. That branch is a no-op here: DeletePage's
		// own reparentDraftsForPage already moved every ParentId pointing at this page, so nothing
		// still matches. The return value is what carries the consequence — the caller uses it to pick
		// the presence-broadcast audience.
		pageWasLive, delErr := s.DeleteDraftReparenting(userID, space.Id, page.Id)
		require.NoError(t, delErr)
		require.False(t, pageWasLive, "a soft-deleted page must read as not-live to the discard path")

		_, err = s.GetDraft(userID, page.Id)
		require.True(t, store.IsErrNotFound(err), "the discarded edit draft must be gone")

		child, err := s.GetDraft(userID, childPageID)
		require.NoError(t, err, "the child draft must be untouched by the sibling discard")
		require.Equal(t, "", child.ParentId, "the child was already reparented to root by the page delete")
	})
}

// TestGetActiveEditorsForPage covers the presence window predicate: a draft updated at/after the
// cutoff counts its user as active; one before the cutoff, or on another page, does not.
func TestGetActiveEditorsForPage(t *testing.T) {
	s := openTestDB(t)
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	pageID := mmmodel.NewId()
	userID := mmmodel.NewId()
	_, _, err = s.UpsertDraft(newDraft(userID, space.Id, pageID, ""), nil, nil, nil)
	require.NoError(t, err)
	now := mmmodel.GetMillis()

	t.Run("within window includes the editor", func(t *testing.T) {
		editors, err := s.GetPageActiveEditors(pageID, space.Id, now-5*60*1000)
		require.NoError(t, err)
		require.Contains(t, editors, userID)
	})

	t.Run("cutoff after the update excludes the editor", func(t *testing.T) {
		editors, err := s.GetPageActiveEditors(pageID, space.Id, now+60*1000)
		require.NoError(t, err)
		require.NotContains(t, editors, userID)
	})

	t.Run("cutoff exactly at LastActiveAt includes the editor", func(t *testing.T) {
		// The window is inclusive (LastActiveAt >= cutoff): a draft updated exactly at the cutoff
		// still counts, pinning the >= predicate against an accidental strict >.
		d, err := s.GetDraft(userID, pageID)
		require.NoError(t, err)
		editors, err := s.GetPageActiveEditors(pageID, space.Id, d.LastActiveAt)
		require.NoError(t, err)
		require.Contains(t, editors, userID)
	})

	t.Run("a different page has no editors", func(t *testing.T) {
		editors, err := s.GetPageActiveEditors(mmmodel.NewId(), space.Id, 0)
		require.NoError(t, err)
		require.Empty(t, editors)
	})

	t.Run("a new-page draft at the same reserved id in another space does not leak", func(t *testing.T) {
		otherSpace, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		otherUser := mmmodel.NewId()
		// Same (reserved) pageID, different space and user — an unpublished new-page draft.
		_, _, err = s.UpsertDraft(newDraft(otherUser, otherSpace.Id, pageID, ""), nil, nil, nil)
		require.NoError(t, err)

		editors, err := s.GetPageActiveEditors(pageID, space.Id, mmmodel.GetMillis()-5*60*1000)
		require.NoError(t, err)
		require.Contains(t, editors, userID)
		require.NotContains(t, editors, otherUser,
			"presence for a page must not disclose an editor from another space sharing the reserved id")
	})
}

func TestGetActiveEditorsForPageInputValidation(t *testing.T) {
	s := openTestDB(t)
	valid := mmmodel.NewId()

	_, err := s.GetPageActiveEditors("", valid, 0)
	require.True(t, store.IsErrInvalidInput(err), "empty pageID, got %v", err)

	_, err = s.GetPageActiveEditors(valid, "", 0)
	require.True(t, store.IsErrInvalidInput(err), "empty spaceID, got %v", err)
}

func TestGetActiveEditorsForPageMultipleEditorsOrderedByLastActiveAt(t *testing.T) {
	s := openTestDB(t)
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	pageID := mmmodel.NewId()
	userA, userB := mmmodel.NewId(), mmmodel.NewId()

	_, _, err = s.UpsertDraft(newDraft(userA, space.Id, pageID, ""), nil, nil, nil)
	require.NoError(t, err)
	_, _, err = s.UpsertDraft(newDraft(userB, space.Id, pageID, ""), nil, nil, nil)
	require.NoError(t, err)

	// Push userA's LastActiveAt into the past so userB (more recent) should appear first.
	past := mmmodel.GetMillis() - 60*1000
	_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Draft").
		Set("LastActiveAt", past).
		Where(sq.Eq{"UserId": userA, "PageId": pageID}))
	require.NoError(t, err)

	editors, err := s.GetPageActiveEditors(pageID, space.Id, 0)
	require.NoError(t, err)
	require.Len(t, editors, 2)
	require.Equal(t, userB, editors[0], "most-recently-active editor must appear first")
	require.Equal(t, userA, editors[1])
}

// TestGetActiveEditorsForPageIgnoresMaintenanceWrites pins presence to LastActiveAt rather than
// UpdateAt. Deleting a page reparents the drafts pending under it, which stamps their UpdateAt
// without their owner having touched them — that must not report the owner as an active editor.
func TestGetActiveEditorsForPageIgnoresMaintenanceWrites(t *testing.T) {
	s := openTestDB(t)
	channelID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	userID := mmmodel.NewId()
	parent, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	// A new-page draft pending under the parent, last actually edited well outside the window.
	childPageID := mmmodel.NewId()
	parentID := parent.Id
	_, _, err = s.UpsertDraft(newDraft(userID, space.Id, childPageID, parentID), &parentID, nil, nil)
	require.NoError(t, err)

	stale := mmmodel.GetMillis() - 60*60*1000
	_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Draft").
		Set("UpdateAt", stale).
		Set("LastActiveAt", stale).
		Where(sq.Eq{"UserId": userID, "PageId": childPageID}))
	require.NoError(t, err)

	// Someone else deletes the parent, which reparents the pending draft and bumps its UpdateAt.
	_, err = s.DeletePage(parent.Id, space.Id, mmmodel.NewId())
	require.NoError(t, err)

	cutoff := mmmodel.GetMillis() - 5*60*1000
	editors, err := s.GetPageActiveEditors(childPageID, space.Id, cutoff)
	require.NoError(t, err)
	require.NotContains(t, editors, userID,
		"reparenting a draft must not report its owner as an active editor")
}

// TestUpsertDraftBumpsUpdateAtMonotonically guards the draft's UpdateAt version token: it must
// advance strictly past the stored value even when the saving node's wall clock is behind it, so a
// later autosave can never commit an UpdateAt that collides with the value a publish already
// captured (which would let the publish CAS delete the newer draft and ship older content).
func TestUpsertDraftBumpsUpdateAtMonotonically(t *testing.T) {
	s := openTestDB(t)
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)
	userID := mmmodel.NewId()
	pageID := mmmodel.NewId()

	_, _, err = s.UpsertDraft(newDraft(userID, space.Id, pageID, ""), nil, nil, nil)
	require.NoError(t, err)

	// Force the stored UpdateAt ahead of the next save's wall clock. Without the monotonic bump,
	// the next upsert would write a smaller UpdateAt (its own GetMillis()).
	future := mmmodel.GetMillis() + 60*60*1000
	_, err = s.ExecBuilderForTest(s.QueryBuilderForTest().
		Update("DOCS_Draft").
		Set("UpdateAt", future).
		Where(sq.Eq{"UserId": userID, "PageId": pageID}))
	require.NoError(t, err)

	saved, _, err := s.UpsertDraft(newDraft(userID, space.Id, pageID, ""), nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, future+1, saved.UpdateAt,
		"UpdateAt must advance to stored+1 when the incoming timestamp is not already greater")
}

func TestDeleteDraftVersion(t *testing.T) {
	s := openTestDB(t)
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)
	userID := mmmodel.NewId()
	pageID := mmmodel.NewId()

	saved, _, err := s.UpsertDraft(newDraft(userID, space.Id, pageID, ""), nil, nil, nil)
	require.NoError(t, err)

	t.Run("stale version deletes nothing and leaves the draft intact", func(t *testing.T) {
		deleted, delErr := s.DeleteDraftVersion(userID, pageID, saved.UpdateAt-1)
		require.NoError(t, delErr)
		require.False(t, deleted, "a mismatched version must not delete the row")
		got, getErr := s.GetDraft(userID, pageID)
		require.NoError(t, getErr, "the draft must survive a stale-version delete")
		require.Equal(t, saved.UpdateAt, got.UpdateAt)
	})

	t.Run("matching version deletes the draft", func(t *testing.T) {
		deleted, delErr := s.DeleteDraftVersion(userID, pageID, saved.UpdateAt)
		require.NoError(t, delErr)
		require.True(t, deleted, "the matching version must delete the row")
		_, getErr := s.GetDraft(userID, pageID)
		require.True(t, store.IsErrNotFound(getErr), "the draft must be gone")
	})

	t.Run("missing draft reports false without error", func(t *testing.T) {
		deleted, delErr := s.DeleteDraftVersion(userID, mmmodel.NewId(), 1)
		require.NoError(t, delErr)
		require.False(t, deleted)
	})
}

// TestPublishDraft covers the atomic publish transactions at the store boundary: the new-page
// insert-and-delete-draft path (PublishNewPageDraft), and the edit path's optimistic-lock CAS
// (PublishPageEditDraft).
func TestPublishDraft(t *testing.T) {
	t.Run("new page inserts the page and deletes the draft", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()
		pageID := mmmodel.NewId()

		draft, _, err := s.UpsertDraft(newDraft(userID, space.Id, pageID, ""), nil, nil, nil)
		require.NoError(t, err)

		page := &model.Page{Id: pageID, SpaceId: space.Id, Title: "Published", Body: `{"type":"doc","content":[]}`, UserId: userID}
		published, err := s.PublishNewPageDraft(page, userID, space.Id, testDefaultMaxDepth, draft.UpdateAt)
		require.NoError(t, err)
		require.Equal(t, pageID, published.Id)

		_, getErr := s.GetDraft(userID, pageID)
		require.True(t, store.IsErrNotFound(getErr), "draft must be deleted by publish")

		live, err := s.GetPage(pageID, false)
		require.NoError(t, err)
		require.Equal(t, "Published", live.Title)
	})

	t.Run("edit path conflicts on a stale baseline", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		d := newDraft(userID, space.Id, created.Id, "")
		d.BaseEditAt = created.EditAt
		draft, _, err := s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		title := "Edited"
		body := `{"type":"doc","content":[]}`
		searchText := ""
		patch := &model.PagePatch{Title: &title, Body: &body, SearchText: &searchText}

		_, err = s.PublishPageEditDraft(created.Id, space.Id, patch, created.EditAt-1, false, userID, draft.UpdateAt) // stale baseline
		require.True(t, store.IsErrConflict(err), "a stale baseline must conflict, got %v", err)
	})

	t.Run("edit path succeeds with a matching baseline", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		d2 := newDraft(userID, space.Id, created.Id, "")
		d2.BaseEditAt = created.EditAt
		draft, _, err := s.UpsertDraft(d2, nil, nil, nil)
		require.NoError(t, err)

		title := "Edited"
		body := `{"type":"doc","content":[]}`
		searchText := ""
		patch := &model.PagePatch{Title: &title, Body: &body, SearchText: &searchText}

		published, err := s.PublishPageEditDraft(created.Id, space.Id, patch, created.EditAt, false, userID, draft.UpdateAt) // matching baseline
		require.NoError(t, err)
		require.Equal(t, "Edited", published.Title)
		require.Greater(t, published.EditAt, created.EditAt, "publish advances EditAt")

		_, getErr := s.GetDraft(userID, created.Id)
		require.True(t, store.IsErrNotFound(getErr), "draft must be deleted by publish")
	})

	t.Run("an autosave landing after the draft was read rolls the publish back", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		created, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)
		d3 := newDraft(userID, space.Id, created.Id, "")
		d3.BaseEditAt = created.EditAt
		stale, _, err := s.UpsertDraft(d3, nil, nil, nil)
		require.NoError(t, err)

		// The user's editor autosaves again after the publish path read the draft.
		newer := newDraft(userID, space.Id, created.Id, "")
		newer.Body = `{"type":"doc","content":[{"type":"paragraph"}]}`
		newer.BaseEditAt = created.EditAt
		newer, _, err = s.UpsertDraft(newer, nil, nil, nil)
		require.NoError(t, err)
		require.Greater(t, newer.UpdateAt, stale.UpdateAt, "the autosave must advance UpdateAt")

		title := "Published from stale content"
		body := `{"type":"doc","content":[]}`
		searchText := ""
		patch := &model.PagePatch{Title: &title, Body: &body, SearchText: &searchText}

		_, err = s.PublishPageEditDraft(created.Id, space.Id, patch, created.EditAt, false, userID, stale.UpdateAt)
		require.True(t, store.IsErrConflict(err), "publishing stale draft content must conflict, got %v", err)

		// The page must be untouched and the newer draft must survive for the client to republish.
		live, err := s.GetPage(created.Id, false)
		require.NoError(t, err)
		require.NotEqual(t, "Published from stale content", live.Title, "the rolled-back publish must not have written the page")

		survived, err := s.GetDraft(userID, created.Id)
		require.NoError(t, err, "the newer draft must survive the rolled-back publish")
		require.Equal(t, newer.Body, survived.Body)
	})
}

// TestUpsertDraftBaseEditAtWriteOnce verifies BaseEditAt is frozen at the establishing INSERT: a
// later upsert on the same (UserId, PageId) key carries a different BaseEditAt, but the stored
// (and returned) value never moves off the value the draft was established with.
func TestUpsertDraftBaseEditAtWriteOnce(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	established := newDraft(userID, space.Id, page.Id, "")
	established.BaseEditAt = page.EditAt
	saved, _, err := s.UpsertDraft(established, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, page.EditAt, saved.BaseEditAt)

	later := newDraft(userID, space.Id, page.Id, "")
	later.BaseEditAt = page.EditAt + 1000
	updated, _, err := s.UpsertDraft(later, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, page.EditAt, updated.BaseEditAt,
		"BaseEditAt is write-once: a later upsert must not change the established baseline")

	// The persisted row (not just the returned struct) must reflect the same frozen value.
	persisted, err := s.GetDraft(userID, page.Id)
	require.NoError(t, err)
	require.Equal(t, page.EditAt, persisted.BaseEditAt)
}

// TestUpsertDraftPropsReplaceOrKeep verifies the whole-value replace-or-keep semantics of the props
// write-intent pointer: nil preserves the stored map untouched, a non-nil pointer replaces the whole
// map (dropping any key it doesn't carry), and a non-nil pointer to an empty map clears every key.
func TestUpsertDraftPropsReplaceOrKeep(t *testing.T) {
	s := openTestDB(t)

	userID, pageID := mmmodel.NewId(), mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	d := newDraft(userID, space.Id, pageID, "")
	d.Props = mmmodel.StringInterface{"foo": "bar"}
	stored, _, err := s.UpsertDraft(d, nil, nil, &d.Props)
	require.NoError(t, err)
	require.Equal(t, "bar", stored.Props["foo"])

	// A nil props pointer omits the write and preserves the stored map.
	omit := newDraft(userID, space.Id, pageID, "")
	afterOmit, _, err := s.UpsertDraft(omit, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "bar", afterOmit.Props["foo"], "a nil props pointer must preserve the stored map")

	// A non-nil props pointer replaces the whole map: the unrelated "foo" key set above is gone,
	// not merged with the new "baz" key.
	replace := newDraft(userID, space.Id, pageID, "")
	replace.Props = mmmodel.StringInterface{"baz": "qux"}
	afterReplace, _, err := s.UpsertDraft(replace, nil, nil, &replace.Props)
	require.NoError(t, err)
	require.Equal(t, "qux", afterReplace.Props["baz"])
	require.NotContains(t, afterReplace.Props, "foo",
		"a non-nil props pointer must replace the whole map, not merge keys")

	// A non-nil pointer to an empty map clears every key.
	toClear := newDraft(userID, space.Id, pageID, "")
	emptyProps := mmmodel.StringInterface{}
	cleared, _, err := s.UpsertDraft(toClear, nil, nil, &emptyProps)
	require.NoError(t, err)
	require.Empty(t, cleared.Props, "a non-nil pointer to an empty map must clear all keys")
}

// TestUpsertDraftOversizedPropsRejected verifies the store rejects a draft whose Props field
// (the field Draft.IsValid actually checks) exceeds PagePropsMaxBytes, regardless of what the
// props write-intent pointer carries. This is enforced by Draft.IsValid, not by the pointer's
// contents — sizing the pointer's target (rather than draft.Props) is the App layer's job.
func TestUpsertDraftOversizedPropsRejected(t *testing.T) {
	s := openTestDB(t)

	userID, pageID := mmmodel.NewId(), mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
	require.NoError(t, err)

	d := newDraft(userID, space.Id, pageID, "")
	d.Props = mmmodel.StringInterface{"k": strings.Repeat("x", model.PagePropsMaxBytes)}
	_, _, err = s.UpsertDraft(d, nil, nil, &d.Props)
	require.Error(t, err)
	require.True(t, store.IsErrInvalidInput(err), "oversized draft.Props must be rejected by Draft.IsValid, got %v", err)
}

// TestUpsertDraftEstablishGuardRejectsBaselineAheadOfPage verifies the establish-time guard: an
// establishing INSERT (no existing draft row) whose BaseEditAt is ahead of the live page's current
// EditAt is impossible (the client cannot have seen a version newer than the one that exists) and
// is rejected as invalid input. A baseline equal to the page's EditAt is accepted; a baseline
// behind it is not caught by this guard but is still rejected by the separate resurrection check
// (see TestUpsertDraftResurrectionClassification).
func TestUpsertDraftEstablishGuardRejectsBaselineAheadOfPage(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)

	t.Run("ahead of the live page is rejected", func(t *testing.T) {
		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		ahead := newDraft(userID, space.Id, page.Id, "")
		ahead.BaseEditAt = page.EditAt + 1000
		_, _, err = s.UpsertDraft(ahead, nil, nil, nil)
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
		require.Equal(t, "BaseEditAt", inv.Field)
	})

	t.Run("equal to the live page is accepted", func(t *testing.T) {
		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		equal := newDraft(userID, space.Id, page.Id, "")
		equal.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(equal, nil, nil, nil)
		require.NoError(t, err, "an establishing baseline equal to the live page's EditAt must be accepted")
	})

	// A baseline strictly behind the live page's EditAt passes the ahead-only guard above (it is
	// not "ahead"), but is still rejected — by the separate resurrection check just below the
	// guard, since this is still a first-ever establish (no existing draft row) and the page
	// advanced past the caller's baseline. This is a real optimistic-lock conflict, not a bug:
	// the client's session is already stale on its very first save.
	t.Run("behind the live page is rejected as a stale baseline, not by the ahead-only guard", func(t *testing.T) {
		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		behind := newDraft(userID, space.Id, page.Id, "")
		behind.BaseEditAt = page.EditAt - 1
		_, _, err = s.UpsertDraft(behind, nil, nil, nil)
		require.Error(t, err)
		require.False(t, store.IsErrInvalidInput(err), "a behind baseline must not trip the ahead-only establish guard")
		require.True(t, store.IsErrConflict(err), "expected ErrConflict, got %T: %v", err, err)
		require.Equal(t, store.ReasonConcurrentEdit, store.ConflictReason(err))
	})
}

// TestUpsertDraftConcurrentFirstAutosavesSerialize verifies that two concurrent establishing
// upserts for the same (userID, pageID) on an existing page do not both take the "no existing
// draft" branch: the per-space FOR UPDATE lock (lockLiveSpace) serializes them, so only the first
// is a true establish and every later one observes the row the first inserted and is treated as an
// update — neither is falsely rejected by the establish-time guard.
func TestUpsertDraftConcurrentFirstAutosavesSerialize(t *testing.T) {
	s := openTestDB(t)

	channelID := mmmodel.NewId()
	userID := mmmodel.NewId()
	space, err := s.CreateSpace(newSpace(channelID))
	require.NoError(t, err)
	page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
	require.NoError(t, err)

	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			<-start
			d := newDraft(userID, space.Id, page.Id, "")
			d.BaseEditAt = page.EditAt
			_, _, errs[i] = s.UpsertDraft(d, nil, nil, nil)
		}()
	}
	close(start)
	wg.Wait()

	for i, uErr := range errs {
		require.NoError(t, uErr, "concurrent first-autosave %d must not be falsely rejected by the establish guard", i)
	}

	got, err := s.GetDraft(userID, page.Id)
	require.NoError(t, err)
	require.Equal(t, page.EditAt, got.BaseEditAt)
}

// TestUpsertDraftResurrectionClassification verifies UpsertDraft distinguishes the two resurrection
// reasons: an autosave with a stale non-zero BaseEditAt behind the page's current EditAt (the page
// advanced under it) classifies as ReasonConcurrentEdit, while an autosave with no baseline (0) on a
// page id a concurrent publish just claimed classifies as ReasonConcurrentAutosave. Both fire only
// when the draft row a resurrection would recreate no longer exists (a concurrent publish consumed
// it), matching the "refuse to resurrect a consumed draft" contract in UpsertDraft.
func TestUpsertDraftResurrectionClassification(t *testing.T) {
	t.Run("stale non-zero baseline behind the page classifies as concurrent edit", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)
		page, err := s.CreatePage(newPage(space.Id, channelID, userID, ""), testDefaultMaxDepth)
		require.NoError(t, err)

		d := newDraft(userID, space.Id, page.Id, "")
		d.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		// The page is edited (advancing EditAt past the draft's baseline), and the draft is
		// removed — simulating a concurrent publish that consumed it.
		newTitle := "Edited concurrently"
		edited, err := s.UpdatePage(page.Id, page.SpaceId, &model.PagePatch{Title: &newTitle}, page.EditAt, false, userID)
		require.NoError(t, err)
		require.Greater(t, edited.EditAt, page.EditAt)
		require.NoError(t, s.DeleteDraft(userID, page.Id))

		// A stale-baseline autosave tries to re-establish the now-consumed draft.
		stale := newDraft(userID, space.Id, page.Id, "")
		stale.BaseEditAt = page.EditAt
		_, _, err = s.UpsertDraft(stale, nil, nil, nil)
		require.Error(t, err)
		require.True(t, store.IsErrConflict(err), "expected ErrConflict, got %T: %v", err, err)
		require.Equal(t, store.ReasonConcurrentEdit, store.ConflictReason(err))
	})

	t.Run("no baseline on a page a concurrent publish just claimed classifies as concurrent autosave", func(t *testing.T) {
		s := openTestDB(t)
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		space, err := s.CreateSpace(newSpace(channelID))
		require.NoError(t, err)

		pageID := mmmodel.NewId()
		d := newDraft(userID, space.Id, pageID, "")
		_, _, err = s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)

		// A concurrent publish creates the page at that exact id and removes the draft.
		published := newPage(space.Id, channelID, userID, "")
		published.Id = pageID
		_, err = s.CreatePage(published, testDefaultMaxDepth)
		require.NoError(t, err)
		require.NoError(t, s.DeleteDraft(userID, pageID))

		// A baseline-less autosave tries to re-establish the now-consumed new-page draft.
		stale := newDraft(userID, space.Id, pageID, "")
		_, _, err = s.UpsertDraft(stale, nil, nil, nil)
		require.Error(t, err)
		require.True(t, store.IsErrConflict(err), "expected ErrConflict, got %T: %v", err, err)
		require.Equal(t, store.ReasonConcurrentAutosave, store.ConflictReason(err))
	})
}

// TestUpsertDraftCountsLiveAncestorDepth pins checkNoDraftCycle's live-ancestor arithmetic: the
// live parent chain's depth counts toward model.MaxPageDepth for a new draft, including when the
// caller holds an edit draft on the live ancestor. An edit draft's stored ParentId defaults to ”
// (version bookkeeping, not a hierarchy edge), and a chain walk that followed it would skip the
// ancestor's real depth and admit drafts that can never publish within the cap.
func TestUpsertDraftCountsLiveAncestorDepth(t *testing.T) {
	buildLiveChain := func(t *testing.T, s *store.Store, spaceID, channelID, userID string, depth int) *model.Page {
		t.Helper()
		parentID := ""
		var page *model.Page
		var err error
		for range depth {
			page, err = s.CreatePage(newPage(spaceID, channelID, userID, parentID), model.MaxPageDepth)
			require.NoError(t, err)
			parentID = page.Id
		}
		return page
	}

	establishEditDraft := func(t *testing.T, s *store.Store, spaceID string, page *model.Page, userID string) {
		t.Helper()
		d := newDraft(userID, spaceID, page.Id, "")
		d.BaseEditAt = page.EditAt
		_, _, err := s.UpsertDraft(d, nil, nil, nil)
		require.NoError(t, err)
	}

	requireTooDeep := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		var inv *store.ErrInvalidInput
		require.True(t, errors.As(err, &inv), "expected ErrInvalidInput, got %T: %v", err, err)
		require.Equal(t, store.ReasonDraftTooDeep, inv.Reason)
	}

	t.Run("rejects a draft under a cap-deep live chain despite an edit draft on the ancestor", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		deepest := buildLiveChain(t, s, space.Id, space.ChannelId, userID, model.MaxPageDepth)
		establishEditDraft(t, s, space.Id, deepest, userID)

		parentID := deepest.Id
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), parentID), &parentID, nil, nil)
		requireTooDeep(t, err)
	})

	t.Run("accepts a draft that lands exactly at the cap below a live chain", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		deepest := buildLiveChain(t, s, space.Id, space.ChannelId, userID, model.MaxPageDepth-1)
		establishEditDraft(t, s, space.Id, deepest, userID)

		parentID := deepest.Id
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), parentID), &parentID, nil, nil)
		require.NoError(t, err)
	})

	t.Run("combined live and draft chain depth is capped across the live boundary", func(t *testing.T) {
		s := openTestDB(t)
		space, err := s.CreateSpace(newSpace(mmmodel.NewId()))
		require.NoError(t, err)
		userID := mmmodel.NewId()

		deepest := buildLiveChain(t, s, space.Id, space.ChannelId, userID, model.MaxPageDepth-2)
		establishEditDraft(t, s, space.Id, deepest, userID)

		// Two new-page drafts chained below the live ancestor land exactly at the cap.
		parentID := deepest.Id
		d1, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), parentID), &parentID, nil, nil)
		require.NoError(t, err)
		d1ID := d1.PageId
		d2, _, err := s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), d1ID), &d1ID, nil, nil)
		require.NoError(t, err)

		// One more level would publish past the cap, so it is rejected at draft time.
		d2ID := d2.PageId
		_, _, err = s.UpsertDraft(newDraft(userID, space.Id, mmmodel.NewId(), d2ID), &d2ID, nil, nil)
		requireTooDeep(t, err)
	})
}
