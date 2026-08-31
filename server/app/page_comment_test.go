// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/lib/pq"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// commentAPIBehavior injects the plugin-API failure modes the write paths must survive. The
// *AfterCommit flags emulate core failing after its store mutation committed (e.g. CreatePost's
// pending-post-id cache write), which is the case the committed-state probes exist for; the
// *BeforeCommit flags emulate a pre-persistence rejection (e.g. a MessageWillBePosted hook).
type commentAPIBehavior struct {
	failCreateBeforeSave bool
	failCreateAfterSave  bool
	failUpdateBeforeSave bool
	failUpdateAfterSave  bool
	failDeleteBeforeSave bool
	failDeleteAfterSave  bool

	// failMoveToChannel makes MovePostsToChannel error without moving anything;
	// dropMoveEffect makes it report success while moving nothing (the non-convergence case).
	failMoveToChannel bool
	dropMoveEffect    bool
}

type recordedEvent struct {
	name    string
	payload map[string]any
}

// commentHarness is the DB-backed service harness with the comment-post plugin API surface
// (CreatePost/GetPost/UpdatePost/DeletePost) emulated against the stand-in Posts table,
// reproducing the platform behaviors the comment paths rely on: PreSave keeps a caller-supplied
// id, UpdatePost replaces the prop map wholesale, and DeletePost cascades soft-delete to replies.
type commentHarness struct {
	*testHarness
	mockAPI  *plugintest.API
	behavior *commentAPIBehavior

	mu        sync.Mutex
	events    []recordedEvent
	moveCalls [][]string
}

// movePostsCalls returns the recorded MovePostsToChannel batches.
func (ch *commentHarness) movePostsCalls() [][]string {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return slices.Clone(ch.moveCalls)
}

func openCommentHarness(t *testing.T) *commentHarness {
	t.Helper()
	mockAPI := &plugintest.API{}
	ch := &commentHarness{mockAPI: mockAPI, behavior: &commentAPIBehavior{}}

	// The recorder must be registered before openTestServiceWithAPI's own generic
	// PublishWebSocketEvent stub: testify dispatches to the first matching expectation, so a
	// recorder registered after it would never fire. Tests assert on the recorded list rather
	// than on mock expectations, so both presence and absence are assertable.
	mockAPI.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		payload, _ := args.Get(1).(map[string]any)
		ch.events = append(ch.events, recordedEvent{name: args.String(0), payload: payload})
	}).Return().Maybe()

	ch.testHarness = openTestServiceWithAPI(t, mockAPI)
	ch.stubPostAPI(t)
	return ch
}

// eventsNamed returns the recorded WS events with the given name.
func (ch *commentHarness) eventsNamed(name string) []recordedEvent {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	var out []recordedEvent
	for _, ev := range ch.events {
		if ev.name == name {
			out = append(out, ev)
		}
	}
	return out
}

