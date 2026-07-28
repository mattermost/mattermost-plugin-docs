// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// TestAdoptPublishRaceWinner exercises the PK-collision adoption path directly: PublishPageDraft
// reaches it only when a concurrent publish inserts the page between the target classification and
// the store insert, a window that cannot be held open through the public service surface.
func TestAdoptPublishRaceWinner(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	svc := New(s, nil, nil)
	space := testutil.MustCreateSpace(t, s, mmmodel.NewId(), mmmodel.NewId())
	userID := mmmodel.NewId()

	// newDraftAt reserves pageID as the user's new-page draft, mirroring CreateSpaceDraft.
	newDraftAt := func(t *testing.T, pageID string) *model.Draft {
		t.Helper()
		d, _, err := s.UpsertDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc"}, nil, nil, nil)
		require.NoError(t, err)
		return d
	}

	// winnerAt commits a live page at pageID, simulating the concurrent publish that won the id.
	winnerAt := func(t *testing.T, pageID string) *model.Page {
		t.Helper()
		winner := testutil.NewPage(space.Id, space.ChannelId, userID, "")
		winner.Id = pageID
		created, err := s.CreatePage(winner, testutil.UncappedMaxDepth)
		require.NoError(t, err)
		return created
	}

	t.Run("live winner in the same space is adopted and the orphaned draft is consumed", func(t *testing.T) {
		pageID := mmmodel.NewId()
		draft := newDraftAt(t, pageID)
		created := winnerAt(t, pageID)

		raced, adopted := svc.adoptPublishRaceWinner(userID, pageID, space.Id, draft.UpdateAt)
		require.True(t, adopted)
		require.Equal(t, created.Id, raced.Id)

		_, err := s.GetDraft(userID, pageID)
		require.True(t, store.IsErrNotFound(err), "the orphaned draft must be deleted, got %v", err)
	})

	t.Run("deleted winner is not adopted", func(t *testing.T) {
		pageID := mmmodel.NewId()
		draft := newDraftAt(t, pageID)
		winnerAt(t, pageID)
		_, err := s.DeletePage(pageID, space.Id, userID)
		require.NoError(t, err)

		raced, adopted := svc.adoptPublishRaceWinner(userID, pageID, space.Id, draft.UpdateAt)
		require.False(t, adopted, "a deleted page is not a publishable winner")
		require.Nil(t, raced)
	})

	t.Run("winner in another space is not adopted", func(t *testing.T) {
		pageID := mmmodel.NewId()
		draft := newDraftAt(t, pageID)
		winnerAt(t, pageID)

		raced, adopted := svc.adoptPublishRaceWinner(userID, pageID, mmmodel.NewId(), draft.UpdateAt)
		require.False(t, adopted, "a winner outside the caller's space is not adoptable")
		require.Nil(t, raced)

		_, err := s.GetDraft(userID, pageID)
		require.NoError(t, err, "a non-adopted draft must be left in place")
	})
}

// TestDerivePublishTarget covers the publish-target classification directly: the cross-space and
// deleted branches guard races (the page moved or was deleted between the draft read and the
// classification) that cannot be constructed through the public service surface — the draft
// read's liveness filter excludes both steady states.
func TestDerivePublishTarget(t *testing.T) {
	spaceID := mmmodel.NewId()

	t.Run("missing page is a new-page publish", func(t *testing.T) {
		notFound := mmmodel.NewAppError("GetPageWithDeleted", "app.page.get.not_found.app_error", nil, "", http.StatusNotFound)
		isNewPage, appErr := derivePublishTarget(nil, notFound, spaceID)
		require.Nil(t, appErr)
		require.True(t, isNewPage)
	})

	t.Run("read failure is passed through", func(t *testing.T) {
		readErr := mmmodel.NewAppError("GetPageWithDeleted", "app.store.not_found.app_error", nil, "", http.StatusInternalServerError)
		isNewPage, appErr := derivePublishTarget(nil, readErr, spaceID)
		require.False(t, isNewPage)
		require.Equal(t, readErr, appErr)
	})

	t.Run("page in another space reports not-found", func(t *testing.T) {
		isNewPage, appErr := derivePublishTarget(&model.Page{SpaceId: mmmodel.NewId()}, nil, spaceID)
		require.False(t, isNewPage)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusNotFound, appErr.StatusCode)
		require.Equal(t, "app.page_draft.publish.page_not_found.app_error", appErr.Id)
	})

	t.Run("deleted page conflicts", func(t *testing.T) {
		isNewPage, appErr := derivePublishTarget(&model.Page{SpaceId: spaceID, DeleteAt: 1}, nil, spaceID)
		require.False(t, isNewPage)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusConflict, appErr.StatusCode)
		require.Equal(t, "app.page_draft.publish.page_deleted.app_error", appErr.Id)
	})

	t.Run("live page in the caller's space is an edit publish", func(t *testing.T) {
		isNewPage, appErr := derivePublishTarget(&model.Page{SpaceId: spaceID}, nil, spaceID)
		require.Nil(t, appErr)
		require.False(t, isNewPage)
	})
}
