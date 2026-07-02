// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
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
}

// pageToSlice converts a Page struct to an ordered value slice for INSERT.
func pageToSlice(p *model.Page) []any {
	return []any{
		p.Id, p.SpaceId, p.ChannelId, p.ParentId, p.Type,
		p.Title, p.Body, p.SearchText,
		p.UserId, p.LastModifiedBy, p.SortOrder,
		p.CreateAt, p.UpdateAt, p.EditAt, p.DeleteAt, p.OriginalId,
		p.Props,
	}
}

var pageColumnsP = columnsWithAlias("p", pageColumnList)

// liveNonSnapshotFilter is the squirrel condition marking a page as live and not a version
// snapshot (DeleteAt=0, OriginalId=""), shared by every store method that requires a live
// page. alias is the optional table alias prefix (e.g. "p." for a query aliasing DOCS_Page
// as p); pass "" for an unaliased query.
func liveNonSnapshotFilter(alias string) sq.Eq {
	return sq.Eq{alias + "DeleteAt": 0, alias + "OriginalId": ""}
}

// CreatePage inserts a new page, assigning it the next sort order among its siblings.
func (s *Store) CreatePage(page *model.Page) (_ *model.Page, err error) {
	if page == nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "page", Value: nil}
	}

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
		if pErr := s.lockLiveParent(tx, page.ParentId, page.SpaceId, "Page"); pErr != nil {
			return nil, pErr
		}
	}

	// Take the next sort slot for this (channelId, parentId) sibling group.
	sortOrder, sortErr := s.nextSortOrder(tx, page.ChannelId, page.ParentId)
	if sortErr != nil {
		return nil, sortErr
	}
	page.SortOrder = sortOrder

	page.PreSave()
	if validErr := page.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
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

// UpdatePage applies patch to a live page. baseEditAt is the EditAt the caller last saw.
// When force is false it compare-and-swaps on EditAt, returning ErrConflict if a concurrent
// writer has advanced it. When force is true the CAS is skipped, so fields the patch leaves
// untouched keep any concurrent edit rather than being clobbered by a stale snapshot.
// lastModifiedBy records the editor.
func (s *Store) UpdatePage(pageID string, patch *model.PagePatch, baseEditAt int64, force bool, lastModifiedBy string) (_ *model.Page, err error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "Id", Value: pageID}
	}
	// Validate the patch before opening the transaction, so an invalid or empty patch never locks
	// the row or bumps UpdateAt/EditAt/LastModifiedBy. Enforced here, not only in the service,
	// so any store caller upholds the contract.
	if validErr := patch.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "Patch", Value: validErr.Error(), Reason: validErr.Id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the live row so the read-modify-write is atomic. The lock (not an EditAt
	// predicate) is what makes the write safe, so both paths merge the patch into the value
	// read here; no concurrent writer can slip between this read and the UPDATE below.
	selectQuery := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR UPDATE")

	var page model.Page
	if txErr := s.getBuilder(tx, &page, selectQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, errors.Wrap(txErr, "failed to get current page")
	}

	if !force && page.EditAt != baseEditAt {
		return nil, &ErrConflict{Resource: "Page id=" + pageID + " (concurrent edit)"}
	}

	page.Patch(patch)
	page.LastModifiedBy = lastModifiedBy
	page.PreUpdate()
	if validErr := page.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	// Keep EditAt strictly monotonic (it is the CAS token).
	now := nextMonotonic(mmmodel.GetMillis(), page.EditAt)

	// SortOrder is intentionally not written here: generic edits never move a page within its
	// sibling group (that is a dedicated reorder concern), so the column keeps its value.
	updateQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("Title", page.Title).
		Set("Body", page.Body).
		Set("SearchText", page.SearchText).
		Set("LastModifiedBy", page.LastModifiedBy).
		Set("Props", page.GetProps()).
		Set("UpdateAt", now).
		Set("EditAt", now).
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter(""))

	result, execErr := s.execBuilder(tx, updateQuery)
	if execErr != nil {
		return nil, errors.Wrap(execErr, "failed to update page")
	}
	// The row is held by the lock above, so this only guards against an impossible
	// disappearance (e.g. a concurrent hard delete with no FK).
	if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
		return nil, raErr
	}

	page.UpdateAt = now
	page.EditAt = now

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return &page, nil
}

