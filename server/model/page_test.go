// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

func validPage() *model.Page {
	p := &model.Page{
		SpaceId:   mmmodel.NewId(),
		ChannelId: mmmodel.NewId(),
		UserId:    mmmodel.NewId(),
		Title:     "Title",
		Body:      "body",
	}
	p.PreSave()
	return p
}

func TestPageIsValid(t *testing.T) {
	t.Run("valid page passes", func(t *testing.T) {
		require.Nil(t, validPage().IsValid())
	})

	t.Run("invalid Id rejected", func(t *testing.T) {
		p := validPage()
		p.Id = "not-a-valid-id"
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.id.app_error", aerr.Id)
	})

	t.Run("invalid UserId rejected", func(t *testing.T) {
		p := validPage()
		p.UserId = "bad-id"
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.user_id.app_error", aerr.Id)
	})

	t.Run("non-empty invalid LastModifiedBy rejected", func(t *testing.T) {
		p := validPage()
		p.LastModifiedBy = "not-a-valid-id"
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.last_modified_by.app_error", aerr.Id)
	})

	t.Run("empty LastModifiedBy accepted", func(t *testing.T) {
		p := validPage()
		p.LastModifiedBy = ""
		require.Nil(t, p.IsValid())
	})

	t.Run("valid LastModifiedBy accepted", func(t *testing.T) {
		p := validPage()
		p.LastModifiedBy = mmmodel.NewId()
		require.Nil(t, p.IsValid())
	})

	t.Run("CreateAt zero rejected", func(t *testing.T) {
		p := validPage()
		p.CreateAt = 0
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.create_at.app_error", aerr.Id)
	})

	t.Run("UpdateAt zero rejected", func(t *testing.T) {
		p := validPage()
		p.UpdateAt = 0
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.update_at.app_error", aerr.Id)
	})

	t.Run("empty SpaceId rejected", func(t *testing.T) {
		p := validPage()
		p.SpaceId = ""
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.space_id.app_error", aerr.Id)
	})

	t.Run("empty ChannelId rejected", func(t *testing.T) {
		p := validPage()
		p.ChannelId = ""
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.channel_id.app_error", aerr.Id)
	})

	t.Run("empty title rejected", func(t *testing.T) {
		p := validPage()
		p.Title = ""
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.title.app_error", aerr.Id)
	})

	t.Run("whitespace-only title rejected", func(t *testing.T) {
		p := validPage()
		p.Title = "   "
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.title.app_error", aerr.Id)
	})

	t.Run("body over cap rejected", func(t *testing.T) {
		p := validPage()
		p.Body = strings.Repeat("x", model.PageBodyMaxBytes+1)
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.body.app_error", aerr.Id)
	})

	t.Run("title over cap rejected", func(t *testing.T) {
		p := validPage()
		p.Title = strings.Repeat("x", model.PageTitleMaxRunes+1)
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.title_length.app_error", aerr.Id)
	})

	t.Run("self parent rejected", func(t *testing.T) {
		p := validPage()
		p.ParentId = p.Id
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.parent_self.app_error", aerr.Id)
	})

	t.Run("invalid type rejected", func(t *testing.T) {
		p := validPage()
		p.Type = "not_a_real_type"
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.type.app_error", aerr.Id)
	})

	t.Run("page_folder type accepted", func(t *testing.T) {
		p := validPage()
		p.Type = model.PageTypeFolder
		require.Nil(t, p.IsValid())
	})

	t.Run("malformed OriginalId rejected", func(t *testing.T) {
		p := validPage()
		p.OriginalId = "not-an-id"
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.original_id.app_error", aerr.Id)
	})

	t.Run("search text over cap rejected", func(t *testing.T) {
		p := validPage()
		p.SearchText = strings.Repeat("x", model.PageSearchTextMaxBytes+1)
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.is_valid.search_text.app_error", aerr.Id)
	})

	t.Run("props over cap rejected", func(t *testing.T) {
		p := validPage()
		// Build a props map whose serialized size exceeds PagePropsMaxBytes.
		bigValue := strings.Repeat("x", model.PagePropsMaxBytes)
		p.Props = mmmodel.StringInterface{"k": bigValue}
		aerr := p.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.shared.props_too_large.app_error", aerr.Id)
	})
}

func TestPageGetProps(t *testing.T) {
	p := &model.Page{}
	require.NotNil(t, p.GetProps(), "GetProps must return a non-nil map even when Props is nil")
}

