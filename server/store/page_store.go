// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"fmt"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// pageColumnList is the ordered column list for SELECT/INSERT on DOCS_Page.
// Must stay in sync with pageToSlice.
var pageColumnList = []string{
	"Id", "SpaceId", "ChannelId", "ParentId", "Type",
	"Title", "Body", "SearchText",
	"UserId", "LastModifiedBy", "SortOrder",
	"CreateAt", "UpdateAt", "EditAt", "DeleteAt", "OriginalId",
	"Props",
	"ReparentedParentOnDelete", "ReparentedChildrenOnDelete",
}

// pageColumnsWithAlias returns columns prefixed with the given alias.
func pageColumnsWithAlias(alias string) []string {
	out := make([]string, len(pageColumnList))
	for i, c := range pageColumnList {
		out[i] = alias + "." + c
	}
	return out
}

// pageToSlice converts a Page struct to an ordered value slice for INSERT.
func pageToSlice(p *model.Page) []any {
	return []any{
		p.Id, p.SpaceId, p.ChannelId, p.ParentId, p.Type,
		p.Title, p.Body, p.SearchText,
		p.UserId, p.LastModifiedBy, p.SortOrder,
		p.CreateAt, p.UpdateAt, p.EditAt, p.DeleteAt, p.OriginalId,
		p.Props,
		p.ReparentedParentOnDelete, p.ReparentedChildrenOnDelete,
	}
}

// pageColumnsP is pageColumnList prefixed with "p.", precomputed.
var pageColumnsP = pageColumnsWithAlias("p")

// CreatePage inserts a new page and assigns its sort order under a transaction-scoped
// advisory lock keyed on (channelId, parentId) that serializes concurrent creates.
func (s *Store) CreatePage(page *model.Page) (_ *model.Page, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the space row for the lifetime of this insert so it serializes with
	// DeleteSpace: a racing delete blocks here and then cascades the new page, while
	// a space already gone causes an immediate abort.
	spaceLockQuery := s.getQueryBuilder().
		Select("ChannelId").
		From("DOCS_Space").
		Where(sq.Eq{"Id": page.SpaceId, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	var spaceChannelID string
	if spErr := s.getBuilder(tx, &spaceChannelID, spaceLockQuery); spErr != nil {
		if errors.Is(spErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: page.SpaceId}
		}
		return nil, errors.Wrap(spErr, "failed to lock space for page create")
	}
	// Derive ChannelId from the locked space row (single source of truth via
	// uq_docs_space_channel_id) rather than trusting the caller-supplied value.
	page.ChannelId = spaceChannelID

	// Re-verify the parent is still live under the same transaction.
	if page.ParentId != "" {
		parentLockQuery := s.getQueryBuilder().
			Select("1").
			From("DOCS_Page").
			Where(sq.Eq{"Id": page.ParentId, "SpaceId": page.SpaceId, "DeleteAt": 0}).
			Suffix("FOR UPDATE")
		var parentExists int
		if pErr := s.getBuilder(tx, &parentExists, parentLockQuery); pErr != nil {
			if errors.Is(pErr, sql.ErrNoRows) {
				return nil, &ErrInvalidInput{Entity: "Page", Field: "ParentId", Value: page.ParentId}
			}
			return nil, errors.Wrap(pErr, "failed to lock parent page for create")
		}
	}

	// Acquire an advisory lock keyed on (channelId, parentId).
	lockKey := page.ChannelId + ":" + page.ParentId
	if _, lockErr := s.exec(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); lockErr != nil {
		return nil, errors.Wrap(lockErr, "failed to acquire advisory lock for page create")
	}

	// The advisory lock serializes concurrent creates for this (channelId, parentId),
	// so a single MAX is safe.
	maxOrderQuery := s.getQueryBuilder().
		Select("COALESCE(MAX(SortOrder), 0)").
		From("DOCS_Page").
		Where(sq.Eq{
			"ChannelId": page.ChannelId,
			"ParentId":  page.ParentId,
			"DeleteAt":  0,
		})

	var maxOrder int64
	if maxErr := s.getBuilder(tx, &maxOrder, maxOrderQuery); maxErr != nil {
		return nil, errors.Wrap(maxErr, "failed to get max sort order for page create")
	}
	page.SortOrder = maxOrder + 1

	page.PreSave()
	if validErr := page.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error()}
	}

	insertQuery := s.getQueryBuilder().
		Insert("DOCS_Page").
		Columns(pageColumnList...).
		Values(pageToSlice(page)...)

	if _, execErr := s.execBuilder(tx, insertQuery); execErr != nil {
		if isUniqueViolation(execErr) {
			return nil, &ErrConflict{Resource: "Page id=" + page.Id}
		}
		return nil, errors.Wrap(execErr, "failed to save Page")
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return page, nil
}