func (ch *commentHarness) stubPostAPI(t *testing.T) {
	t.Helper()

	ch.mockAPI.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(
		func(post *mmmodel.Post) (*mmmodel.Post, *mmmodel.AppError) {
			if ch.behavior.failCreateBeforeSave {
				return nil, mmmodel.NewAppError("CreatePost", "test.hook_rejection", nil, "", http.StatusBadRequest)
			}
			created := post.Clone()
			if created.Id == "" {
				created.Id = mmmodel.NewId()
			}
			now := mmmodel.GetMillis()
			created.CreateAt = now
			created.UpdateAt = now
			insertStandInPost(t, ch.db, created)
			if ch.behavior.failCreateAfterSave {
				return nil, mmmodel.NewAppError("CreatePost", "test.cache_write_failure", nil, "", http.StatusInternalServerError)
			}
			return created, nil
		}, nil).Maybe()

	ch.mockAPI.On("GetPost", mock.AnythingOfType("string")).Return(
		func(postID string) (*mmmodel.Post, *mmmodel.AppError) {
			post := readStandInPost(t, ch.db, postID)
			if post == nil || post.DeleteAt != 0 {
				return nil, mmmodel.NewAppError("GetPost", "test.post_not_found", nil, "", http.StatusNotFound)
			}
			return post, nil
		}, nil).Maybe()

	ch.mockAPI.On("UpdatePost", mock.AnythingOfType("*model.Post")).Return(
		func(post *mmmodel.Post) (*mmmodel.Post, *mmmodel.AppError) {
			if ch.behavior.failUpdateBeforeSave {
				return nil, mmmodel.NewAppError("UpdatePost", "test.hook_rejection", nil, "", http.StatusBadRequest)
			}
			old := readStandInPost(t, ch.db, post.Id)
			if old == nil || old.DeleteAt != 0 {
				return nil, mmmodel.NewAppError("UpdatePost", "test.post_not_found", nil, "", http.StatusNotFound)
			}
			updated := post.Clone()
			updated.UpdateAt = mmmodel.GetMillis()
			// EditAt moves only when the message changes, mirroring core's update path.
			updated.EditAt = old.EditAt
			if updated.Message != old.Message {
				updated.EditAt = updated.UpdateAt
			}
			// The superseded row is re-keyed and kept born soft-deleted, mirroring core's store
			// Update — listings and counts must never see it.
			hist := old.Clone()
			hist.OriginalId = old.Id
			hist.Id = mmmodel.NewId()
			hist.UpdateAt = updated.UpdateAt
			hist.DeleteAt = updated.UpdateAt
			insertStandInPost(t, ch.db, hist)
			props, err := json.Marshal(updated.GetProps())
			require.NoError(t, err)
			// The prop map is replaced wholesale, mirroring the SafeUpdate:false path the plugin
			// API forces — a service that sends a fresh map loses page_id here.
			_, err = ch.db.Exec(`UPDATE Posts SET UpdateAt = $1, EditAt = $2, Message = $3, Props = $4 WHERE Id = $5`,
				updated.UpdateAt, updated.EditAt, updated.Message, props, updated.Id)
			require.NoError(t, err)
			if ch.behavior.failUpdateAfterSave {
				return nil, mmmodel.NewAppError("UpdatePost", "test.publish_failure", nil, "", http.StatusInternalServerError)
			}
			return updated, nil
		}, nil).Maybe()

	// Emulates the core move primitive: the root's whole thread lands on the target channel in
	// one call, edit-history rows included via the OriginalId leg of the predicate.
	ch.mockAPI.On("MovePostsToChannel", mock.AnythingOfType("[]string"), mock.AnythingOfType("string")).Return(
		func(rootIDs []string, channelID string) *mmmodel.AppError {
			ch.mu.Lock()
			ch.moveCalls = append(ch.moveCalls, slices.Clone(rootIDs))
			ch.mu.Unlock()
			if ch.behavior.failMoveToChannel {
				return mmmodel.NewAppError("MovePostsToChannel", "test.move_failure", nil, "", http.StatusInternalServerError)
			}
			if ch.behavior.dropMoveEffect {
				return nil
			}
			_, err := ch.db.Exec(`UPDATE Posts SET ChannelId = $1 WHERE Id = ANY($2) OR RootId = ANY($2) OR OriginalId = ANY($2)`, channelID, pq.Array(rootIDs))
			require.NoError(t, err)
			return nil
		}).Maybe()

	ch.mockAPI.On("DeletePost", mock.AnythingOfType("string")).Return(
		func(postID string) *mmmodel.AppError {
			if ch.behavior.failDeleteBeforeSave {
				return mmmodel.NewAppError("DeletePost", "test.archived_channel", nil, "", http.StatusBadRequest)
			}
			// The platform cascade: one statement soft-deletes the post and its replies.
			_, err := ch.db.Exec(`UPDATE Posts SET DeleteAt = $1 WHERE (Id = $2 OR RootId = $2) AND DeleteAt = 0`,
				mmmodel.GetMillis(), postID)
			require.NoError(t, err)
			if ch.behavior.failDeleteAfterSave {
				return mmmodel.NewAppError("DeletePost", "test.cleanup_failure", nil, "", http.StatusInternalServerError)
			}
			return nil
		}).Maybe()
}

func insertStandInPost(t *testing.T, db *sql.DB, post *mmmodel.Post) {
	t.Helper()
	testutil.MustInsertPost(t, db, post)
}

// readStandInPost reads a Posts stand-in row back as a post, nil when absent.
func readStandInPost(t *testing.T, db *sql.DB, id string) *mmmodel.Post {
	t.Helper()
	var post mmmodel.Post
	var props []byte
	err := db.QueryRow(`SELECT Id, CreateAt, UpdateAt, EditAt, DeleteAt, UserId, ChannelId, RootId, OriginalId, Message, Type, COALESCE(Props::text, '{}')
		FROM Posts WHERE Id = $1`, id).
		Scan(&post.Id, &post.CreateAt, &post.UpdateAt, &post.EditAt, &post.DeleteAt, &post.UserId, &post.ChannelId, &post.RootId, &post.OriginalId, &post.Message, &post.Type, &props)
	if err == sql.ErrNoRows {
		return nil
	}
	require.NoError(t, err)
	var propMap mmmodel.StringInterface
	require.NoError(t, json.Unmarshal(props, &propMap))
	post.SetProps(propMap)
	return &post
}

// seedCommentFixture creates a space and a live page for comment tests.
func seedCommentFixture(t *testing.T, ch *commentHarness) (*model.Space, *model.Page) {
	t.Helper()
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, ch.store, channelID)
	page := mustCreatePage(t, ch.store, space.Id, channelID, mmmodel.NewId(), "")
	return space, page
}

// seedStoredComment inserts a comment row directly, bypassing the service, for read-path tests
// that need controlled CreateAt values.
func seedStoredComment(t *testing.T, ch *commentHarness, space *model.Space, pageID, rootID string, createAt int64, props mmmodel.StringInterface) *mmmodel.Post {
	t.Helper()
	if props == nil {
		props = mmmodel.StringInterface{model.PropKeyPageId: pageID}
	}
	post := &mmmodel.Post{
		Id:        mmmodel.NewId(),
		CreateAt:  createAt,
		UserId:    mmmodel.NewId(),
		ChannelId: space.ChannelId,
		RootId:    rootID,
		Message:   "seeded",
		Type:      model.PostTypePageComment,
	}
	post.SetProps(props)
	insertStandInPost(t, ch.db, post)
	return post
}

