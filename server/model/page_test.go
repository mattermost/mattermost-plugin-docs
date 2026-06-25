// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model_test

import (
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
		require.Equal(t, "model.page.is_valid.props_size.app_error", aerr.Id)
	})
}

func TestPageGetProps(t *testing.T) {
	p := &model.Page{}
	require.NotNil(t, p.GetProps(), "GetProps must return a non-nil map even when Props is nil")
}

func TestPageCloneDeepCopy(t *testing.T) {
	p := validPage()
	p.Props = mmmodel.StringInterface{"k": "v"}
	reparent := "parent-id"
	p.ReparentedParentOnDelete = &reparent

	cp := p.Clone()
	require.Equal(t, p.Id, cp.Id)

	cp.Props["k"] = "changed"
	require.Equal(t, "v", p.Props["k"], "Props must be deep-copied")

	*cp.ReparentedParentOnDelete = "other"
	require.Equal(t, "parent-id", *p.ReparentedParentOnDelete, "pointer field must be deep-copied")
}

func TestPageCloneDeepCopyNested(t *testing.T) {
	p := validPage()
	p.Props = mmmodel.StringInterface{
		"nested": map[string]any{"inner": "v"},
		"list":   []any{"a", "b"},
	}

	cp := p.Clone()

	cp.Props["nested"].(map[string]any)["inner"] = "changed"
	require.Equal(t, "v", p.Props["nested"].(map[string]any)["inner"],
		"nested map inside Props must be deep-copied, not aliased")

	cp.Props["list"].([]any)[0] = "changed"
	require.Equal(t, "a", p.Props["list"].([]any)[0],
		"slice inside Props must be deep-copied, not aliased")
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
