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

func validDraft() *model.Draft {
	d := &model.Draft{
		UserId:  mmmodel.NewId(),
		SpaceId: mmmodel.NewId(),
		PageId:  mmmodel.NewId(),
		Title:   "Title",
		Body:    `{"type":"doc","content":[]}`,
	}
	d.PreSave()
	return d
}

func TestDraftIsValid(t *testing.T) {
	t.Run("valid draft passes", func(t *testing.T) {
		require.Nil(t, validDraft().IsValid())
	})

	t.Run("invalid UserId rejected", func(t *testing.T) {
		d := validDraft()
		d.UserId = "not-a-valid-id"
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.user_id.app_error", aerr.Id)
	})

	t.Run("invalid SpaceId rejected", func(t *testing.T) {
		d := validDraft()
		d.SpaceId = "bad"
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.space_id.app_error", aerr.Id)
	})

	t.Run("invalid PageId rejected", func(t *testing.T) {
		d := validDraft()
		d.PageId = "bad"
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.page_id.app_error", aerr.Id)
	})

	t.Run("non-empty invalid ParentId rejected", func(t *testing.T) {
		d := validDraft()
		d.ParentId = "bad"
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.parent_id.app_error", aerr.Id)
	})

	t.Run("empty ParentId accepted", func(t *testing.T) {
		d := validDraft()
		d.ParentId = ""
		require.Nil(t, d.IsValid())
	})

	t.Run("ParentId equal to PageId rejected", func(t *testing.T) {
		d := validDraft()
		d.ParentId = d.PageId
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.parent_self.app_error", aerr.Id)
	})

	t.Run("title over cap rejected", func(t *testing.T) {
		d := validDraft()
		d.Title = strings.Repeat("x", model.PageTitleMaxRunes+1)
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.title_length.app_error", aerr.Id)
	})

	t.Run("body over cap rejected", func(t *testing.T) {
		d := validDraft()
		d.Body = strings.Repeat("x", model.PageBodyMaxBytes+1)
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.body_size.app_error", aerr.Id)
	})

	t.Run("file ids over cap rejected", func(t *testing.T) {
		d := validDraft()
		ids := make(mmmodel.StringArray, 0, 20)
		for range 20 {
			ids = append(ids, mmmodel.NewId())
		}
		d.FileIds = ids
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.file_ids.app_error", aerr.Id)
	})

	t.Run("props over cap rejected", func(t *testing.T) {
		d := validDraft()
		d.Props = mmmodel.StringInterface{"k": strings.Repeat("x", model.PagePropsMaxBytes)}
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.shared.props_too_large.app_error", aerr.Id)
	})

	t.Run("CreateAt zero rejected", func(t *testing.T) {
		d := validDraft()
		d.CreateAt = 0
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.create_at.app_error", aerr.Id)
	})

	t.Run("UpdateAt zero rejected", func(t *testing.T) {
		d := validDraft()
		d.UpdateAt = 0
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.update_at.app_error", aerr.Id)
	})

	t.Run("negative BaseEditAt rejected", func(t *testing.T) {
		d := validDraft()
		d.BaseEditAt = -1
		aerr := d.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.draft.is_valid.base_edit_at.app_error", aerr.Id)
	})

	t.Run("zero BaseEditAt allowed", func(t *testing.T) {
		d := validDraft()
		d.BaseEditAt = 0
		require.Nil(t, d.IsValid())
	})
}

func TestDraftIsValidLastActiveAtZeroRejected(t *testing.T) {
	d := validDraft()
	d.LastActiveAt = 0
	aerr := d.IsValid()
	require.NotNil(t, aerr)
	require.Equal(t, "model.draft.is_valid.last_active_at.app_error", aerr.Id)
}

func TestDraftPreSaveDefaults(t *testing.T) {
	d := &model.Draft{UserId: mmmodel.NewId(), SpaceId: mmmodel.NewId(), PageId: mmmodel.NewId()}
	d.PreSave()
	require.NotZero(t, d.CreateAt)
	require.NotZero(t, d.UpdateAt)
	require.NotZero(t, d.LastActiveAt)
	require.NotNil(t, d.FileIds)
	require.NotNil(t, d.Props)
}

func TestDraftPreSavePreservesExistingCreateAt(t *testing.T) {
	d := &model.Draft{UserId: mmmodel.NewId(), SpaceId: mmmodel.NewId(), PageId: mmmodel.NewId(), CreateAt: 12345}
	d.PreSave()
	require.Equal(t, int64(12345), d.CreateAt, "PreSave must not overwrite an existing CreateAt")
}

func TestDraftPreSaveTrimsTitleWhitespace(t *testing.T) {
	d := &model.Draft{UserId: mmmodel.NewId(), SpaceId: mmmodel.NewId(), PageId: mmmodel.NewId(), Title: "  hello  "}
	d.PreSave()
	require.Equal(t, "hello", d.Title)
}

func TestDraftGetPropsNilReturnsEmpty(t *testing.T) {
	d := &model.Draft{Props: nil}
	require.NotNil(t, d.GetProps(), "GetProps must return an empty map, not nil")
	require.Empty(t, d.GetProps())
}

func TestDraftAuditable(t *testing.T) {
	d := validDraft()
	d.BaseEditAt = 42
	auditable := d.Auditable()
	require.Equal(t, int64(42), auditable["base_edit_at"])
}