func TestServiceCreatePageComment(t *testing.T) {
	t.Run("footer comment persists with type and page_id prop", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		userID := mmmodel.NewId()

		comment, _, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "  first!  "}, userID)
		require.Nil(t, appErr)

		assert.Equal(t, space.Id, comment.SpaceId)
		assert.Equal(t, page.Id, comment.PageId)
		assert.Equal(t, userID, comment.UserId)
		assert.Equal(t, "first!", comment.Message)
		assert.Equal(t, model.CommentTypeFooter, comment.CommentType)
		assert.Empty(t, comment.RootId)
		assert.Zero(t, comment.ReplyCount)
		assert.False(t, comment.Resolved)

		stored := readStandInPost(t, ch.db, comment.Id)
		require.NotNil(t, stored)
		assert.Equal(t, model.PostTypePageComment, stored.Type)
		assert.Equal(t, space.ChannelId, stored.ChannelId)
		assert.Equal(t, page.Id, stored.GetProp(model.PropKeyPageId))

		events := ch.eventsNamed("page_comment_created")
		require.Len(t, events, 1)
		assert.Equal(t, map[string]any{
			"id":       comment.Id,
			"root_id":  "",
			"page_id":  page.Id,
			"space_id": space.Id,
		}, events[0].payload)
	})

	t.Run("inline comment persists both props and projects them", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)

		comment, _, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{
			Message:     "look here",
			CommentType: model.CommentTypeInline,
			AnchorId:    "anchor-42",
		}, mmmodel.NewId())
		require.Nil(t, appErr)
		assert.Equal(t, model.CommentTypeInline, comment.CommentType)
		assert.Equal(t, "anchor-42", comment.AnchorId)

		stored := readStandInPost(t, ch.db, comment.Id)
		assert.Equal(t, model.CommentTypeInline, stored.GetProp(model.PropKeyCommentType))
		assert.Equal(t, "anchor-42", stored.GetProp(model.PropKeyAnchorId))
	})

	t.Run("validation failures reject before core is called", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)

		for name, create := range map[string]*model.PageCommentCreate{
			"empty message":         {Message: "   "},
			"inline without anchor": {Message: "m", CommentType: model.CommentTypeInline},
			"footer with anchor":    {Message: "m", AnchorId: "a"},
			"unknown type":          {Message: "m", CommentType: "margin"},
		} {
			t.Run(name, func(t *testing.T) {
				_, _, appErr := ch.svc.CreatePageComment(space, page.Id, create, mmmodel.NewId())
				require.NotNil(t, appErr)
				assert.Equal(t, http.StatusBadRequest, appErr.StatusCode)
			})
		}
		ch.mockAPI.AssertNotCalled(t, "CreatePost", mock.Anything)
	})

	t.Run("a page in another space reads as not-found", func(t *testing.T) {
		ch := openCommentHarness(t)
		_, page := seedCommentFixture(t, ch)
		otherSpace := mustCreateSpace(t, ch.store, mmmodel.NewId())

		_, _, appErr := ch.svc.CreatePageComment(otherSpace, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
		ch.mockAPI.AssertNotCalled(t, "CreatePost", mock.Anything)
	})

	t.Run("a deleted page is not commentable", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		requireStoreDeletePage(t, ch.store, page.Id, space.Id, mmmodel.NewId())

		_, _, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
	})

	t.Run("a post-save failure is committed: the row exists at the allocated id and the event fires", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		ch.behavior.failCreateAfterSave = true

		_, committed, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
		assert.True(t, committed, "the audit trail must record the create that landed, not the error the caller saw")

		// The comment is durably created at the id the handler allocated, and the plugin event —
		// the only signal Docs clients get — was still published.
		var count int
		require.NoError(t, ch.db.QueryRow(`SELECT COUNT(*) FROM Posts WHERE ChannelId = $1`, space.ChannelId).Scan(&count))
		assert.Equal(t, 1, count)
		assert.Len(t, ch.eventsNamed("page_comment_created"), 1)
	})

	t.Run("a pre-save rejection is not committed: no row and no event", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		ch.behavior.failCreateBeforeSave = true

		_, committed, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.False(t, committed, "nothing was written, so the audit record must stay a failure")

		var count int
		require.NoError(t, ch.db.QueryRow(`SELECT COUNT(*) FROM Posts WHERE ChannelId = $1`, space.ChannelId).Scan(&count))
		assert.Zero(t, count)
		assert.Empty(t, ch.eventsNamed("page_comment_created"))
	})
}

func TestServiceCreatePageCommentReply(t *testing.T) {
	t.Run("reply carries page_id and RootId and inherits the inline root's anchor", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{
			Message: "root", CommentType: model.CommentTypeInline, AnchorId: "a1",
		}, mmmodel.NewId())
		require.Nil(t, appErr)

		reply, _, appErr := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "reply", mmmodel.NewId())
		require.Nil(t, appErr)
		assert.Equal(t, root.Id, reply.RootId)
		assert.Equal(t, page.Id, reply.PageId)
		// The reply inherits by projection; its stored row carries neither prop.
		assert.Equal(t, model.CommentTypeInline, reply.CommentType)
		assert.Equal(t, "a1", reply.AnchorId)
		stored := readStandInPost(t, ch.db, reply.Id)
		assert.Nil(t, stored.GetProp(model.PropKeyCommentType))
		assert.Nil(t, stored.GetProp(model.PropKeyAnchorId))
		assert.Equal(t, page.Id, stored.GetProp(model.PropKeyPageId))

		events := ch.eventsNamed("page_comment_created")
		require.Len(t, events, 2)
		assert.Equal(t, root.Id, events[1].payload["root_id"])
	})

	t.Run("a reply to a reply is rejected without calling core", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "root"}, mmmodel.NewId())
		reply, _, appErr := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "reply", mmmodel.NewId())
		require.Nil(t, appErr)

		createCalls := len(ch.mockAPI.Calls)
		_, _, appErr = ch.svc.CreatePageCommentReply(space, page.Id, reply.Id, "sub-reply", mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusBadRequest, appErr.StatusCode)
		assert.Equal(t, "app.page_comment.create_reply.parent_is_reply.app_error", appErr.Id)
		for _, call := range ch.mockAPI.Calls[createCalls:] {
			assert.NotEqual(t, "CreatePost", call.Method, "the sub-reply rejection must not reach core")
		}
	})

	t.Run("a parent on another page reads as not-found, matching DELETE and PATCH", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		otherPage := mustCreatePage(t, ch.store, space.Id, space.ChannelId, mmmodel.NewId(), "")
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "root"}, mmmodel.NewId())

		_, _, appErr := ch.svc.CreatePageCommentReply(space, otherPage.Id, root.Id, "reply", mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
	})
}

