// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"maps"
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

	// PageSearchTextMaxBytes caps the stored SearchText, the Body's plain-text search projection.
	PageSearchTextMaxBytes = 2 * 1024 * 1024
)

// Compile-time checks that plugin model types satisfy the upstream audit interface.
var (
	_ mmmodel.Auditable = (*Page)(nil)
	_ mmmodel.Auditable = (*Space)(nil)
	_ mmmodel.Auditable = (*Draft)(nil)
)

// Page is stored in the DOCS_Page table. Live pages have (DeleteAt=0, OriginalId="");
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

	CreateAt int64 `json:"create_at"`
	UpdateAt int64 `json:"update_at"`
	// EditAt is the optimistic-lock (CAS) token compared against a caller-supplied
	// base value on update; see Store.UpdatePage.
	EditAt     int64  `json:"edit_at"`
	DeleteAt   int64  `json:"delete_at"`
	OriginalId string `json:"original_id"`

	Props mmmodel.StringInterface `json:"props"`
}

// IsSnapshot reports whether p is a version snapshot rather than a live page.
func (p *Page) IsSnapshot() bool {
	return p.OriginalId != ""
}

// MaxDepthOfPreOrderedPages returns the maximum depth in pages relative to rootID (depth 0).
// pages must be pre-order sorted: each page's parent appears earlier in the slice (or is rootID).
func MaxDepthOfPreOrderedPages(pages []*Page, rootID string) int {
	depthOf := map[string]int{rootID: 0}
	maxDepth := 0
	for _, p := range pages {
		if p.Id == rootID {
			continue
		}
		depth := depthOf[p.ParentId] + 1
		depthOf[p.Id] = depth
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	return maxDepth
}

// PreSave sanitizes Page and defaults its Id-independent fields before insert.
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

// PreUpdate sanitizes Page and stamps UpdateAt before an update is persisted.
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
	Title      *string                  `json:"title"`
	Body       *string                  `json:"body"`
	SearchText *string                  `json:"search_text"`
	Props      *mmmodel.StringInterface `json:"props"`
}

// Patch applies the non-nil fields of patch to the page. Normalization (title trim,
// etc.) happens in PreUpdate. Callers must call patch.IsValid() first — a nil patch is a no-op here
// rather than a panic, but produces no changes, silently defeating the caller's intent.
func (p *Page) Patch(patch *PagePatch) {
	if patch == nil {
		return
	}
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
		p.Props = maps.Clone(*patch.Props)
	}
}

// IsValid checks patch-level rules that Page.IsValid can't, since it only sees the merged page.
// Body and SearchText must be patched together (one without the other desyncs the search index
// from the body); a non-empty SearchText also requires a non-empty Body. Enforced here, not just
// in the service, so callers that bypass the service still uphold it.
func (p *PagePatch) IsValid() *mmmodel.AppError {
	// Reject a nil patch (which would panic on the Body/SearchText cross-checks below) and an
	// all-nil-fields patch (a no-op that would otherwise bump timestamps without a content change).
	if p == nil || (p.Title == nil && p.Body == nil && p.SearchText == nil && p.Props == nil) {
		return mmmodel.NewAppError("PagePatch.IsValid", "model.page.patch.nothing_to_update.app_error", nil, "", http.StatusBadRequest)
	}
	bodyPatched := p.Body != nil
	searchTextPatched := p.SearchText != nil
	if bodyPatched != searchTextPatched {
		return mmmodel.NewAppError("PagePatch.IsValid", "model.page.patch.search_text_body_mismatch.app_error", nil, "", http.StatusBadRequest)
	}
	if p.SearchText != nil && *p.SearchText != "" && *p.Body == "" {
		return mmmodel.NewAppError("PagePatch.IsValid", "model.page.patch.search_text_without_content.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// IsValid checks Page's required fields, size limits, and cross-field invariants.
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

	if p.SearchText != "" && p.Body == "" {
		return mmmodel.NewAppError("Page.IsValid", "model.page.is_valid.search_text_without_body.app_error", nil, "id="+p.Id, http.StatusBadRequest)
	}

	if err := validatePropsSize("Page.IsValid", "id="+p.Id, p.Props, PagePropsMaxBytes); err != nil {
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

	// Enforce the snapshot invariant (see the type comment above) for an early, specific error;
	// chk_docs_page_snapshot_deleted guards the same invariant at the DB level.
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

// Auditable returns Page's fields safe to include in an audit log, excluding Body and SearchText.
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

// GetProps returns Props, or an empty map if Props is nil.
func (p *Page) GetProps() mmmodel.StringInterface {
	return ensureProps(p.Props)
}