func TestPageSummaryJSONOmitsContentAndProps(t *testing.T) {
	summary := model.PageSummary{
		Id:             mmmodel.NewId(),
		SpaceId:        mmmodel.NewId(),
		ParentId:       mmmodel.NewId(),
		Type:           model.PageTypePage,
		Title:          "Summary",
		UserId:         mmmodel.NewId(),
		LastModifiedBy: mmmodel.NewId(),
		SortOrder:      1000,
		CreateAt:       1,
		UpdateAt:       2,
		EditAt:         3,
	}

	encoded, err := json.Marshal(summary)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.Contains(t, fields, "title")
	require.NotContains(t, fields, "body")
	require.NotContains(t, fields, "search_text")
	require.NotContains(t, fields, "props")
}

func TestPagePatch(t *testing.T) {
	base := func() *model.Page {
		return &model.Page{Title: "orig", Body: "origbody", SearchText: "origsearch"}
	}

	t.Run("nil fields leave values unchanged", func(t *testing.T) {
		p := base()
		p.Patch(&model.PagePatch{})
		require.Equal(t, "orig", p.Title)
		require.Equal(t, "origbody", p.Body)
		require.Equal(t, "origsearch", p.SearchText)
	})

	t.Run("non-nil fields are applied", func(t *testing.T) {
		p := base()
		p.Patch(&model.PagePatch{
			Title:      mmmodel.NewPointer("new"),
			Body:       mmmodel.NewPointer("newbody"),
			SearchText: mmmodel.NewPointer("newsearch"),
		})
		require.Equal(t, "new", p.Title)
		require.Equal(t, "newbody", p.Body)
		require.Equal(t, "newsearch", p.SearchText)
	})

	t.Run("empty string is a real value, not leave-unchanged", func(t *testing.T) {
		p := base()
		p.Patch(&model.PagePatch{Body: mmmodel.NewPointer("")})
		require.Equal(t, "", p.Body, "an explicit empty Body must clear the value")
		require.Equal(t, "orig", p.Title, "unset Title must remain unchanged")
	})
}

func TestPagePatchIsValid(t *testing.T) {
	t.Run("nil patch rejected", func(t *testing.T) {
		var patch *model.PagePatch
		aerr := patch.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.patch.nothing_to_update.app_error", aerr.Id)
	})

	t.Run("empty patch rejected", func(t *testing.T) {
		aerr := (&model.PagePatch{}).IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.patch.nothing_to_update.app_error", aerr.Id)
	})

	t.Run("body without search text rejected", func(t *testing.T) {
		aerr := (&model.PagePatch{Body: mmmodel.NewPointer("body")}).IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.patch.search_text_body_mismatch.app_error", aerr.Id)
	})

	t.Run("search text without body rejected", func(t *testing.T) {
		aerr := (&model.PagePatch{SearchText: mmmodel.NewPointer("search")}).IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.patch.search_text_body_mismatch.app_error", aerr.Id)
	})

	t.Run("non-empty search text with empty body rejected", func(t *testing.T) {
		aerr := (&model.PagePatch{
			Body:       mmmodel.NewPointer(""),
			SearchText: mmmodel.NewPointer("search"),
		}).IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.page.patch.search_text_without_content.app_error", aerr.Id)
	})

	t.Run("body and search text patched together accepted", func(t *testing.T) {
		aerr := (&model.PagePatch{
			Body:       mmmodel.NewPointer("body"),
			SearchText: mmmodel.NewPointer("search"),
		}).IsValid()
		require.Nil(t, aerr)
	})
}

// TestMaxDepthOfPages verifies depth computation does not depend on slice order: a
// children-before-parents ordering yields the same depth as pre-order.
func TestMaxDepthOfPages(t *testing.T) {
	rootID := mmmodel.NewId()
	childID := mmmodel.NewId()
	grandchildID := mmmodel.NewId()
	child := &model.Page{Id: childID, ParentId: rootID}
	grandchild := &model.Page{Id: grandchildID, ParentId: childID}

	t.Run("pre-order", func(t *testing.T) {
		require.Equal(t, 2, model.MaxDepthOfPages([]*model.Page{child, grandchild}, rootID))
	})

	t.Run("children before parents", func(t *testing.T) {
		require.Equal(t, 2, model.MaxDepthOfPages([]*model.Page{grandchild, child}, rootID))
	})

	t.Run("direct children only", func(t *testing.T) {
		other := &model.Page{Id: mmmodel.NewId(), ParentId: rootID}
		require.Equal(t, 1, model.MaxDepthOfPages([]*model.Page{child, other}, rootID))
	})

	t.Run("empty slice", func(t *testing.T) {
		require.Equal(t, 0, model.MaxDepthOfPages(nil, rootID))
	})
}