func TestServiceGetPageComments(t *testing.T) {
	t.Run("cursor walk survives ties and returns roots only", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		c1 := seedStoredComment(t, ch, space, page.Id, "", 1000, nil)
		c2 := seedStoredComment(t, ch, space, page.Id, "", 2000, nil)
		c3 := seedStoredComment(t, ch, space, page.Id, "", 3000, nil)
		seedStoredComment(t, ch, space, page.Id, c1.Id, 4000, nil) // reply: excluded

		first, next, hasMore, appErr := ch.svc.GetPageComments(space, page.Id, nil, "", nil, 2)
		require.Nil(t, appErr)
		require.True(t, hasMore)
		require.NotNil(t, next)
		assert.Equal(t, []string{c1.Id, c2.Id}, []string{first[0].Id, first[1].Id})

		second, next2, hasMore2, appErr := ch.svc.GetPageComments(space, page.Id, nil, "", next, 2)
		require.Nil(t, appErr)
		assert.False(t, hasMore2)
		assert.Nil(t, next2)
		require.Len(t, second, 1)
		assert.Equal(t, c3.Id, second[0].Id)
	})

	t.Run("reply counts are populated on the listing", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root := seedStoredComment(t, ch, space, page.Id, "", 1000, nil)
		seedStoredComment(t, ch, space, page.Id, root.Id, 2000, nil)
		seedStoredComment(t, ch, space, page.Id, root.Id, 3000, nil)

		got, _, _, appErr := ch.svc.GetPageComments(space, page.Id, nil, "", nil, 10)
		require.Nil(t, appErr)
		require.Len(t, got, 1)
		assert.Equal(t, 2, got[0].ReplyCount)
	})

	t.Run("filters compose and has_more is computed over the filtered set", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		// Two resolved-inline roots, then one unresolved footer root.
		for i := range 2 {
			seedStoredComment(t, ch, space, page.Id, "", int64(1000*(i+1)), mmmodel.StringInterface{
				model.PropKeyPageId:      page.Id,
				model.PropKeyCommentType: model.CommentTypeInline,
				model.PropKeyAnchorId:    "a",
				model.PropKeyResolved:    true,
			})
		}
		footer := seedStoredComment(t, ch, space, page.Id, "", 5000, nil)

		// A footer-filtered page must not report more pages just because resolved-inline rows
		// exist beyond it.
		resolvedFalse := false
		got, _, hasMore, appErr := ch.svc.GetPageComments(space, page.Id, &resolvedFalse, model.CommentTypeFooter, nil, 2)
		require.Nil(t, appErr)
		require.Len(t, got, 1)
		assert.Equal(t, footer.Id, got[0].Id)
		assert.False(t, hasMore)
	})

	t.Run("a page in another space reads as not-found", func(t *testing.T) {
		ch := openCommentHarness(t)
		_, page := seedCommentFixture(t, ch)
		otherSpace := mustCreateSpace(t, ch.store, mmmodel.NewId())

		_, _, _, appErr := ch.svc.GetPageComments(otherSpace, page.Id, nil, "", nil, 10)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
	})
}

func TestServiceGetPageComment(t *testing.T) {
	ch := openCommentHarness(t)
	space, page := seedCommentFixture(t, ch)
	root := seedStoredComment(t, ch, space, page.Id, "", 1000, mmmodel.StringInterface{
		model.PropKeyPageId:      page.Id,
		model.PropKeyCommentType: model.CommentTypeInline,
		model.PropKeyAnchorId:    "a1",
	})
	reply := seedStoredComment(t, ch, space, page.Id, root.Id, 2000, nil)

	t.Run("a root resolves with its computed reply count", func(t *testing.T) {
		got, appErr := ch.svc.GetPageComment(space, page.Id, root.Id)
		require.Nil(t, appErr)
		assert.Equal(t, 1, got.ReplyCount)
		assert.Equal(t, model.CommentTypeInline, got.CommentType)
	})

	t.Run("a reply resolves and inherits the root's thread identity", func(t *testing.T) {
		got, appErr := ch.svc.GetPageComment(space, page.Id, reply.Id)
		require.Nil(t, appErr)
		assert.Equal(t, root.Id, got.RootId)
		assert.Equal(t, model.CommentTypeInline, got.CommentType)
		assert.Equal(t, "a1", got.AnchorId)
		assert.Zero(t, got.ReplyCount)
	})

	t.Run("a comment on another page reads as not-found", func(t *testing.T) {
		otherPage := mustCreatePage(t, ch.store, space.Id, space.ChannelId, mmmodel.NewId(), "")
		_, appErr := ch.svc.GetPageComment(space, otherPage.Id, root.Id)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
	})

	t.Run("a reply whose root is gone reads as thread gone", func(t *testing.T) {
		orphanRoot := seedStoredComment(t, ch, space, page.Id, "", 3000, nil)
		orphanReply := seedStoredComment(t, ch, space, page.Id, orphanRoot.Id, 4000, nil)
		_, err := ch.db.Exec(`UPDATE Posts SET DeleteAt = 1 WHERE Id = $1`, orphanRoot.Id)
		require.NoError(t, err)

		_, appErr := ch.svc.GetPageComment(space, page.Id, orphanReply.Id)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
	})
}

