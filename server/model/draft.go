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

// MaxDraftsPerUserPerSpace is the maximum number of draft rows a single user may hold in one
// space.
const MaxDraftsPerUserPerSpace = 100

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
	// BaseEditAt is the optimistic-lock (CAS) baseline: the page EditAt the client saw when it opened
	// this page for editing, compared against the page's current EditAt on publish to reject a
	// concurrent-edit conflict. Write-once — frozen at the SQL layer on conflict (see
	// Store.UpsertDraft), so unlike the mutable Page.EditAt it never changes once established. 0 means
	// no baseline: either a new-page draft or an existing-page edit whose baseline was never captured
	// (fails closed to a forced publish). "New page" is decided by page existence, never by
	// BaseEditAt == 0.
	BaseEditAt int64 `json:"base_edit_at"`
}

// DraftSummary is the metadata projection returned by draft collection endpoints. It deliberately
// omits Body and Props: Body can be up to PageBodyMaxBytes per draft, and Props is opaque and may
// be up to PagePropsMaxBytes — matching the fields PageSummary omits for the same reason. Fetch a
// Draft by page id when the content is required.
type DraftSummary struct {
	UserId       string              `json:"user_id"`
	SpaceId      string              `json:"space_id"`
	PageId       string              `json:"page_id"`
	ParentId     string              `json:"parent_id"`
	Title        string              `json:"title"`
	FileIds      mmmodel.StringArray `json:"file_ids"`
	CreateAt     int64               `json:"create_at"`
	UpdateAt     int64               `json:"update_at"`
	LastActiveAt int64               `json:"last_active_at"`
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
		"base_edit_at":   d.BaseEditAt,
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

	// Write-path contract only: PreSave always stamps LastActiveAt, so a zero here means the
	// caller skipped PreSave. Stored rows can still legitimately hold 0 — bulk maintenance writes
	// (cross-space move) reset LastActiveAt in SQL to drop the owner from presence — so a read
	// path must not treat a stored 0 as invalid.
	if d.LastActiveAt == 0 {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.last_active_at.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	// BaseEditAt is an optimistic-lock baseline (a page EditAt) or 0 for "no baseline"; a negative
	// value is never legitimate. 0 is legal and must not be rejected.
	if d.BaseEditAt < 0 {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.base_edit_at.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	// A draft publishes into a page, so it is bound by the page content limits.
	if utf8.RuneCountInString(d.Title) > PageTitleMaxRunes {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.title_length.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if len(d.Body) > PageBodyMaxBytes {
		return mmmodel.NewAppError("Draft.IsValid", "model.draft.is_valid.body_size.app_error", nil, "page_id="+d.PageId, http.StatusBadRequest)
	}

	if err := ValidateDraftFileIds("Draft.IsValid", "page_id="+d.PageId, d.FileIds); err != nil {
		return err
	}

	if err := ValidatePropsSize("Draft.IsValid", "page_id="+d.PageId, d.Props, PagePropsMaxBytes); err != nil {
		return err
	}

	return nil
}

// ValidateDraftFileIds checks that the serialized file-id list stays within DraftFileIdsMaxRunes and
// that every entry is a well-formed ID. An empty list is valid; an empty entry is not, since a blank
// ID names no file and would be stored verbatim.
func ValidateDraftFileIds(where, detail string, fileIDs mmmodel.StringArray) *mmmodel.AppError {
	if utf8.RuneCountInString(mmmodel.ArrayToJSON(fileIDs)) > DraftFileIdsMaxRunes {
		return mmmodel.NewAppError(where, "model.draft.is_valid.file_ids.app_error", nil, detail, http.StatusBadRequest)
	}
	for _, fileID := range fileIDs {
		if !mmmodel.IsValidId(fileID) {
			return mmmodel.NewAppError(where, "model.draft.is_valid.file_id.app_error", nil, detail, http.StatusBadRequest)
		}
	}
	return nil
}

// ValidateDraftWriteIntent applies the FileIds and Props bounds to the pointer-carried write-intent
// values a draft upsert takes alongside the Draft struct. Those values never reach the struct's own
// fields — the upsert merges them in SQL — so IsValid cannot see them, and without this the bounds
// would hold only for callers that remembered to check them separately. A nil pointer means the
// field was omitted and the stored value is preserved, so there is nothing to bound.
func ValidateDraftWriteIntent(where, detail string, fileIDs *mmmodel.StringArray, props *mmmodel.StringInterface) *mmmodel.AppError {
	if fileIDs != nil {
		if err := ValidateDraftFileIds(where, detail, *fileIDs); err != nil {
			return err
		}
	}
	if props != nil {
		if err := ValidatePropsSize(where, detail, *props, PagePropsMaxBytes); err != nil {
			return err
		}
	}
	return nil
}

// GetProps returns Props, or an empty map if Props is nil.
func (d *Draft) GetProps() mmmodel.StringInterface {
	return ensureProps(d.Props)
}
