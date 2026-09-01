// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// seedCommentPost inserts a comment Posts row for handler tests, with a controlled CreateAt so
// listings order deterministically.
func seedCommentPost(t *testing.T, h *apiTestHarness, channelID, pageID, rootID, userID string, createAt int64) *mmmodel.Post {
	t.Helper()
	post := &mmmodel.Post{
		Id:        mmmodel.NewId(),
		CreateAt:  createAt,
		UserId:    userID,
		ChannelId: channelID,
		RootId:    rootID,
		Message:   "seeded comment",
		Type:      model.PostTypePageComment,
	}
	post.SetProps(mmmodel.StringInterface{model.PropKeyPageId: pageID})
	return testutil.MustInsertPost(t, h.db, post)
}

// TestHandler_PageCommentsCursorRoundTrip walks the roots listing over HTTP: the next_after token
// from one response must resume the next window when passed back as the after param — the
// encode/decode pair is the wire contract clients depend on.
func TestHandler_PageCommentsCursorRoundTrip(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	userID := mmmodel.NewId()

	var seeded []string
	for i := range 3 {
		seeded = append(seeded, seedCommentPost(t, h, channelID, page.Id, "", mmmodel.NewId(), int64(1000*(i+1))).Id)
	}

	base := "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/comments"

	rec := h.do(t, http.MethodGet, base+"?per_page=2", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var first cursorResponse[model.PageComment]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	require.Len(t, first.Items, 2)
	assert.Equal(t, []string{seeded[0], seeded[1]}, []string{first.Items[0].Id, first.Items[1].Id})
	require.True(t, first.HasMore)
	require.NotNil(t, first.NextAfter)

	rec = h.do(t, http.MethodGet, base+"?per_page=2&after="+url.QueryEscape(*first.NextAfter), userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var second cursorResponse[model.PageComment]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &second))
	require.Len(t, second.Items, 1)
	assert.Equal(t, seeded[2], second.Items[0].Id)
	assert.False(t, second.HasMore)
	assert.Nil(t, second.NextAfter)
}

// TestHandler_PageCommentDeleteHasRepliesConflict pins the serialized 409 body: reply_count is a
// declared field beside error, and current_page keeps its key (as null) so the shipped
// publish-conflict client's key-presence check is unaffected.
func TestHandler_PageCommentDeleteHasRepliesConflict(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	author := mmmodel.NewId()

	root := seedCommentPost(t, h, channelID, page.Id, "", author, 1000)
	seedCommentPost(t, h, channelID, page.Id, root.Id, mmmodel.NewId(), 2000)
	seedCommentPost(t, h, channelID, page.Id, root.Id, mmmodel.NewId(), 3000)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/comments/"+root.Id, author, nil)
	require.Equal(t, http.StatusConflict, rec.Code)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Contains(t, body, "reply_count")
	var replyCount int
	require.NoError(t, json.Unmarshal(body["reply_count"], &replyCount))
	assert.Equal(t, 2, replyCount)
	require.Contains(t, body, "error")
	require.Contains(t, body, "current_page")
	assert.Equal(t, "null", string(body["current_page"]))
}

