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

func validSpace() *model.Space {
	s := &model.Space{
		ChannelId:  mmmodel.NewId(),
		TeamId:     mmmodel.NewId(),
		CreatorId:  mmmodel.NewId(),
		Title:      "Title",
		ViewAccess: model.ViewAccessOpen,
	}
	s.PreSave()
	return s
}

func TestSpaceIsValid(t *testing.T) {
	t.Run("valid space passes", func(t *testing.T) {
		require.Nil(t, validSpace().IsValid())
	})

	t.Run("invalid Id rejected", func(t *testing.T) {
		s := validSpace()
		s.Id = "not-a-valid-id"
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.id.app_error", aerr.Id)
	})

	t.Run("CreateAt zero rejected", func(t *testing.T) {
		s := validSpace()
		s.CreateAt = 0
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.create_at.app_error", aerr.Id)
	})

	t.Run("UpdateAt zero rejected", func(t *testing.T) {
		s := validSpace()
		s.UpdateAt = 0
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.update_at.app_error", aerr.Id)
	})

	t.Run("empty ChannelId rejected", func(t *testing.T) {
		s := validSpace()
		s.ChannelId = ""
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.channel_id.app_error", aerr.Id)
	})

	t.Run("non-empty invalid TeamId rejected", func(t *testing.T) {
		s := validSpace()
		s.TeamId = "bad-id"
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.team_id.app_error", aerr.Id)
	})

	t.Run("empty TeamId accepted", func(t *testing.T) {
		s := validSpace()
		s.TeamId = ""
		require.Nil(t, s.IsValid())
	})

	t.Run("non-empty invalid CreatorId rejected", func(t *testing.T) {
		s := validSpace()
		s.CreatorId = "bad-id"
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.creator_id.app_error", aerr.Id)
	})

	t.Run("empty CreatorId accepted", func(t *testing.T) {
		s := validSpace()
		s.CreatorId = ""
		require.Nil(t, s.IsValid())
	})

	t.Run("empty title rejected", func(t *testing.T) {
		s := validSpace()
		s.Title = "   "
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.title.app_error", aerr.Id)
	})

	t.Run("title over cap rejected", func(t *testing.T) {
		s := validSpace()
		s.Title = strings.Repeat("x", model.SpaceTitleMaxRunes+1)
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.title_length.app_error", aerr.Id)
	})

	t.Run("description over cap rejected", func(t *testing.T) {
		s := validSpace()
		s.Description = strings.Repeat("x", model.SpaceDescriptionMaxRunes+1)
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.description_length.app_error", aerr.Id)
	})

	t.Run("icon over cap rejected", func(t *testing.T) {
		s := validSpace()
		s.Icon = strings.Repeat("x", model.SpaceIconMaxBytes+1)
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.icon_length.app_error", aerr.Id)
	})

	t.Run("props over cap rejected", func(t *testing.T) {
		s := validSpace()
		bigValue := strings.Repeat("x", model.SpacePropsMaxBytes)
		s.Props = mmmodel.StringInterface{"k": bigValue}
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.shared.props_too_large.app_error", aerr.Id)
	})

	t.Run("empty ViewAccess rejected", func(t *testing.T) {
		s := validSpace()
		s.ViewAccess = ""
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.view_access.app_error", aerr.Id)
	})

	t.Run("unknown ViewAccess rejected", func(t *testing.T) {
		s := validSpace()
		s.ViewAccess = "public"
		aerr := s.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.is_valid.view_access.app_error", aerr.Id)
	})

	t.Run("private ViewAccess accepted", func(t *testing.T) {
		s := validSpace()
		s.ViewAccess = model.ViewAccessPrivate
		require.Nil(t, s.IsValid())
	})
}

