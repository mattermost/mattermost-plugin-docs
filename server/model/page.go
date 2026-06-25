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
	PageTypePage   = "page"
	PageTypeFolder = "page_folder"

	PageTitleMaxRunes = 255

	// PageBodyMaxBytes caps the stored body size; the body is stored as-is (opaque)
	// so this is a storage safety bound against unbounded writes.
	PageBodyMaxBytes = 2 * 1024 * 1024

	// PagePropsMaxBytes caps the serialized size of the opaque Props map.
	PagePropsMaxBytes = 64 * 1024

	// PageSearchTextMaxBytes caps the stored SearchText, which feeds the GIN index.
	PageSearchTextMaxBytes = 2 * 1024 * 1024
)

// Page is stored in the Pages table. Live pages have (DeleteAt=0, OriginalId="");
// snapshots have OriginalId!="" and are always soft-deleted (DeleteAt>0).
type Page struct {
	Id         string `json:"id"`
	SpaceId    string `json:"space_id"`
	ChannelId  string `json:"-"`
	ParentId   string `json:"parent_id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	SearchText string `json:"search_text,omitempty"`

	UserId         string `json:"user_id"`
	LastModifiedBy string `json:"last_modified_by"`
	SortOrder      int64  `json:"sort_order"`

	CreateAt   int64  `json:"create_at"`
	UpdateAt   int64  `json:"update_at"`
	EditAt     int64  `json:"edit_at"`
	DeleteAt   int64  `json:"delete_at"`
	OriginalId string `json:"original_id"`

	Props mmmodel.StringInterface `json:"props"`
}

func (p *Page) PreSave() {
	if p.Id == "" {
		p.Id = mmmodel.NewId()
	}

	p.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(p.Title))

	if p.Type == "" {
		p.Type = PageTypePage
	}

	if p.Props == nil {
		p.Props = make(mmmodel.StringInterface)
	}

	if p.CreateAt == 0 {
		p.CreateAt = mmmodel.GetMillis()
	}
	p.UpdateAt = p.CreateAt
}

func (p *Page) PreUpdate() {
	p.UpdateAt = mmmodel.GetMillis()
	p.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(p.Title))
	// Body is stored as-is (opaque; not validated here).

	if p.Props == nil {
		p.Props = make(mmmodel.StringInterface)
	}
}

// PagePatch is a partial update to a Page: a nil field is left unchanged, a non-nil
// field (including an empty string) is applied. This avoids overloading "" to mean
// both "set to empty" and "leave unchanged". Mirrors MM core's *Patch convention.
type PagePatch struct {
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	SearchText *string `json:"search_text"`
}

// Patch applies the non-nil fields of patch to the page. Normalization (title trim,
// etc.) happens in PreUpdate.
func (p *Page) Patch(patch *PagePatch) {
	if patch.Title != nil {
		p.Title = *patch.Title
	}
	if patch.Body != nil {
		p.Body = *patch.Body
	}
	if patch.SearchText != nil {
		p.SearchText = *patch.SearchText
	}
}

func (p *Page) IsValid() *mmmodel.AppError {
	if !mmmodel.IsValidId(p.Id) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.id.app_error", nil, "", http.StatusBadRequest)
	}

	if !mmmodel.IsValidId(p.UserId) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.user_id.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if p.LastModifiedBy != "" && !mmmodel.IsValidId(p.LastModifiedBy) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.last_modified_by.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if p.CreateAt == 0 {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.create_at.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if p.UpdateAt == 0 {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.update_at.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if p.Type != PageTypePage && p.Type != PageTypeFolder {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.type.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if strings.TrimSpace(p.Title) == "" {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.title.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if utf8.RuneCountInString(p.Title) > PageTitleMaxRunes {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.title_length.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if len(p.Body) > PageBodyMaxBytes {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.body.app_error", map[string]any{"MaxBytes": PageBodyMaxBytes}, "id="+p.Id, http.StatusBadRequest)
	}

	if len(p.SearchText) > PageSearchTextMaxBytes {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.search_text.app_error", map[string]any{"MaxBytes": PageSearchTextMaxBytes}, "id="+p.Id, http.StatusBadRequest)
	}

	if err := validatePropsSize("Page.IsValid", "model.page.is_valid", "id="+p.Id, p.Props, PagePropsMaxBytes); err != nil {
		return err
	}

	if p.ParentId != "" && !mmmodel.IsValidId(p.ParentId) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.parent_id.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if p.ParentId == p.Id {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.parent_self.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if p.OriginalId != "" && !mmmodel.IsValidId(p.OriginalId) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.original_id.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if !mmmodel.IsValidId(p.SpaceId) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.space_id.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if !mmmodel.IsValidId(p.ChannelId) {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.channel_id.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	return nil
}

func (p *Page) Auditable() map[string]any {
	return map[string]any{
		"id":               p.Id,
		"space_id":         p.SpaceId,
		"channel_id":       p.ChannelId,
		"parent_id":        p.ParentId,
		"type":             p.Type,
		"title":            p.Title,
		"user_id":          p.UserId,
		"last_modified_by": p.LastModifiedBy,
		"sort_order":       p.SortOrder,
		"create_at":        p.CreateAt,
		"update_at":        p.UpdateAt,
		"edit_at":          p.EditAt,
		"delete_at":        p.DeleteAt,
		"original_id":      p.OriginalId,
		// Body and SearchText are excluded: large/derived, not audit-relevant.
		"props": p.GetProps(),
	}
}

// GetProps returns Props, initializing to an empty map if nil.
func (p *Page) GetProps() mmmodel.StringInterface {
	return ensureProps(p.Props)
}

// Clone returns a deep copy.
func (p *Page) Clone() *Page {
	cp := *p

	if p.Props != nil {
		cp.Props = deepCloneStringInterface(p.Props)
	}

	return &cp
}
