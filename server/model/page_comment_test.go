// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

func newCommentPost(id, rootID string, props mmmodel.StringInterface) *mmmodel.Post {
	post := &mmmodel.Post{
		Id:        id,
		RootId:    rootID,
		UserId:    mmmodel.NewId(),
		ChannelId: mmmodel.NewId(),
		Message:   "hello",
		Type:      PostTypePageComment,
		CreateAt:  100,
		UpdateAt:  200,
	}
	post.SetProps(props)
	return post
}

func TestPageCommentCreateIsValid(t *testing.T) {
	valid := func(c PageCommentCreate) *mmmodel.AppError {
		c.Normalize()
		return c.IsValid()
	}

	t.Run("footer with message is valid", func(t *testing.T) {
		require.Nil(t, valid(PageCommentCreate{Message: "hi"}))
		require.Nil(t, valid(PageCommentCreate{Message: "hi", CommentType: CommentTypeFooter}))
	})

	t.Run("empty and whitespace-only messages are rejected", func(t *testing.T) {
		for _, msg := range []string{"", "   ", "\n\t "} {
			appErr := valid(PageCommentCreate{Message: msg})
			require.NotNil(t, appErr)
			assert.Equal(t, "model.page_comment.create.message_required.app_error", appErr.Id)
		}
	})

	t.Run("message at the post-length limit is accepted, one over is rejected", func(t *testing.T) {
		require.Nil(t, valid(PageCommentCreate{Message: strings.Repeat("x", mmmodel.PostMessageMaxRunesV2)}))

		appErr := valid(PageCommentCreate{Message: strings.Repeat("x", mmmodel.PostMessageMaxRunesV2+1)})
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.create.message_too_long.app_error", appErr.Id)
	})

	t.Run("inline requires an anchor", func(t *testing.T) {
		appErr := valid(PageCommentCreate{Message: "hi", CommentType: CommentTypeInline})
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.create.anchor_required.app_error", appErr.Id)

		appErr = valid(PageCommentCreate{Message: "hi", CommentType: CommentTypeInline, AnchorId: "   "})
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.create.anchor_required.app_error", appErr.Id)
	})

	t.Run("footer with an anchor is rejected with its own id", func(t *testing.T) {
		for _, ct := range []string{"", CommentTypeFooter} {
			appErr := valid(PageCommentCreate{Message: "hi", CommentType: ct, AnchorId: "a1"})
			require.NotNil(t, appErr)
			assert.Equal(t, "model.page_comment.create.anchor_not_allowed.app_error", appErr.Id)
		}
	})

	t.Run("anchor at the bound is accepted, one over is rejected", func(t *testing.T) {
		atBound := strings.Repeat("a", MaxAnchorIdRunes)
		require.Nil(t, valid(PageCommentCreate{Message: "hi", CommentType: CommentTypeInline, AnchorId: atBound}))

		appErr := valid(PageCommentCreate{Message: "hi", CommentType: CommentTypeInline, AnchorId: atBound + "a"})
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.create.anchor_too_long.app_error", appErr.Id)
	})

	t.Run("unknown comment_type is rejected", func(t *testing.T) {
		appErr := valid(PageCommentCreate{Message: "hi", CommentType: "margin"})
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.create.invalid_comment_type.app_error", appErr.Id)
	})
}

func TestPageCommentPatchIsValid(t *testing.T) {
	t.Run("nil and empty patches are rejected", func(t *testing.T) {
		var nilPatch *PageCommentPatch
		appErr := nilPatch.IsValid()
		require.NotNil(t, appErr)
		assert.Equal(t, http.StatusBadRequest, appErr.StatusCode)

		appErr = (&PageCommentPatch{}).IsValid()
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.patch.nothing_to_update.app_error", appErr.Id)
	})

	t.Run("explicit false is valid", func(t *testing.T) {
		resolved := false
		require.Nil(t, (&PageCommentPatch{Resolved: &resolved}).IsValid())
	})

	t.Run("a message-only patch is valid and normalized", func(t *testing.T) {
		message := "  edited  "
		patch := &PageCommentPatch{Message: &message}
		patch.Normalize()
		require.Nil(t, patch.IsValid())
		assert.Equal(t, "edited", *patch.Message)
	})

	t.Run("a whitespace-only message is rejected after normalize", func(t *testing.T) {
		message := "   "
		patch := &PageCommentPatch{Message: &message}
		patch.Normalize()
		appErr := patch.IsValid()
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.patch.message_required.app_error", appErr.Id)
	})

	t.Run("an over-long message is rejected", func(t *testing.T) {
		message := strings.Repeat("x", mmmodel.PostMessageMaxRunesV2+1)
		appErr := (&PageCommentPatch{Message: &message}).IsValid()
		require.NotNil(t, appErr)
		assert.Equal(t, "model.page_comment.patch.message_too_long.app_error", appErr.Id)
	})
}