func TestSpacePatchIsValid(t *testing.T) {
	t.Run("nil patch rejected", func(t *testing.T) {
		var patch *model.SpacePatch
		aerr := patch.IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.patch.nothing_to_update.app_error", aerr.Id)
	})

	t.Run("empty patch rejected", func(t *testing.T) {
		aerr := (&model.SpacePatch{}).IsValid()
		require.NotNil(t, aerr)
		require.Equal(t, "model.space.patch.nothing_to_update.app_error", aerr.Id)
	})

	t.Run("Title only accepted", func(t *testing.T) {
		aerr := (&model.SpacePatch{Title: mmmodel.NewPointer("new title")}).IsValid()
		require.Nil(t, aerr)
	})

	t.Run("Description only accepted", func(t *testing.T) {
		aerr := (&model.SpacePatch{Description: mmmodel.NewPointer("desc")}).IsValid()
		require.Nil(t, aerr)
	})

	t.Run("Icon only accepted", func(t *testing.T) {
		aerr := (&model.SpacePatch{Icon: mmmodel.NewPointer("icon")}).IsValid()
		require.Nil(t, aerr)
	})

	t.Run("Props only accepted", func(t *testing.T) {
		props := mmmodel.StringInterface{"k": "v"}
		aerr := (&model.SpacePatch{Props: &props}).IsValid()
		require.Nil(t, aerr)
	})

	t.Run("ViewAccess only accepted", func(t *testing.T) {
		aerr := (&model.SpacePatch{ViewAccess: mmmodel.NewPointer(model.ViewAccessPrivate)}).IsValid()
		require.Nil(t, aerr)
	})
}

func TestSpacePatch(t *testing.T) {
	base := func() *model.Space {
		return &model.Space{Title: "orig", Description: "origdesc", Icon: "origicon"}
	}

	t.Run("nil fields leave values unchanged", func(t *testing.T) {
		s := base()
		s.Patch(&model.SpacePatch{})
		require.Equal(t, "orig", s.Title)
		require.Equal(t, "origdesc", s.Description)
		require.Equal(t, "origicon", s.Icon)
	})

	t.Run("non-nil fields are applied", func(t *testing.T) {
		s := base()
		s.Patch(&model.SpacePatch{
			Title:       mmmodel.NewPointer("new"),
			Description: mmmodel.NewPointer("newdesc"),
			Icon:        mmmodel.NewPointer("newicon"),
		})
		require.Equal(t, "new", s.Title)
		require.Equal(t, "newdesc", s.Description)
		require.Equal(t, "newicon", s.Icon)
	})

	t.Run("empty string is a real value, not leave-unchanged", func(t *testing.T) {
		s := base()
		s.Patch(&model.SpacePatch{Description: mmmodel.NewPointer("")})
		require.Equal(t, "", s.Description)
		require.Equal(t, "orig", s.Title)
	})

	t.Run("Props are applied as a clone", func(t *testing.T) {
		s := base()
		props := mmmodel.StringInterface{"k": "v"}
		s.Patch(&model.SpacePatch{Props: &props})
		require.Equal(t, mmmodel.StringInterface{"k": "v"}, s.Props)
		// Mutating the original map must not affect the space's Props.
		props["k"] = "mutated"
		require.Equal(t, "v", s.Props["k"])
	})

	t.Run("nil patch is a no-op", func(t *testing.T) {
		s := base()
		s.Patch(nil)
		require.Equal(t, "orig", s.Title)
	})

	t.Run("ViewAccess is applied", func(t *testing.T) {
		s := base()
		s.ViewAccess = model.ViewAccessOpen
		s.Patch(&model.SpacePatch{ViewAccess: mmmodel.NewPointer(model.ViewAccessPrivate)})
		require.Equal(t, model.ViewAccessPrivate, s.ViewAccess)
		// A ViewAccess-only patch must leave every other field alone.
		require.Equal(t, "orig", s.Title)
	})
}

func TestSpacePreSaveDefaultsSortOrder(t *testing.T) {
	s := &model.Space{ChannelId: mmmodel.NewId(), Title: "T"}
	s.PreSave()
	require.NotZero(t, s.CreateAt)
	require.Equal(t, s.CreateAt, s.SortOrder)
}
