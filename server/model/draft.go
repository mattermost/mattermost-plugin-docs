// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"net/http"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// DraftFileIdsMaxRunes is the maximum rune length of the serialized FileIds JSON array.
const DraftFileIdsMaxRunes = 300

// DraftPropsOriginalPageEditAt stores the EditAt the user last saw when they opened
// a page for editing — used as the optimistic-lock baseline on publish.
const DraftPropsOriginalPageEditAt = "original_page_edit_at"

// Draft is a per-user autosave draft for a space page, stored in DOCS_Draft.
//
// A draft is keyed by (UserId, PageId): PageId is the page id reserved when the
// user starts editing and stable across the draft -> publish lifecycle, so a
// draft for a not-yet-created page and a draft editing an existing page share the
// same key. An orphan draft — one whose PageId has no matching DOCS_Page row — is legal,
// since the page has not been published yet. ParentId carries the pending hierarchy
// parent for a new page; Body holds the raw (opaque) editor content.
//
// LastActiveAt is distinct from UpdateAt: it moves only when the user themselves saves the draft,
// whereas UpdateAt also moves when a bulk maintenance write touches the row. Editor presence is
// derived from LastActiveAt, so only real authoring activity counts as editing.
type Draft struct {
	UserId       string                  `json:"user_id"`
	SpaceId      string                  `json:"space_id"`
	PageId       string                  `json:"page_id"`
	ParentId     string                  `json:"parent_id"`
	Title        string                  `json:"title"`
	Body         string                  `json:"body"`
	FileIds      mmmodel.StringArray     `json:"file_ids"`
	Props        mmmodel.StringInterface `json:"props"`
	CreateAt     int64                   `json:"create_at"`
	UpdateAt     int64                   `json:"update_at"`
	LastActiveAt int64                   `json:"last_active_at"`
}

// DraftSummary is the metadata projection returned by draft collection endpoints. It deliberately
// omits Body, which can be up to PageBodyMaxBytes per draft. Fetch a Draft by page id when the
// content is required.
type DraftSummary struct {
	UserId       string                  `json:"user_id"`
	SpaceId      string                  `json:"space_id"`
	PageId       string                  `json:"page_id"`
	ParentId     string                  `json:"parent_id"`
	Title        string                  `json:"title"`
	FileIds      mmmodel.StringArray     `json:"file_ids"`
	Props        mmmodel.StringInterface `json:"props"`
	CreateAt     int64                   `json:"create_at"`
	UpdateAt     int64                   `json:"update_at"`
	LastActiveAt int64                   `json:"last_active_at"`
}

// PreSave sanitizes Draft and defaults its Id-independent fields before insert.
func (d *Draft) PreSave() {
	d.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(d.Title))
	// Body is stored as-is (no sanitization, unlike Title).

	if d.FileIds == nil {
		d.FileIds = mmmodel.StringArray{}
	}

	if d.Props == nil {
		d.Props = make(mmmodel.StringInterface)
	}

	now := mmmodel.GetMillis()
	if d.CreateAt == 0 {
		d.CreateAt = now
	}
	d.UpdateAt = now
	// PreSave runs only on a user's own save of the draft, which is exactly the signal presence
	// needs — bulk maintenance writes update the row without going through here.
	d.LastActiveAt = now
}

// Auditable returns Draft's fields safe to include in an audit log, excluding Body.
func (d *Draft) Auditable() map[string]any {
	return map[string]any{
		"user_id":        d.UserId,
		"space_id":       d.SpaceId,
		"page_id":        d.PageId,
		"parent_id":      d.ParentId,
		"title":          d.Title,
		"file_ids":       d.FileIds,
		"props":          d.GetProps(),
		"create_at":      d.CreateAt,
		"update_at":      d.UpdateAt,
		"last_active_at": d.LastActiveAt,
	}
}

// IsValid checks Draft's required fields and size limits.
func (d *Draft) IsValid() *mmmodel.AppError {
	if !mmmodel.IsValidId(d.UserId) {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.user_id.app_error", nil, "user_id="+d.UserId, http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(d.SpaceId) {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.space_id.app_error", nil, "space_id="+d.SpaceId, http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(d.PageId) {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.page_id.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if d.ParentId != "" {
		if !mmmodel.IsValidId(d.ParentId) {
			return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.parent_id.app_error", nil, "parent_id="+d.ParentId, http.StatusBadRequest)
		}
		if d.ParentId == d.PageId {
			return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.parent_self.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
		}
	}

	if d.CreateAt == 0 {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.create_at.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if d.UpdateAt == 0 {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.update_at.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if d.LastActiveAt == 0 {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.last_active_at.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	// A draft publishes into a page, so it is bound by the page content limits.
	if utf8.RuneCountInString(d.Title) > PageTitleMaxRunes {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.title_length.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if len(d.Body) > PageBodyMaxBytes {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.body_size.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if utf8.RuneCountInString(mmmodel.ArrayToJSON(d.FileIds)) > DraftFileIdsMaxRunes {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.file_ids.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if err := validatePropsSize("Draft.IsValid", "page_id="+d.PageId, d.Props, PagePropsMaxBytes); err != nil {
		return err
	}

	return nil
}

// GetProps returns Props, or an empty map if Props is nil.
func (d *Draft) GetProps() mmmodel.StringInterface {
	d.Props = ensureProps(d.Props)
	return d.Props
}

// SanitizeProps strips any props key not on the recognized allowlist. Call on every write path to
// prevent unknown client-supplied keys from accumulating in the stored map.
func (d *Draft) SanitizeProps() {
	allowed := mmmodel.StringInterface{}
	if v, ok := d.Props[DraftPropsOriginalPageEditAt]; ok {
		allowed[DraftPropsOriginalPageEditAt] = v
	}
	d.Props = allowed
}

// EditBaseline extracts the optimistic-lock baseline from the draft's props. JSON-decoded numbers
// arrive as float64; a programmatically-set int64/int is also accepted. Returns (0, false) when
// the baseline is absent or zero (e.g. a new-page draft).
func (d *Draft) EditBaseline() (int64, bool) {
	v, ok := d.GetProps()[DraftPropsOriginalPageEditAt]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), n != 0
	case int64:
		return n, n != 0
	case int:
		return int64(n), n != 0
	}
	return 0, false
}