func TestServiceGetPageCommentReplies(t *testing.T) {
	ch := openCommentHarness(t)
	space, page := seedCommentFixture(t, ch)
	root := seedStoredComment(t, ch, space, page.Id, "", 1000, nil)
	r1 := seedStoredComment(t, ch, space, page.Id, root.Id, 2000, nil)
	r2 := seedStoredComment(t, ch, space, page.Id, root.Id, 3000, nil)

	t.Run("returns the thread's replies in order", func(t *testing.T) {
		got, hasMore, appErr := ch.svc.GetPageCommentReplies(space, page.Id, root.Id, 0, 10)
		require.Nil(t, appErr)
		assert.False(t, hasMore)
		require.Len(t, got, 2)
		assert.Equal(t, []string{r1.Id, r2.Id}, []string{got[0].Id, got[1].Id})
	})

	t.Run("a reply-typed target is rejected 400, not an empty 200", func(t *testing.T) {
		_, _, appErr := ch.svc.GetPageCommentReplies(space, page.Id, r1.Id, 0, 10)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusBadRequest, appErr.StatusCode)
		assert.Equal(t, "app.page_comment.get_replies.target_is_reply.app_error", appErr.Id)
	})
}

func TestServiceUpdatePageComment(t *testing.T) {
	resolvedTrue, resolvedFalse := true, false

	t.Run("resolve preserves the page_id prop", func(t *testing.T) {
		// The single highest-value case in the epic: UpdatePost replaces the prop map wholesale,
		// so anything but read-modify-write permanently orphans the comment.
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, appErr := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		require.Nil(t, appErr)
		userID := mmmodel.NewId()

		updated, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, userID)
		require.Nil(t, appErr)
		assert.True(t, updated.Resolved)
		assert.Equal(t, userID, updated.ResolvedBy)
		assert.NotZero(t, updated.ResolvedAt)

		stored := readStandInPost(t, ch.db, root.Id)
		assert.Equal(t, page.Id, stored.GetProp(model.PropKeyPageId), "resolve must not destroy page_id")
		assert.Equal(t, true, stored.GetProp(model.PropKeyResolved))

		// The comment still lists: it was not orphaned.
		got, _, _, appErr := ch.svc.GetPageComments(space, page.Id, &resolvedTrue, "", nil, 10)
		require.Nil(t, appErr)
		require.Len(t, got, 1)

		events := ch.eventsNamed("page_comment_updated")
		require.Len(t, events, 1)
		assert.Equal(t, map[string]any{
			"id":       root.Id,
			"root_id":  "",
			"page_id":  page.Id,
			"space_id": space.Id,
		}, events[0].payload)
	})

	t.Run("unresolve records who reopened the thread", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		userA, userB := mmmodel.NewId(), mmmodel.NewId()

		_, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, userA)
		require.Nil(t, appErr)
		reopened, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedFalse}, userB)
		require.Nil(t, appErr)

		assert.False(t, reopened.Resolved)
		assert.Equal(t, userB, reopened.ResolvedBy, "attribution must cover the unresolve direction")
		assert.NotZero(t, reopened.ResolvedAt)
	})

	t.Run("an empty patch is rejected and does not unresolve", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		_, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, mmmodel.NewId())
		require.Nil(t, appErr)

		_, _, appErr = ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusBadRequest, appErr.StatusCode)

		got, appErr := ch.svc.GetPageComment(space, page.Id, root.Id)
		require.Nil(t, appErr)
		assert.True(t, got.Resolved, "an empty patch must not read as resolved: false")
	})

	t.Run("a reply target is rejected 400", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		reply, _, _ := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r", mmmodel.NewId())

		_, _, appErr := ch.svc.UpdatePageComment(space, page.Id, reply.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusBadRequest, appErr.StatusCode)
		assert.Equal(t, "app.page_comment.patch.target_is_reply.app_error", appErr.Id)
	})

	t.Run("the PATCH response carries the live reply count", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		for range 2 {
			_, _, appErr := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r", mmmodel.NewId())
			require.Nil(t, appErr)
		}

		updated, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, mmmodel.NewId())
		require.Nil(t, appErr)
		assert.Equal(t, 2, updated.ReplyCount)
	})

	t.Run("a post-commit failure still publishes the update event; a pre-commit one does not", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())

		ch.behavior.failUpdateAfterSave = true
		_, committed, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.True(t, committed, "the audit trail must record the patch that landed")
		assert.Len(t, ch.eventsNamed("page_comment_updated"), 1, "a committed write must announce itself")
		stored := readStandInPost(t, ch.db, root.Id)
		assert.Equal(t, true, stored.GetProp(model.PropKeyResolved))

		ch.behavior.failUpdateAfterSave = false
		ch.behavior.failUpdateBeforeSave = true
		_, committed, appErr = ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedFalse}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.False(t, committed, "nothing was written, so the audit record must stay a failure")
		assert.Len(t, ch.eventsNamed("page_comment_updated"), 1, "an uncommitted write must not announce anything")
	})

	t.Run("the author edits their own message; the edit is stamped and lists exactly once", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "before"}, author)
		message := "after"

		updated, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Message: &message}, author)
		require.Nil(t, appErr)
		assert.Equal(t, "after", updated.Message)
		assert.NotZero(t, updated.EditAt)
		assert.False(t, updated.Resolved, "a message edit must not touch resolve state")
		assert.Empty(t, updated.ResolvedBy)

		stored := readStandInPost(t, ch.db, root.Id)
		assert.Equal(t, page.Id, stored.GetProp(model.PropKeyPageId), "the edit must not destroy page_id")

		// The superseded version is a real Posts row now; the listing must still see one root.
		got, _, _, appErr := ch.svc.GetPageComments(space, page.Id, nil, "", nil, 10)
		require.Nil(t, appErr)
		require.Len(t, got, 1)
		assert.Equal(t, "after", got[0].Message)
		assert.NotZero(t, got[0].EditAt, "the listing must serve the edit stamp")
	})

	t.Run("a non-author message edit is refused 403 and changes nothing", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "before"}, mmmodel.NewId())
		message := "hijack"

		_, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Message: &message}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusForbidden, appErr.StatusCode)
		assert.Equal(t, "app.page_comment.patch.message_not_author.app_error", appErr.Id)
		assert.Equal(t, "before", readStandInPost(t, ch.db, root.Id).Message)
	})

	t.Run("the author edits their own reply and the response keeps the root's kind", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m", CommentType: model.CommentTypeInline, AnchorId: "a1"}, mmmodel.NewId())
		author := mmmodel.NewId()
		reply, _, _ := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "before", author)
		message := "after"

		updated, _, appErr := ch.svc.UpdatePageComment(space, page.Id, reply.Id, &model.PageCommentPatch{Message: &message}, author)
		require.Nil(t, appErr)
		assert.Equal(t, "after", updated.Message)
		assert.Equal(t, root.Id, updated.RootId)
		assert.Equal(t, model.CommentTypeInline, updated.CommentType)
		assert.Equal(t, "a1", updated.AnchorId)

		events := ch.eventsNamed("page_comment_updated")
		require.Len(t, events, 1)
		assert.Equal(t, root.Id, events[0].payload["root_id"], "the update event must carry the thread root")
	})

	t.Run("a mixed patch applies resolve and message together for the author", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "before"}, author)
		message := "after"

		updated, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue, Message: &message}, author)
		require.Nil(t, appErr)
		assert.Equal(t, "after", updated.Message)
		assert.True(t, updated.Resolved)
		assert.Equal(t, author, updated.ResolvedBy)
	})

	t.Run("a mixed patch from a non-author is refused whole", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "before"}, mmmodel.NewId())
		message := "hijack"

		_, _, appErr := ch.svc.UpdatePageComment(space, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue, Message: &message}, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusForbidden, appErr.StatusCode)

		got, appErr := ch.svc.GetPageComment(space, page.Id, root.Id)
		require.Nil(t, appErr)
		assert.False(t, got.Resolved, "no half of a refused patch may apply")
		assert.Equal(t, "before", got.Message)
	})
}