// lockLiveSpace FOR UPDATE-locks the live space row.
// Returns ErrNotFound if the space does not exist or is already soft-deleted.
func (s *Store) lockLiveSpace(tx *sqlx.Tx, spaceID string) error {
	query := s.getQueryBuilder().
		Select("1").
		From("DOCS_Space").
		Where(sq.Eq{"Id": spaceID, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	var exists int
	if err := s.getBuilder(tx, &exists, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Space", ID: spaceID}
		}
		return errors.Wrap(err, "failed to lock space")
	}
	return nil
}

// tryLockLiveParent FOR UPDATE-locks parentID and reports whether it is still a live,
// non-snapshot page in spaceID.
func (s *Store) tryLockLiveParent(tx *sqlx.Tx, parentID, spaceID string) (bool, error) {
	query := s.getQueryBuilder().
		Select("1").
		From("DOCS_Page").
		Where(sq.Eq{"Id": parentID, "SpaceId": spaceID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR UPDATE")
	var exists int
	if err := s.getBuilder(tx, &exists, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, errors.Wrap(err, "failed to lock parent page")
	}
	return true, nil
}

// lockLiveParent FOR UPDATE-locks the prospective parent page, requiring it to be a live page
// in the given space. A cross-space or missing parent finds no row and yields ErrInvalidInput
// (Field "ParentId").
// entity names the calling resource (e.g. "Page", "Draft") for the error.
func (s *Store) lockLiveParent(tx *sqlx.Tx, parentID, spaceID, entity string) error {
	ok, err := s.tryLockLiveParent(tx, parentID, spaceID)
	if err != nil {
		return err
	}
	if !ok {
		return &ErrInvalidInput{Entity: entity, Field: "ParentId", Value: parentID}
	}
	return nil
}

// nextSortOrder acquires the advisory lock for the (channelID, parentID) sibling group and
// returns the next SortOrder (MAX+1). hashtextextended maps the key to a single bigint, so a
// hash collision only over-serializes two unrelated sibling groups — added contention (each
// group still computes its own MAX), never corruption, with negligible probability. Must be
// called inside tx; the lock is held until the transaction ends.
func (s *Store) nextSortOrder(tx *sqlx.Tx, channelID, parentID string) (int64, error) {
	lockKey := channelID + ":" + parentID
	if _, lockErr := s.exec(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); lockErr != nil {
		return 0, errors.Wrap(lockErr, "failed to acquire advisory lock for sort order")
	}
	maxOrderQuery := s.getQueryBuilder().
		Select("COALESCE(MAX(SortOrder), 0)").
		From("DOCS_Page").
		Where(sq.Eq{"ChannelId": channelID, "ParentId": parentID, "DeleteAt": 0})
	var maxOrder int64
	if maxErr := s.getBuilder(tx, &maxOrder, maxOrderQuery); maxErr != nil {
		return 0, errors.Wrap(maxErr, "failed to get max sort order")
	}
	return maxOrder + 1, nil
}

// DeletePage soft-deletes a live page and promotes its live children to the page's own
// parent as a contiguous block in the page's slot so their relative order survives. It
// rejects snapshots (OriginalId != ""). The invariant that no live page sits under a
// deleted parent holds even under concurrent CreatePage calls.
func (s *Store) DeletePage(pageID string) (err error) {
	if pageID == "" {
		return &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Unlocked read of the page's space (immutable for a live page) to pick the space row to
	// lock first — space-before-page avoids deadlocking with a concurrent DeleteSpace.
	spaceIDQuery := s.getQueryBuilder().
		Select("SpaceId").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter(""))
	var spaceID string
	if idErr := s.getBuilder(tx, &spaceID, spaceIDQuery); idErr != nil {
		if errors.Is(idErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(idErr, "failed to read page space for delete")
	}

	if lockErr := s.lockLiveSpace(tx, spaceID); lockErr != nil {
		return lockErr
	}

	// Lock the target row and take its parent and sort position from the locked row.
	var deleted struct {
		ParentID  string
		SortOrder int64
		ChannelID string
		CreateAt  int64
	}
	pageLockQuery := s.getQueryBuilder().
		Select("ParentId", "SortOrder", "ChannelId", "CreateAt").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR UPDATE")
	if lockErr := s.getBuilder(tx, &deleted, pageLockQuery); lockErr != nil {
		if errors.Is(lockErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(lockErr, "failed to lock page for delete")
	}

	// Read the live children in GetPageChildren's (SortOrder, CreateAt, Id) order, locking
	// them FOR UPDATE; that order drives the block placement below.
	childIDsQuery := s.getQueryBuilder().
		Select("Id").
		From("DOCS_Page").
		Where(sq.Eq{"ParentId": pageID, "DeleteAt": 0}).
		OrderBy("SortOrder ASC", "CreateAt ASC", "Id ASC").
		Suffix("FOR UPDATE")
	var childIDs []string
	if txErr := s.selectBuilder(tx, &childIDs, childIDsQuery); txErr != nil {
		return errors.Wrap(txErr, "failed to read children for promote")
	}

	// Place the children as a contiguous block in the deleted page's slot, keeping their
	// relative order. The space lock serializes this and SortOrder is non-unique, so the
	// renumber can't collide.
	if n := len(childIDs); n > 0 {
		// Make room: the freed slot fits one but the block needs n, so shift later siblings
		// up by n-1. A single child needs no shift.
		if n > 1 {
			shiftQuery := s.getQueryBuilder().
				Update("DOCS_Page").
				Set("SortOrder", sq.Expr("SortOrder + ?", n-1)).
				Set("UpdateAt", now).
				Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
				Where(sq.And{
					sq.Eq{"ChannelId": deleted.ChannelID, "ParentId": deleted.ParentID, "DeleteAt": 0},
					// Match GetPageChildren's (SortOrder, CreateAt, Id) order: SortOrder is
					// non-unique, so also shift siblings that tie on SortOrder but sort after
					// the deleted page on the CreateAt/Id tie-breakers.
					sq.Or{
						sq.Gt{"SortOrder": deleted.SortOrder},
						sq.Expr("(SortOrder = ? AND (CreateAt > ? OR (CreateAt = ? AND Id > ?)))",
							deleted.SortOrder, deleted.CreateAt, deleted.CreateAt, pageID),
					},
				})
			if _, txErr := s.execBuilder(tx, shiftQuery); txErr != nil {
				return errors.Wrap(txErr, "failed to make room for child block")
			}
		}

		// Reparent all children in a single statement: SortOrder is per-child, so drive it
		// from a CASE keyed on Id rather than issuing one UPDATE per child.
		// Explicit ::bigint cast on the THEN branch: with no ELSE clause and only bound
		// parameters, Postgres can't infer the CASE result type and defaults to text,
		// which then fails to assign into the bigint SortOrder column.
		sortOrderCase := sq.Case("Id")
		for i, childID := range childIDs {
			sortOrderCase = sortOrderCase.When(sq.Expr("?", childID), sq.Expr("?::bigint", deleted.SortOrder+int64(i)))
		}
		blockQuery := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("ParentId", deleted.ParentID).
			Set("SortOrder", sortOrderCase).
			Set("UpdateAt", now).
			Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
			Where(sq.Eq{"Id": childIDs, "ParentId": pageID, "DeleteAt": 0})
		result, txErr := s.execBuilder(tx, blockQuery)
		if txErr != nil {
			return errors.Wrap(txErr, "failed to reparent children into block")
		}
		// The children were locked FOR UPDATE, so an affected count below len(childIDs) means
		// inconsistent state (a child no longer matches) rather than benign contention. Fail
		// loudly with a plain error so the app layer maps it to 500, not the 404 a sentinel
		// would yield.
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return errors.Wrap(rowsErr, "failed to read rows affected for child reparent")
		}
		if rows != int64(len(childIDs)) {
			return errors.Errorf("expected to reparent %d children but affected %d rows after FOR UPDATE lock", len(childIDs), rows)
		}
	}

	deleteQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", now).
		Set("UpdateAt", now).
		Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter(""))
	result, txErr := s.execBuilder(tx, deleteQuery)
	if txErr != nil {
		return errors.Wrap(txErr, "failed to delete page")
	}
	if rowsErr := checkRowsAffected(result, "Page", pageID); rowsErr != nil {
		return rowsErr
	}

	// A draft is unpublished work on the page, so deleting the page ends its life (mirrors core
	// deleting drafts associated with a deleted post); a new-page draft parented under this page
	// is a pending child of it, so it is reparented rather than deleted (see
	// reparentDraftsForPage). Both cascades run inside this transaction.
	if draftErr := s.deleteDraftsForPage(tx, pageID); draftErr != nil {
		return draftErr
	}
	if draftErr := s.reparentDraftsForPage(tx, pageID, deleted.ParentID, now); draftErr != nil {
		return draftErr
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page. Promoted children are NOT pulled back
// (matching Confluence); the parent-fallback rule is documented at the restore-parent
// resolution below. Only a soft-deleted original page (OriginalId == "", DeleteAt > 0) in a
// live space is restorable.
func (s *Store) RestorePage(pageID string) (err error) {
	if pageID == "" {
		return &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Unlocked read of the page's space (immutable regardless of live/deleted state) to pick
	// the space row to lock first — space-before-page avoids deadlocking with a concurrent
	// DeleteSpace/CreatePage/DeletePage, matching every other writer in this file.
	spaceIDQuery := s.getQueryBuilder().
		Select("SpaceId").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID})
	var spaceID string
	if idErr := s.getBuilder(tx, &spaceID, spaceIDQuery); idErr != nil {
		if errors.Is(idErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(idErr, "failed to read page space for restore")
	}

	// Lock the live space first (space-before-page); a deleted space has nowhere to restore into.
	if spErr := s.lockLiveSpace(tx, spaceID); spErr != nil {
		return spErr
	}

	// Read channel/parent in-tx so the checks and the restore are atomic.
	var target struct {
		ChannelID  string
		ParentID   string
		OriginalId string
		DeleteAt   int64
	}
	// Lock the row regardless of state (live, deleted, or snapshot) so not-found vs
	// not-restorable vs already-live is decided atomically under the lock, with no pre-fetch
	// race window for the caller.
	targetQuery := s.getQueryBuilder().
		Select("ChannelId", "ParentId", "OriginalId", "DeleteAt").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID}).
		Suffix("FOR UPDATE")
	if txErr := s.getBuilder(tx, &target, targetQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(txErr, "failed to read page for restore")
	}
	// Version snapshots are historical, not live pages; restore does not apply to them.
	if target.OriginalId != "" {
		return &ErrInvalidInput{Entity: "Page", Field: "id", Value: pageID, Reason: ReasonNotRestorable}
	}
	// Already live: nothing to restore. Decided under the row lock so a concurrent restore
	// cannot turn this into a misleading not-found.
	if target.DeleteAt == 0 {
		return &ErrInvalidInput{Entity: "Page", Field: "id", Value: pageID, Reason: ReasonNotDeleted}
	}

	// Restore under the original parent if it's still live; otherwise fall back to the space
	// root rather than failing (matching Confluence — root always exists once the space is live).
	restoreParentID := target.ParentID
	if target.ParentID != "" {
		parentLive, pErr := s.tryLockLiveParent(tx, target.ParentID, spaceID)
		if pErr != nil {
			return pErr
		}
		if !parentLive {
			restoreParentID = ""
		}
	}

	// The pre-delete SortOrder is stale (its slot was reused; siblings may have changed), so
	// append at the end of the destination group under the (channelId, parentId) advisory lock
	// (via nextSortOrder) to avoid collisions.
	sortOrder, sortErr := s.nextSortOrder(tx, target.ChannelID, restoreParentID)
	if sortErr != nil {
		return sortErr
	}

	restoreQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", 0).
		Set("UpdateAt", now).
		Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
		Set("ParentId", restoreParentID).
		Set("SortOrder", sortOrder)

	restoreQuery = restoreQuery.Where(sq.And{
		sq.Eq{"Id": pageID},
		sq.Eq{"OriginalId": ""},
		sq.NotEq{"DeleteAt": 0},
	})
	result, txErr := s.execBuilder(tx, restoreQuery)
	if txErr != nil {
		return errors.Wrap(txErr, "failed to restore page")
	}
	if rowsErr := checkRowsAffected(result, "Page", pageID); rowsErr != nil {
		return rowsErr
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}

// GetPageChildren fetches direct live children of a page, ordered by the
// caller-maintained SortOrder (ascending), with CreateAt then Id as stable tie-breakers.
// limit must be > 0.
func (s *Store) GetPageChildren(pageID string, offset, limit int) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if err := requirePositiveLimit("Page", limit); err != nil {
		return nil, err
	}

	query := s.getQueryBuilder().
		Select(pageColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{"p.ParentId": pageID}).
		Where(liveNonSnapshotFilter("p.")).
		OrderBy("p.SortOrder ASC", "p.CreateAt ASC", "p.Id ASC")

	query = applyLimitOffset(query, offset, limit)

	pages := []*model.Page{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find children for page_id=%s", pageID)
	}

	return pages, nil
}

// GetPageDescendants fetches all live descendants. Returns ErrLimitExceeded rather than
// silently truncating when the subtree exceeds MaxPageDescendantsLimit (too many rows) or
// extends more than MaxPageHierarchyDepth levels below the requested page (too deep — depth
// counts edges below the page, so a direct child is depth 1; the CTE recurses one level past
// the cap so an over-deep subtree surfaces a depth > MaxPageHierarchyDepth row, not a drop).
func (s *Store) GetPageDescendants(pageID string) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := pageDescendantsCTE +
		fmt.Sprintf(" LIMIT %d", MaxPageDescendantsLimit+1)

	var rows []struct {
		model.Page
		Depth int `db:"depth"`
	}
	if err := s.selectAll(s.db, &rows, query, pageID); err != nil {
		return nil, errors.Wrapf(err, "failed to find descendants for page_id=%s", pageID)
	}
	if len(rows) > MaxPageDescendantsLimit {
		return nil, &ErrLimitExceeded{Resource: "Page descendants for page_id=" + pageID, Limit: MaxPageDescendantsLimit}
	}

	pages := make([]*model.Page, len(rows))
	for i := range rows {
		if rows[i].Depth > MaxPageHierarchyDepth {
			return nil, &ErrLimitExceeded{Resource: "Page descendants for page_id=" + pageID + " (depth)", Limit: MaxPageHierarchyDepth}
		}
		page := rows[i].Page
		pages[i] = &page
	}

	return pages, nil
}

// GetSpacePages fetches a paginated set of live pages in a space, ordered by
// CreateAt DESC, with Id DESC as a stable tie-breaker. Filters on SpaceId (not ChannelId)
// so pages from a prior soft-deleted space that shared the channel are never returned.
// limit must be > 0.
func (s *Store) GetSpacePages(spaceID string, offset, limit int) ([]*model.Page, error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "spaceID", Value: spaceID}
	}
	if err := requirePositiveLimit("Page", limit); err != nil {
		return nil, err
	}

	query := s.getQueryBuilder().
		Select(pageColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{"p.SpaceId": spaceID}).
		Where(liveNonSnapshotFilter("p.")).
		OrderBy("p.CreateAt DESC, p.Id DESC")

	query = applyLimitOffset(query, offset, limit)

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

// GetPageAncestors fetches all live ancestors of a page up to the root. Returns ErrLimitExceeded
// when the chain exceeds MaxPageHierarchyDepth ancestors (too deep), rather than truncating.
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