func TestNewPageCommentFromPost(t *testing.T) {
	spaceID := mmmodel.NewId()
	pageID := mmmodel.NewId()

	t.Run("root with no comment_type prop projects as footer", func(t *testing.T) {
		post := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{PropKeyPageId: pageID})
		c := NewPageCommentFromPost(post, nil, spaceID, 3)

		assert.Equal(t, post.Id, c.Id)
		assert.Equal(t, spaceID, c.SpaceId)
		assert.Equal(t, pageID, c.PageId)
		assert.Equal(t, CommentTypeFooter, c.CommentType)
		assert.Empty(t, c.AnchorId)
		assert.Equal(t, 3, c.ReplyCount)
		assert.False(t, c.Resolved)
	})

	t.Run("inline root carries its anchor", func(t *testing.T) {
		post := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{
			PropKeyPageId:      pageID,
			PropKeyCommentType: CommentTypeInline,
			PropKeyAnchorId:    "anchor-1",
		})
		c := NewPageCommentFromPost(post, nil, spaceID, 0)

		assert.Equal(t, CommentTypeInline, c.CommentType)
		assert.Equal(t, "anchor-1", c.AnchorId)
	})

	t.Run("reply inherits the root's comment_type and anchor, not the absence rule", func(t *testing.T) {
		root := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{
			PropKeyPageId:      pageID,
			PropKeyCommentType: CommentTypeInline,
			PropKeyAnchorId:    "anchor-1",
		})
		reply := newCommentPost(mmmodel.NewId(), root.Id, mmmodel.StringInterface{PropKeyPageId: pageID})
		c := NewPageCommentFromPost(reply, root, spaceID, 0)

		assert.Equal(t, CommentTypeInline, c.CommentType)
		assert.Equal(t, "anchor-1", c.AnchorId)
		assert.Equal(t, root.Id, c.RootId)
	})

	t.Run("reply in a footer thread carries neither field", func(t *testing.T) {
		root := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{PropKeyPageId: pageID})
		reply := newCommentPost(mmmodel.NewId(), root.Id, mmmodel.StringInterface{PropKeyPageId: pageID})
		c := NewPageCommentFromPost(reply, root, spaceID, 0)

		assert.Equal(t, CommentTypeFooter, c.CommentType)
		assert.Empty(t, c.AnchorId)
	})

	t.Run("resolve state coerces across JSON encodings", func(t *testing.T) {
		userID := mmmodel.NewId()
		for name, props := range map[string]mmmodel.StringInterface{
			"native types": {PropKeyPageId: pageID, PropKeyResolved: true, PropKeyResolvedBy: userID, PropKeyResolvedAt: int64(123)},
			"round-trip":   {PropKeyPageId: pageID, PropKeyResolved: "true", PropKeyResolvedBy: userID, PropKeyResolvedAt: float64(123)},
		} {
			t.Run(name, func(t *testing.T) {
				post := newCommentPost(mmmodel.NewId(), "", props)
				c := NewPageCommentFromPost(post, nil, spaceID, 0)
				assert.True(t, c.Resolved)
				assert.Equal(t, userID, c.ResolvedBy)
				assert.EqualValues(t, 123, c.ResolvedAt)
			})
		}
	})

	t.Run("attribution is both-or-neither", func(t *testing.T) {
		post := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{
			PropKeyPageId:     pageID,
			PropKeyResolvedBy: mmmodel.NewId(),
		})
		c := NewPageCommentFromPost(post, nil, spaceID, 0)
		assert.Empty(t, c.ResolvedBy)
		assert.Zero(t, c.ResolvedAt)
	})

	t.Run("a never-resolved comment omits the attribution keys entirely", func(t *testing.T) {
		post := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{PropKeyPageId: pageID})
		body, err := json.Marshal(NewPageCommentFromPost(post, nil, spaceID, 0))
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(body, &decoded))
		assert.NotContains(t, decoded, "resolved_by")
		assert.NotContains(t, decoded, "resolved_at")
		assert.Equal(t, false, decoded["resolved"])
		// comment_type is not omitempty: footer is a real value a client must be able to
		// distinguish from a field the server declined to send.
		assert.Equal(t, CommentTypeFooter, decoded["comment_type"])
		assert.NotContains(t, decoded, "anchor_id")
	})

	t.Run("an oversized stored anchor is clamped on read", func(t *testing.T) {
		post := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{
			PropKeyPageId:      pageID,
			PropKeyCommentType: CommentTypeInline,
			PropKeyAnchorId:    strings.Repeat("a", MaxAnchorIdRunes*2),
		})
		c := NewPageCommentFromPost(post, nil, spaceID, 0)
		assert.Len(t, c.AnchorId, MaxAnchorIdRunes)
	})

	t.Run("auditable excludes the message", func(t *testing.T) {
		post := newCommentPost(mmmodel.NewId(), "", mmmodel.StringInterface{PropKeyPageId: pageID})
		audit := NewPageCommentFromPost(post, nil, spaceID, 0).Auditable()
		assert.NotContains(t, audit, "message")
		assert.Equal(t, post.Id, audit["id"])
	})
}
