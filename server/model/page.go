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

	now := mmmodel.GetMillis()
	if p.CreateAt == 0 {
		p.CreateAt = now
	}
	p.UpdateAt = now
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
// SortOrder is deliberately not patchable here: changing a page's position is a sibling-group
// reorder/move concern (group locking, renumbering, duplicate/negative prevention), not a
// generic field edit, and belongs in a dedicated operation.
type PagePatch struct {
	Title      *string                  `json:"title"`
	Body       *string                  `json:"body"`
	SearchText *string                  `json:"search_text"`
	Props      *mmmodel.StringInterface `json:"props"`
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
	if patch.Props != nil {
		p.Props = *patch.Props
	}
}

// IsValid enforces patch-level invariants that Page.IsValid cannot see, because they depend on
// which fields the patch carries rather than on the merged page's final values. SearchText is
// Body's plain-text projection backing the search index (Body is opaque rich-text, so it can't
// be tokenized directly), so the two must be patched together: a Body change that left
// SearchText stale would desync the index, and a SearchText change without a Body change would
// desync the projection. A non-empty SearchText additionally requires a non-empty Body. This
// lives on the patch (not only in the service) so every store.UpdatePage caller upholds it.
func (p *PagePatch) IsValid() *mmmodel.AppError {
	// A nil or all-nil patch is a no-op: rejecting it here (not only in the service) keeps a
	// direct store.UpdatePage caller from panicking on nil or bumping UpdateAt/EditAt/
	// LastModifiedBy without a content change.
	if p == nil || (p.Title == nil && p.Body == nil && p.SearchText == nil && p.Props == nil) {
		return mmmodel.NewAppError("PagePatch.IsValid", "model.page.patch.nothing_to_update.app_error", nil, "", http.StatusBadRequest)
	}
	if (p.Body == nil) != (p.SearchText == nil) {
		return mmmodel.NewAppError("PagePatch.IsValid", "model.page.patch.search_text_body_mismatch.app_error", nil, "", http.StatusBadRequest)
	}
	if p.SearchText != nil && *p.SearchText != "" && *p.Body == "" {
		return mmmodel.NewAppError("PagePatch.IsValid", "model.page.patch.search_text_without_content.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
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

	if p.OriginalId == p.Id {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.original_self.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	// A version snapshot (OriginalId set) is always soft-deleted; enforce the invariant the
	// chk_docs_page_snapshot_deleted DB constraint also guards, for an early, specific error.
	if p.OriginalId != "" && p.DeleteAt == 0 {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.snapshot_not_deleted.app_error", nil, "id="+p.Id, http.StatusBadRequest)
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