// GetPage fetches a live (or optionally deleted) page by ID.
func (s *Store) GetPage(pageID string, includeDeleted bool) (*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := s.getQueryBuilder().
		Select(pageColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{"p.Id": pageID})

	if !includeDeleted {
		query = query.Where(sq.Eq{"p.DeleteAt": 0})
	}

	var page model.Page
	if err := s.getBuilder(s.db, &page, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, errors.Wrap(err, "failed to get page")
	}

	return &page, nil
}

// UpdatePage updates a page with optimistic locking using EditAt for compare-and-swap.
func (s *Store) UpdatePage(page *model.Page) (_ *model.Page, err error) {
	if page.Id == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "Id", Value: page.Id}
	}
	// Normalize and validate before touching the DB; UpdateAt is recomputed below.
	page.PreUpdate()
	if validErr := page.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error()}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	selectQuery := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.And{
			sq.Eq{"Id": page.Id},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
		})

	var currentPage model.Page
	if txErr := s.getBuilder(tx, &currentPage, selectQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Page", ID: page.Id}
		}
		return nil, errors.Wrap(txErr, "failed to get current page")
	}

	// Keep EditAt strictly monotonic (it is the CAS token).
	now := nextMonotonic(mmmodel.GetMillis(), currentPage.EditAt)

	updateQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("Title", page.Title).
		Set("Body", page.Body).
		Set("SearchText", page.SearchText).
		Set("SortOrder", page.SortOrder).
		Set("LastModifiedBy", page.LastModifiedBy).
		Set("Props", page.GetProps()).
		Set("UpdateAt", now).
		Set("EditAt", now).
		Where(sq.And{
			sq.Eq{"Id": page.Id},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
			sq.Eq{"EditAt": page.EditAt},
		})

	result, execErr := s.execBuilder(tx, updateQuery)
	if execErr != nil {
		return nil, errors.Wrap(execErr, "failed to update page")
	}

	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, errors.Wrap(rowsErr, "failed to get rows affected")
	}
	if rowsAffected == 0 {
		// Row existed at the SELECT, so zero means a concurrent delete (→ 404) or
		// stale EditAt (→ 409). Check which.
		existsQuery := s.getQueryBuilder().
			Select("1").
			From("DOCS_Page").
			Where(sq.And{
				sq.Eq{"Id": page.Id},
				sq.Eq{"DeleteAt": 0},
				sq.Eq{"OriginalId": ""},
			})
		var stillExists int
		if existsErr := s.getBuilder(tx, &stillExists, existsQuery); existsErr != nil {
			if errors.Is(existsErr, sql.ErrNoRows) {
				return nil, &ErrNotFound{EntityName: "Page", ID: page.Id}
			}
			return nil, errors.Wrap(existsErr, "failed to check page existence after zero rows affected")
		}
		return nil, &ErrConflict{Resource: "Page id=" + page.Id + " (concurrent edit)"}
	}

	// Build the return value from the pre-update snapshot without an extra round-trip.
	currentPage.Title = page.Title
	currentPage.Body = page.Body
	currentPage.SearchText = page.SearchText
	currentPage.SortOrder = page.SortOrder
	currentPage.LastModifiedBy = page.LastModifiedBy
	currentPage.Props = page.GetProps()
	currentPage.UpdateAt = now
	currentPage.EditAt = now

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return &currentPage, nil
}