// TestHandler_PageCommentListParamValidation pins the 400 rejections for each malformed listing
// parameter.
func TestHandler_PageCommentListParamValidation(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/comments"

	cases := map[string]string{
		"?after=%21%21%21not-base64": "api.page_comment.invalid_cursor.app_error",
		"?after=dGVzdA":              "api.page_comment.invalid_cursor.app_error", // decodes, but has no separator
		"?resolved=maybe":            "api.page_comment.invalid_resolved_filter.app_error",
		"?comment_type=margin":       "api.page_comment.invalid_comment_type_filter.app_error",
	}
	for query, wantID := range cases {
		t.Run(query, func(t *testing.T) {
			rec := h.do(t, http.MethodGet, base+query, userID, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			var appErr mmmodel.AppError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
			assert.Equal(t, wantID, appErr.Id)
		})
	}

	// A well-formed cursor naming a row that does not exist is not an error: the window resumes
	// from the nearest greater row.
	cursor := encodeCommentCursor(&app.PageCommentCursor{CreateAt: 1, Id: mmmodel.NewId()})
	rec := h.do(t, http.MethodGet, base+"?after="+url.QueryEscape(*cursor), userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHandler_PageCommentReplyAndEditRoundTrip drives the reply POST and the message-edit PATCH
// over HTTP: decode, service wiring, and the serialized response — the reply inherits the inline
// root's thread identity on the wire, the edit stamps edit_at, and a foreign edit maps to 403.
func TestHandler_PageCommentReplyAndEditRoundTrip(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil)
	var h *apiTestHarness
	mockAPI.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(
		func(post *mmmodel.Post) (*mmmodel.Post, *mmmodel.AppError) {
			created := post.Clone()
			created.Id = mmmodel.NewId()
			now := mmmodel.GetMillis()
			created.CreateAt, created.UpdateAt = now, now
			testutil.MustInsertPost(t, h.db, created)
			return created, nil
		}, nil).Once()
	mockAPI.On("GetPost", mock.AnythingOfType("string")).Return(
		func(postID string) (*mmmodel.Post, *mmmodel.AppError) {
			post := readStandInCommentPost(t, h, postID)
			if post == nil {
				return nil, mmmodel.NewAppError("GetPost", "test.post_not_found", nil, "", http.StatusNotFound)
			}
			return post, nil
		}, nil).Maybe()
	mockAPI.On("UpdatePost", mock.AnythingOfType("*model.Post")).Return(
		func(post *mmmodel.Post) (*mmmodel.Post, *mmmodel.AppError) {
			updated := post.Clone()
			updated.UpdateAt = mmmodel.GetMillis()
			updated.EditAt = updated.UpdateAt
			props, err := json.Marshal(updated.GetProps())
			require.NoError(t, err)
			_, err = h.db.Exec(`UPDATE Posts SET UpdateAt = $1, EditAt = $2, Message = $3, Props = $4 WHERE Id = $5`,
				updated.UpdateAt, updated.EditAt, updated.Message, props, updated.Id)
			require.NoError(t, err)
			return updated, nil
		}, nil).Once()
	h = openTestPlugin(t, mockAPI)

	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	rootAuthor, replyAuthor := mmmodel.NewId(), mmmodel.NewId()

	root := seedCommentPost(t, h, channelID, page.Id, "", rootAuthor, 1000)
	root.AddProp(model.PropKeyCommentType, model.CommentTypeInline)
	root.AddProp(model.PropKeyAnchorId, "anchor-1")
	props, err := json.Marshal(root.GetProps())
	require.NoError(t, err)
	_, err = h.db.Exec(`UPDATE Posts SET Props = $1 WHERE Id = $2`, props, root.Id)
	require.NoError(t, err)

	base := "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/comments/" + root.Id

	// # Reply over HTTP
	rec := h.do(t, http.MethodPost, base+"/replies", replyAuthor, map[string]any{"message": "a reply"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var reply model.PageComment
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reply))
	assert.Equal(t, root.Id, reply.RootId)
	assert.Equal(t, model.CommentTypeInline, reply.CommentType, "the wire response must inherit the root's kind")
	assert.Equal(t, "anchor-1", reply.AnchorId)
	assert.Equal(t, replyAuthor, reply.UserId)
	assert.Zero(t, reply.EditAt)

	replyPath := "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/comments/" + reply.Id

	// # A foreign edit is refused before any write
	rec = h.do(t, http.MethodPatch, replyPath, rootAuthor, map[string]any{"message": "hijack"})
	require.Equal(t, http.StatusForbidden, rec.Code)

	// # The author edits their own reply
	rec = h.do(t, http.MethodPatch, replyPath, replyAuthor, map[string]any{"message": "an edited reply"})
	require.Equal(t, http.StatusOK, rec.Code)
	var edited model.PageComment
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &edited))
	assert.Equal(t, "an edited reply", edited.Message)
	assert.Positive(t, edited.EditAt, "the serialized response must carry edit_at")
	assert.Equal(t, root.Id, edited.RootId)
	assert.Equal(t, model.CommentTypeInline, edited.CommentType)
	assert.False(t, edited.Resolved, "a message edit must not touch resolve state")
}

// readStandInCommentPost reads a Posts stand-in row back for the handler-level post-API emulation.
func readStandInCommentPost(t *testing.T, h *apiTestHarness, id string) *mmmodel.Post {
	t.Helper()
	var post mmmodel.Post
	var props []byte
	err := h.db.QueryRow(`SELECT Id, CreateAt, UpdateAt, EditAt, DeleteAt, UserId, ChannelId, RootId, Message, Type, COALESCE(Props::text, '{}')
		FROM Posts WHERE Id = $1 AND DeleteAt = 0`, id).
		Scan(&post.Id, &post.CreateAt, &post.UpdateAt, &post.EditAt, &post.DeleteAt, &post.UserId, &post.ChannelId, &post.RootId, &post.Message, &post.Type, &props)
	if err != nil {
		return nil
	}
	var propMap mmmodel.StringInterface
	require.NoError(t, json.Unmarshal(props, &propMap))
	post.SetProps(propMap)
	return &post
}

// TestHandler_PageCommentCounts pins the counts route, including the one thing route order
// decides: "counts" must not be captured as a comment id by the sibling {comment_id} route.
func TestHandler_PageCommentCounts(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	userID := mmmodel.NewId()
	path := "/api/v1/spaces/" + space.Id + "/pages/" + page.Id + "/comments/counts"

	rec := h.do(t, http.MethodGet, path, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, "counts must not be routed as a comment id")
	var empty model.PageCommentCounts
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &empty))
	assert.Equal(t, model.PageCommentCounts{}, empty)

	root := seedCommentPost(t, h, channelID, page.Id, "", mmmodel.NewId(), 1000)
	seedCommentPost(t, h, channelID, page.Id, root.Id, mmmodel.NewId(), 2000)
	seedCommentPost(t, h, channelID, page.Id, "", mmmodel.NewId(), 3000)

	rec = h.do(t, http.MethodGet, path, userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var counts model.PageCommentCounts
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &counts))
	assert.Equal(t, model.PageCommentCounts{Total: 2, Open: 2, Resolved: 0}, counts,
		"two roots; the reply is part of a thread, not a thread of its own")
}
