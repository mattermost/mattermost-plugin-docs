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
		ChannelId: mmmodel.NewId(),
		TeamId:    mmmodel.NewId(),
		CreatorId: mmmodel.NewId(),
		Title:     "Title",
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
}

func TestSpacePreSaveDefaultsSortOrder(t *testing.T) {
	s := &model.Space{ChannelId: mmmodel.NewId(), Title: "T"}
	s.PreSave()
	require.NotZero(t, s.CreateAt)
	require.Equal(t, s.CreateAt, s.SortOrder)
}

func TestSpaceCloneDeepCopy(t *testing.T) {
	s := validSpace()
	s.Props = mmmodel.StringInterface{"key": "original"}

	cp := s.Clone()
	require.Equal(t, s.Id, cp.Id)
	require.Equal(t, s.Title, cp.Title)

	// Mutating the clone's Props must not affect the original.
	cp.Props["key"] = "mutated"
	require.Equal(t, "original", s.Props["key"], "Clone must deep-copy Props")

	// Adding a new key to the clone must not appear in the original.
	cp.Props["new"] = "extra"
	_, exists := s.Props["new"]
	require.False(t, exists, "new key added to clone must not appear in original")
}

func TestSpaceCloneNilProps(t *testing.T) {
	s := validSpace()
	s.Props = nil

	cp := s.Clone()
	require.Equal(t, s.Id, cp.Id)
	// Clone with nil Props must not panic, and the clone's Props remain nil.
	require.Nil(t, cp.Props)
}