// GetPageChildren fetches direct live children of a page, ordered by the
// caller-maintained SortOrder (ascending), with CreateAt then Id as stable
// tie-breakers.
func (s *Store) GetPageChildren(pageID string, offset, limit int) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := s.getQueryBuilder().
		Select(pageColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{
			"p.ParentId":   pageID,
			"p.DeleteAt":   0,
			"p.OriginalId": "",
		}).
		OrderBy("p.SortOrder ASC", "p.CreateAt ASC", "p.Id ASC")

	if limit > 0 {
		if offset < 0 {
			offset = 0
		}
		query = query.Limit(uint64(limit)).Offset(uint64(offset)) //nolint:gosec // limit>0 and offset>=0 enforced above
	}

	pages := []*model.Page{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find children for page_id=%s", pageID)
	}

	return pages, nil
}

// GetPageDescendants fetches all live descendants. Returns ErrLimitExceeded rather
// than silently truncating when the subtree exceeds MaxPageDescendantsLimit.
func (s *Store) GetPageDescendants(pageID string) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := pageDescendantsCTE +
		fmt.Sprintf(" LIMIT %d", MaxPageDescendantsLimit+1)

	pages := []*model.Page{}
	if err := s.selectAll(s.db, &pages, query, pageID); err != nil {
		return nil, errors.Wrapf(err, "failed to find descendants for page_id=%s", pageID)
	}
	if len(pages) > MaxPageDescendantsLimit {
		return nil, &ErrLimitExceeded{Resource: "Page descendants for page_id=" + pageID, Limit: MaxPageDescendantsLimit}
	}

	return pages, nil
}

// GetSpacePages fetches a paginated set of live pages in a space, ordered by
// CreateAt DESC. Filters on SpaceId (not ChannelId) so pages from a prior
// soft-deleted space that shared the channel are never returned.
func (s *Store) GetSpacePages(spaceID string, offset, limit int) ([]*model.Page, error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "spaceID", Value: spaceID}
	}

	query := s.getQueryBuilder().
		Select(pageColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{
			"p.SpaceId":    spaceID,
			"p.DeleteAt":   0,
			"p.OriginalId": "",
		}).
		OrderBy("p.CreateAt DESC, p.Id DESC")

	if limit > 0 {
		if offset < 0 {
			offset = 0
		}
		query = query.Limit(uint64(limit)).Offset(uint64(offset)) //nolint:gosec // limit>0 and offset>=0 enforced above
	}

	pages := []*model.Page{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find pages for space_id=%s", spaceID)
	}

	return pages, nil
}

// GetPageAncestorDepth returns the count of live ancestors (excluding the page itself).
func (s *Store) GetPageAncestorDepth(pageID string) (int, error) {
	if pageID == "" {
		return 0, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	var count int
	if err := s.get(s.db, &count, pageAncestorCountCTE, pageID); err != nil {
		return 0, errors.Wrapf(err, "failed to count ancestors for page_id=%s", pageID)
	}
	return count, nil
}

// GetPageAncestors fetches all live ancestors of a page up to the root.
func (s *Store) GetPageAncestors(pageID string) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := pageAncestorsCTE + fmt.Sprintf(" LIMIT %d", MaxPageHierarchyDepth+1)

	pages := []*model.Page{}
	if err := s.selectAll(s.db, &pages, query, pageID); err != nil {
		return nil, errors.Wrapf(err, "failed to find ancestors for page_id=%s", pageID)
	}
	// The CTE recurses one level past the cap (see page_hierarchy.go), so an
	// over-deep chain yields MaxPageHierarchyDepth+1 rows — error rather than truncate.
	if len(pages) > MaxPageHierarchyDepth {
		return nil, &ErrLimitExceeded{Resource: "Page ancestors for page_id=" + pageID, Limit: MaxPageHierarchyDepth}
	}

	return pages, nil
}