func TestServiceDeletePageComment(t *testing.T) {
	t.Run("own delete of a root with live replies is refused 409 with the count", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, author)
		reply1, _, _ := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r1", mmmodel.NewId())
		reply2, _, _ := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r2", mmmodel.NewId())

		replyCount, _, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusConflict, appErr.StatusCode)
		assert.Equal(t, "app.page_comment.delete.has_replies.app_error", appErr.Id)
		assert.Equal(t, 2, replyCount)
		ch.mockAPI.AssertNotCalled(t, "DeletePost", mock.Anything)

		// The refusal lifts once every reply is deleted (each reply author deletes their own).
		for _, reply := range []*model.PageComment{reply1, reply2} {
			_, _, appErr = ch.svc.DeletePageComment(space, page.Id, reply.Id, reply.UserId)
			require.Nil(t, appErr)
		}
		_, _, appErr = ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.Nil(t, appErr)
	})

	t.Run("a single live reply is enough to refuse the own delete", func(t *testing.T) {
		// The boundary the guard turns on: exactly one reply — the most common non-empty thread.
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, author)
		_, _, appErr := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r", mmmodel.NewId())
		require.Nil(t, appErr)

		replyCount, _, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusConflict, appErr.StatusCode)
		assert.Equal(t, 1, replyCount)
	})

	t.Run("an ordinary non-author cannot delete another member's thread", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())

		_, _, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusForbidden, appErr.StatusCode)
		assert.Zero(t, readStandInPost(t, ch.db, root.Id).DeleteAt)
	})

	t.Run("the space creator can moderate a thread and cascade its replies", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		moderator := mmmodel.NewId()
		space.CreatorId = moderator
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		reply, _, _ := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r", mmmodel.NewId())

		_, _, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, moderator)
		require.Nil(t, appErr)

		assert.NotZero(t, readStandInPost(t, ch.db, root.Id).DeleteAt)
		assert.NotZero(t, readStandInPost(t, ch.db, reply.Id).DeleteAt, "the platform cascade soft-deletes the replies")

		// One deleted event, for the root: the cascade never reports which replies it caught, so
		// no per-reply event can exist.
		events := ch.eventsNamed("page_comment_deleted")
		require.Len(t, events, 1)
		assert.Equal(t, root.Id, events[0].payload["id"])

		got, _, _, appErr := ch.svc.GetPageComments(space, page.Id, nil, "", nil, 10)
		require.Nil(t, appErr)
		assert.Empty(t, got)
	})

	t.Run("the space creator can moderate their own thread", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, space.CreatorId)
		reply, _, _ := ch.svc.CreatePageCommentReply(space, page.Id, root.Id, "r", mmmodel.NewId())

		_, _, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, space.CreatorId)
		require.Nil(t, appErr)
		assert.NotZero(t, readStandInPost(t, ch.db, root.Id).DeleteAt)
		assert.NotZero(t, readStandInPost(t, ch.db, reply.Id).DeleteAt)
	})

	t.Run("a second delete reads as not-found", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, author)
		_, _, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.Nil(t, appErr)

		_, _, appErr = ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
	})

	t.Run("a post-commit failure is committed: the row is deleted and the event fires", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, author)

		ch.behavior.failDeleteAfterSave = true
		_, committed, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
		assert.True(t, committed, "the audit trail must record the deletion that landed")
		assert.NotZero(t, readStandInPost(t, ch.db, root.Id).DeleteAt)
		assert.Len(t, ch.eventsNamed("page_comment_deleted"), 1)
	})

	t.Run("a pre-commit failure is not committed: the row is live and no event fires", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, author)

		ch.behavior.failDeleteBeforeSave = true
		_, committed, appErr := ch.svc.DeletePageComment(space, page.Id, root.Id, author)
		require.NotNil(t, appErr)
		assert.False(t, committed, "nothing was written, so the audit record must stay a failure")
		assert.Zero(t, readStandInPost(t, ch.db, root.Id).DeleteAt)
		assert.Empty(t, ch.eventsNamed("page_comment_deleted"))
	})

	t.Run("a comment id from another page cannot be deleted through this page's route", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		otherPage := mustCreatePage(t, ch.store, space.Id, space.ChannelId, mmmodel.NewId(), "")
		author := mmmodel.NewId()
		root, _, _ := ch.svc.CreatePageComment(space, page.Id, &model.PageCommentCreate{Message: "m"}, author)

		_, _, appErr := ch.svc.DeletePageComment(space, otherPage.Id, root.Id, author)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
		assert.Zero(t, readStandInPost(t, ch.db, root.Id).DeleteAt)
	})

	t.Run("an ordinary channel post cannot be deleted through the comment route", func(t *testing.T) {
		ch := openCommentHarness(t)
		space, page := seedCommentFixture(t, ch)
		chatPost := &mmmodel.Post{Id: mmmodel.NewId(), UserId: mmmodel.NewId(), ChannelId: space.ChannelId, Message: "chat"}
		insertStandInPost(t, ch.db, chatPost)

		_, _, appErr := ch.svc.DeletePageComment(space, page.Id, chatPost.Id, chatPost.UserId)
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusNotFound, appErr.StatusCode)
		assert.Zero(t, readStandInPost(t, ch.db, chatPost.Id).DeleteAt)
	})
}

