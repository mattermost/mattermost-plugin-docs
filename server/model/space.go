// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"net/http"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

const (
	SpaceTitleMaxRunes       = 128
	SpaceDescriptionMaxRunes = 1024
	SpaceIconMaxBytes        = 256

	// SpacePropsMaxBytes caps the serialized size of the opaque Props map.
	SpacePropsMaxBytes = 64 * 1024
)

// Space is stored in the DOCS_Space table. Each space owns a backing MM channel (ChannelId).
// A soft-deleted space (DeleteAt>0) retains its pages; pages share the same DeleteAt via a
// cascade and can be restored with the space.
type Space struct {
	Id          string                  `json:"id"`
	ChannelId   string                  `json:"-"`
	TeamId      string                  `json:"team_id"`
	CreatorId   string                  `json:"creator_id"`
	Title       string                  `json:"title"`
	Description string                  `json:"description,omitempty"`
	Icon        string                  `json:"icon,omitempty"`
	Props       mmmodel.StringInterface `json:"props"`
	CreateAt    int64                   `json:"create_at"`
	UpdateAt    int64                   `json:"update_at"`
	DeleteAt    int64                   `json:"delete_at"`
	SortOrder   int64                   `json:"sort_order"`
}

func (s *Space) PreSave() {
	if s.Id == "" {
		s.Id = mmmodel.NewId()
	}

	s.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(s.Title))
	s.Description = mmmodel.SanitizeUnicode(s.Description)

	if s.Props == nil {
		s.Props = make(mmmodel.StringInterface)
	}

	now := mmmodel.GetMillis()
	if s.CreateAt == 0 {
		s.CreateAt = now
	}
	s.UpdateAt = now

	if s.SortOrder == 0 {
		s.SortOrder = s.CreateAt
	}
}

func (s *Space) PreUpdate() {
	s.UpdateAt = mmmodel.GetMillis()
	s.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(s.Title))
	s.Description = mmmodel.SanitizeUnicode(s.Description)

	if s.Props == nil {
		s.Props = make(mmmodel.StringInterface)
	}
}

func (s *Space) Auditable() map[string]any {
	return map[string]any{
		"id":          s.Id,
		"channel_id":  s.ChannelId,
		"team_id":     s.TeamId,
		"creator_id":  s.CreatorId,
		"title":       s.Title,
		"description": s.Description,
		"icon":        s.Icon,
		"props":       s.GetProps(),
		"create_at":   s.CreateAt,
		"update_at":   s.UpdateAt,
		"delete_at":   s.DeleteAt,
		"sort_order":  s.SortOrder,
	}
}

func (s *Space) IsValid() *mmmodel.AppError {
	if !mmmodel.IsValidId(s.Id) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.id.app_error", nil, "", http.StatusBadRequest)
	}

	if s.CreateAt == 0 {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.create_at.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if s.UpdateAt == 0 {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.update_at.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if !mmmodel.IsValidId(s.ChannelId) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.channel_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if s.TeamId != "" && !mmmodel.IsValidId(s.TeamId) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.team_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if s.CreatorId != "" && !mmmodel.IsValidId(s.CreatorId) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.creator_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if strings.TrimSpace(s.Title) == "" {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.title.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if utf8.RuneCountInString(s.Title) > SpaceTitleMaxRunes {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.title_length.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if utf8.RuneCountInString(s.Description) > SpaceDescriptionMaxRunes {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.description_length.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if len(s.Icon) > SpaceIconMaxBytes {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.icon_length.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if err := validatePropsSize("Space.IsValid", "id="+s.Id, s.Props, SpacePropsMaxBytes); err != nil {
		return err
	}

	return nil
}

// GetProps returns Props, or an empty map if Props is nil.
func (s *Space) GetProps() mmmodel.StringInterface {
	return ensureProps(s.Props)
}

// Clone returns a deep copy.
func (s *Space) Clone() *Space {
	cp := *s
	if s.Props != nil {
		cp.Props = deepCloneStringInterface(s.Props)
	}
	return &cp
}