func TestServiceMovePageToSpaceRehomesComments(t *testing.T) {
	seedTwoSpaces := func(t *testing.T, ch *commentHarness) (*model.Space, *model.Space, *model.Page) {
		t.Helper()
		source := mustCreateSpace(t, ch.store, mmmodel.NewId())
		target := mustCreateSpace(t, ch.store, mmmodel.NewId())
		// Same team: a cross-team move is rejected before any comment work.
		_, err := ch.db.Exec(`UPDATE DOCS_Space SET TeamId = $1 WHERE Id = $2`, source.TeamId, target.Id)
		require.NoError(t, err)
		target.TeamId = source.TeamId
		page := mustCreatePage(t, ch.store, source.Id, source.ChannelId, mmmodel.NewId(), "")
		return source, target, page
	}

	t.Run("a cross-space move re-homes the page's comment threads", func(t *testing.T) {
		ch := openCommentHarness(t)
		source, target, page := seedTwoSpaces(t, ch)
		root := seedStoredComment(t, ch, source, page.Id, "", 1000, nil)
		reply := seedStoredComment(t, ch, source, page.Id, root.Id, 2000, nil)
		otherPage := mustCreatePage(t, ch.store, source.Id, source.ChannelId, mmmodel.NewId(), "")
		bystander := seedStoredComment(t, ch, source, otherPage.Id, "", 3000, nil)

		_, appErr := ch.svc.MovePageToSpace(page.Id, source, target, nil, nil, true, mmmodel.NewId())
		require.Nil(t, appErr)

		assert.Equal(t, target.ChannelId, readStandInPost(t, ch.db, root.Id).ChannelId)
		assert.Equal(t, target.ChannelId, readStandInPost(t, ch.db, reply.Id).ChannelId)
		assert.Equal(t, source.ChannelId, readStandInPost(t, ch.db, bystander.Id).ChannelId,
			"a page staying behind keeps its comments")

		// Only roots are handed to the move; the thread moves with them.
		calls := ch.movePostsCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, []string{root.Id}, calls[0])

		// The re-homed thread serves from the target space.
		got, _, _, appErr := ch.svc.GetPageComments(target, page.Id, nil, "", nil, 10)
		require.Nil(t, appErr)
		require.Len(t, got, 1)
		assert.Equal(t, root.Id, got[0].Id)
	})

	t.Run("a resolved root's edit history does not poison the re-home", func(t *testing.T) {
		// Every UpdatePost re-keys the superseded row with RootId='' and OriginalId set — a shape
		// that passes a roots-only misplacement predicate while the move primitive rejects it as
		// non-root input, refusing the whole batch.
		ch := openCommentHarness(t)
		source, target, page := seedTwoSpaces(t, ch)
		resolvedTrue := true
		root, _, appErr := ch.svc.CreatePageComment(source, page.Id, &model.PageCommentCreate{Message: "m"}, mmmodel.NewId())
		require.Nil(t, appErr)
		_, _, appErr = ch.svc.UpdatePageComment(source, page.Id, root.Id, &model.PageCommentPatch{Resolved: &resolvedTrue}, mmmodel.NewId())
		require.Nil(t, appErr)

		_, appErr = ch.svc.MovePageToSpace(page.Id, source, target, nil, nil, true, mmmodel.NewId())
		require.Nil(t, appErr)

		calls := ch.movePostsCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, []string{root.Id}, calls[0], "only the root may be named; history rides its OriginalId leg")
		assert.Equal(t, target.ChannelId, readStandInPost(t, ch.db, root.Id).ChannelId)
	})

	t.Run("a failed re-home strands repairably, and a re-issued move repairs it", func(t *testing.T) {
		ch := openCommentHarness(t)
		source, target, page := seedTwoSpaces(t, ch)
		root := seedStoredComment(t, ch, source, page.Id, "", 1000, nil)

		ch.behavior.failMoveToChannel = true
		moved, appErr := ch.svc.MovePageToSpace(page.Id, source, target, nil, nil, true, mmmodel.NewId())
		require.Nil(t, appErr, "a committed page move must not fail because the comment re-home did")
		require.Equal(t, target.Id, moved.SpaceId)
		assert.Equal(t, source.ChannelId, readStandInPost(t, ch.db, root.Id).ChannelId, "the comment is stranded")

		// The straggler is detectable from its surviving page_id tie.
		misplaced, err := ch.store.GetMisplacedCommentRoots(target.Id, target.ChannelId, nil, 10)
		require.NoError(t, err)
		assert.Equal(t, []string{root.Id}, misplaced)

		// A same-space re-issue is the repair path and must sweep rather than no-op.
		ch.behavior.failMoveToChannel = false
		_, appErr = ch.svc.MovePageToSpace(page.Id, target, target, nil, nil, true, mmmodel.NewId())
		require.Nil(t, appErr)
		assert.Equal(t, target.ChannelId, readStandInPost(t, ch.db, root.Id).ChannelId)

		misplaced, err = ch.store.GetMisplacedCommentRoots(target.Id, target.ChannelId, nil, 10)
		require.NoError(t, err)
		assert.Empty(t, misplaced)
	})

	t.Run("the sweep drives the move a chunk at a time, not per root", func(t *testing.T) {
		ch := openCommentHarness(t)
		source, target, page := seedTwoSpaces(t, ch)
		for i := range 101 {
			seedStoredComment(t, ch, source, page.Id, "", int64(1000*(i+1)), nil)
		}

		_, appErr := ch.svc.MovePageToSpace(page.Id, source, target, nil, nil, true, mmmodel.NewId())
		require.Nil(t, appErr)

		calls := ch.movePostsCalls()
		require.Len(t, calls, 2, "101 roots at a chunk size of 100 is two calls")
		assert.Len(t, calls[0], 100)
		assert.Len(t, calls[1], 1)

		misplaced, err := ch.store.GetMisplacedCommentRoots(target.Id, target.ChannelId, nil, 10)
		require.NoError(t, err)
		assert.Empty(t, misplaced)
	})

	t.Run("a move that reports success without moving anything stops instead of looping", func(t *testing.T) {
		ch := openCommentHarness(t)
		source, target, page := seedTwoSpaces(t, ch)
		root := seedStoredComment(t, ch, source, page.Id, "", 1000, nil)

		ch.behavior.dropMoveEffect = true
		_, appErr := ch.svc.MovePageToSpace(page.Id, source, target, nil, nil, true, mmmodel.NewId())
		require.Nil(t, appErr, "the page move still succeeds; the sweep failure is logged")

		// The non-convergence guard fires after one fruitless move rather than spinning.
		assert.Len(t, ch.movePostsCalls(), 1)
		assert.Equal(t, source.ChannelId, readStandInPost(t, ch.db, root.Id).ChannelId)
	})
}

// TestServiceCreatePageCommentCursorRoundTrip pins that the app-level cursor from one window
// resumes the next without repeating or dropping a row even when CreateAt ties.
func TestServiceCreatePageCommentCursorRoundTrip(t *testing.T) {
	ch := openCommentHarness(t)
	space, page := seedCommentFixture(t, ch)
	ids := []string{"aaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccc"}
	for _, id := range ids {
		post := &mmmodel.Post{
			Id: id, CreateAt: 1000, UserId: mmmodel.NewId(), ChannelId: space.ChannelId,
			Message: "m", Type: model.PostTypePageComment,
		}
		post.SetProps(mmmodel.StringInterface{model.PropKeyPageId: page.Id})
		insertStandInPost(t, ch.db, post)
	}

	var walked []string
	var after *app.PageCommentCursor
	for {
		items, next, hasMore, appErr := ch.svc.GetPageComments(space, page.Id, nil, "", after, 1)
		require.Nil(t, appErr)
		for _, item := range items {
			walked = append(walked, item.Id)
		}
		if !hasMore {
			break
		}
		after = next
	}
	assert.Equal(t, ids, walked)
}
