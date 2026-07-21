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

// pageSummaryColumnList is the ordered metadata projection returned by page collection queries.
// Keep large content and opaque Props out of the collection path; callers fetch an individual
// Page when they need its body or search projection.
var pageSummaryColumnList = []string{
	"Id", "SpaceId", "ParentId", "Type", "Title",
	"UserId", "LastModifiedBy", "SortOrder",
	"CreateAt", "UpdateAt", "EditAt",
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

var (
	pageColumnsP        = columnsWithAlias("p", pageColumnList)
	pageSummaryColumnsP = columnsWithAlias("p", pageSummaryColumnList)
)

// liveNonSnapshotFilter is the squirrel condition marking a page as live and not a version
// snapshot (DeleteAt=0, OriginalId=""), shared by every store method that requires a live
// page. alias is the optional table alias prefix (e.g. "p." for a query aliasing DOCS_Page
// as p); pass "" for an unaliased query.
func liveNonSnapshotFilter(alias string) sq.Eq {
	return sq.Eq{alias + "DeleteAt": 0, alias + "OriginalId": ""}
}

// CreatePage inserts a new page, assigning it the next sort order among its siblings. maxDepth
// caps the page's depth in the hierarchy (root pages are depth 1); the caller owns the limit's
// value, this only enforces it atomically against the locked parent.
func (s *Store) CreatePage(page *model.Page, maxDepth int) (_ *model.Page, err error) {
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
	// Derive ChannelId from the locked space row (single source of truth) rather than trusting the caller-supplied value.
	page.ChannelId = spaceChannelID

	// Re-verify the parent is still live under the same transaction, and enforce maxDepth against
	// its locked, current depth — atomic with the insert, unlike a pre-transaction read.
	if page.ParentId != "" {
		if pErr := s.lockLiveParent(tx, page.ParentId, page.SpaceId, "Page"); pErr != nil {
			return nil, pErr
		}
		parentDepth, depthErr := s.pageDepth(tx, page.ParentId)
		if depthErr != nil {
			return nil, depthErr
		}
		if capErr := depthCapError("Page parent_id="+page.ParentId+" (create depth)", parentDepth, 0, maxDepth); capErr != nil {
			return nil, capErr
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
// writer has advanced it. When force is true the CAS is skipped, but the patch is still merged
// into the current row, so fields the patch leaves untouched keep any concurrent edit rather
// than being clobbered by a stale snapshot. lastModifiedBy records the editor.
func (s *Store) UpdatePage(pageID, spaceID string, patch *model.PagePatch, baseEditAt int64, force bool, lastModifiedBy string) (_ *model.Page, err error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "Id", Value: pageID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
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
	// read here; no concurrent writer can slip between this read and the UPDATE below. Scoped to
	// spaceID so a page relocated out of the caller's {space_id, page_id} URL by a concurrent
	// move-to-space finds no row here and reads as not-found rather than being edited under the
	// stale space.
	selectQuery := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
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

	// Keep both EditAt and UpdateAt strictly monotonic. EditAt is the CAS token for content
	// edits; UpdateAt may have been advanced beyond EditAt by a prior structural operation
	// (move, delete, restore), so we take the greater of the two as the baseline.
	now := nextMonotonic(mmmodel.GetMillis(), max(page.EditAt, page.UpdateAt))

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
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
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

// DeletePage soft-deletes a live page and promotes its live children to the page's own
// parent as a contiguous block in the page's slot so their relative order survives. It
// rejects snapshots (OriginalId != ""). The invariant that no live page sits under a
// deleted parent holds even under concurrent CreatePage calls. Returns the deleted page's
// backing ChannelId (from the locked row) so the caller can notify its channel.
func (s *Store) DeletePage(pageID, spaceID, userID string) (_ string, err error) {
	if pageID == "" {
		return "", &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return "", &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return "", errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Lock the caller's space row first — space-before-page avoids deadlocking with a concurrent
	// DeleteSpace. spaceID comes from the caller's {space_id, page_id} URL.
	if lockErr := s.lockLiveSpace(tx, spaceID); lockErr != nil {
		return "", lockErr
	}

	// Lock the target row, scoped to the caller's space: a page relocated out of the URL by a
	// concurrent move-to-space (or a stale URL) finds no row here and reads as not-found rather than
	// being deleted under the wrong space. Take its parent and sort position from the locked row.
	var deleted struct {
		ParentID  string
		SortOrder int64
		ChannelID string
		CreateAt  int64
	}
	pageLockQuery := s.getQueryBuilder().
		Select("ParentId", "SortOrder", "ChannelId", "CreateAt").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR UPDATE")
	if lockErr := s.getBuilder(tx, &deleted, pageLockQuery); lockErr != nil {
		if errors.Is(lockErr, sql.ErrNoRows) {
			return "", &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return "", errors.Wrap(lockErr, "failed to lock page for delete")
	}

	// Read the live children in canonical sibling order (SortOrder, CreateAt, Id ASC), locking
	// them FOR UPDATE; that order drives the block placement below.
	childIDsQuery := s.getQueryBuilder().
		Select("Id").
		From("DOCS_Page").
		Where(sq.Eq{"ParentId": pageID}).
		Where(liveNonSnapshotFilter("")).
		OrderBy("SortOrder ASC", "CreateAt ASC", "Id ASC").
		Suffix("FOR UPDATE")
	var childIDs []string
	if txErr := s.selectBuilder(tx, &childIDs, childIDsQuery); txErr != nil {
		return "", errors.Wrap(txErr, "failed to read children for promote")
	}

	// Place the children as a contiguous block in the deleted page's slot, keeping their
	// relative order. The space lock serializes this and SortOrder is non-unique, so the
	// renumber can't collide.
	if n := len(childIDs); n > 0 {
		// The promoted children join deleted.ParentID's sibling group. Enforce the sibling-group cap
		// that every other insertion path applies, otherwise repeated deletes of many-childed pages
		// could grow a group past MaxPageSiblingsLimit with no cap check on this path. The lock
		// serializes the count against a concurrent CreatePage/MovePage append into the same group.
		if lockErr := s.lockSiblingGroup(tx, deleted.ChannelID, deleted.ParentID); lockErr != nil {
			return "", lockErr
		}
		var destGroupCount int64
		countQuery := s.getQueryBuilder().
			Select("COUNT(*)").
			From("DOCS_Page").
			Where(sq.Eq{"ChannelId": deleted.ChannelID, "ParentId": deleted.ParentID}).
			Where(liveNonSnapshotFilter(""))
		if countErr := s.getBuilder(tx, &destGroupCount, countQuery); countErr != nil {
			return "", errors.Wrap(countErr, "failed to count destination sibling group for child promotion")
		}
		// destGroupCount currently includes pageID itself (still live pre-delete); it is replaced
		// by n promoted children.
		if capErr := siblingCapError(deleted.ParentID, destGroupCount-1, int64(n)); capErr != nil {
			return "", capErr
		}
		// Make room: the freed slot fits one but the block needs n, so shift later siblings
		// up by n-1. A single child needs no shift. Their SortOrder shifts as a side effect of the
		// promotion, not a substantive change, so UpdateAt/the optimistic-lock baseline is left
		// untouched — mirroring reindexSiblingGroup, so a client holding one of these siblings
		// doesn't get a spurious conflict on its next CAS.
		if n > 1 {
			shiftQuery := s.getQueryBuilder().
				Update("DOCS_Page").
				Set("SortOrder", sq.Expr("SortOrder + ?", n-1)).
				Where(sq.And{
					sq.Eq{"ChannelId": deleted.ChannelID, "ParentId": deleted.ParentID, "DeleteAt": 0},
					// (SortOrder, CreateAt, Id ASC) ordering: SortOrder is non-unique, so also
					// shift siblings that tie on SortOrder but sort after the deleted page on
					// the CreateAt/Id tie-breakers.
					sq.Or{
						sq.Gt{"SortOrder": deleted.SortOrder},
						sq.Expr("(SortOrder = ? AND (CreateAt > ? OR (CreateAt = ? AND Id > ?)))",
							deleted.SortOrder, deleted.CreateAt, deleted.CreateAt, pageID),
					},
				})
			if _, txErr := s.execBuilder(tx, shiftQuery); txErr != nil {
				return "", errors.Wrap(txErr, "failed to make room for child block")
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
			Set("UpdateAt", monotonicBump("UpdateAt", now)).
			Where(sq.Eq{"Id": childIDs, "ParentId": pageID}).
			Where(liveNonSnapshotFilter(""))
		result, txErr := s.execBuilder(tx, blockQuery)
		if txErr != nil {
			return "", errors.Wrap(txErr, "failed to reparent children into block")
		}
		// The children were locked FOR UPDATE, so an affected count below len(childIDs) means
		// inconsistent state (a child no longer matches) rather than benign contention. Fail
		// loudly with a plain error so the app layer maps it to 500, not the 404 a sentinel
		// would yield.
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return "", errors.Wrap(rowsErr, "failed to read rows affected for child reparent")
		}
		if rows != int64(len(childIDs)) {
			return "", errors.Errorf("expected to reparent %d children but affected %d rows after FOR UPDATE lock", len(childIDs), rows)
		}
	}

	deleteQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", now).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Set("EditAt", monotonicBump("EditAt", now)).
		Set("LastModifiedBy", userID).
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter(""))
	result, txErr := s.execBuilder(tx, deleteQuery)
	if txErr != nil {
		return "", errors.Wrap(txErr, "failed to delete page")
	}
	if rowsErr := checkRowsAffected(result, "Page", pageID); rowsErr != nil {
		return "", rowsErr
	}

	// A draft is unpublished work on the page, so deleting the page ends its life; a new-page
	// draft parented under this page is a pending child of it, so it is reparented rather than
	// deleted (see reparentDraftsForPage). Both cascades run inside this transaction.
	if draftErr := s.deleteDraftsForPage(tx, pageID, spaceID); draftErr != nil {
		return "", draftErr
	}
	if draftErr := s.reparentDraftsForPage(tx, pageID, deleted.ParentID, now); draftErr != nil {
		return "", draftErr
	}

	if err = tx.Commit(); err != nil {
		return "", errors.Wrap(err, "commit_transaction")
	}
	return deleted.ChannelID, nil
}

// RestorePage un-deletes a soft-deleted page and returns the restored page. Promoted children
// are NOT pulled back. Restore never fails on account of a deleted or now-too-deep original
// parent (see the fallback inside). Only a soft-deleted original page (OriginalId == "",
// DeleteAt > 0) in a live space is restorable. The restored page comes from the transaction's
// own locked read, so a caller never needs a second read that could fail after the commit.
func (s *Store) RestorePage(pageID, spaceID, userID string, maxDepth int) (_ *model.Page, err error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Lock the live space first (space-before-page); a deleted space has nowhere to restore into.
	if spErr := s.lockLiveSpace(tx, spaceID); spErr != nil {
		return nil, spErr
	}

	// Read the row in-tx so the checks and the restore are atomic. Scoped to spaceID so a
	// page whose {space_id, page_id} URL no longer matches its space reads as not-found. Lock the
	// row regardless of state (live, deleted, or snapshot) so not-found vs not-restorable vs
	// already-live is decided atomically under the lock, with no pre-fetch race window for the
	// caller.
	var page model.Page
	targetQuery := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
		Suffix("FOR UPDATE")
	if txErr := s.getBuilder(tx, &page, targetQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, errors.Wrap(txErr, "failed to read page for restore")
	}
	// Version snapshots are historical, not live pages; restore does not apply to them.
	if page.OriginalId != "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "id", Value: pageID, Reason: ReasonNotRestorable}
	}
	// Already live: nothing to restore. Decided under the row lock so a concurrent restore
	// cannot turn this into a misleading not-found.
	if page.DeleteAt == 0 {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "id", Value: pageID, Reason: ReasonNotDeleted}
	}

	// Restore under the original parent if it's still live and not too deep; otherwise fall back
	// to the space root rather than failing (matching Confluence — root always exists once the
	// space is live). The parent may have moved deeper since this page was deleted, so the depth
	// cap is re-checked here under the parent's lock rather than trusted from delete time.
	restoreParentID := page.ParentId
	if page.ParentId != "" {
		parentLive, pErr := s.tryLockLiveParent(tx, page.ParentId, spaceID)
		if pErr != nil {
			return nil, pErr
		}
		if !parentLive {
			restoreParentID = ""
		} else {
			parentDepth, depthErr := s.pageDepth(tx, page.ParentId)
			if depthErr != nil {
				return nil, depthErr
			}
			if parentDepth+1 > maxDepth {
				restoreParentID = ""
			}
		}
	}

	// The pre-delete SortOrder is stale (its slot was reused; siblings may have changed), so
	// append at the end of the destination group under the (channelId, parentId) advisory lock
	// (via nextSortOrder) to avoid collisions.
	sortOrder, sortErr := s.nextSortOrder(tx, page.ChannelId, restoreParentID)
	if sortErr != nil {
		return nil, sortErr
	}

	// The row is FOR UPDATE-locked above, so computing the monotonic bumps in Go from the locked
	// read writes the same values monotonicBump would, while keeping them on the returned page.
	page.DeleteAt = 0
	page.UpdateAt = nextMonotonic(now, page.UpdateAt)
	page.EditAt = nextMonotonic(now, page.EditAt)
	page.LastModifiedBy = userID
	page.ParentId = restoreParentID
	page.SortOrder = sortOrder

	restoreQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", page.DeleteAt).
		Set("UpdateAt", page.UpdateAt).
		Set("EditAt", page.EditAt).
		Set("LastModifiedBy", page.LastModifiedBy).
		Set("ParentId", page.ParentId).
		Set("SortOrder", page.SortOrder).
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"OriginalId": ""},
			sq.NotEq{"DeleteAt": 0},
		})
	result, txErr := s.execBuilder(tx, restoreQuery)
	if txErr != nil {
		return nil, errors.Wrap(txErr, "failed to restore page")
	}
	if rowsErr := checkRowsAffected(result, "Page", pageID); rowsErr != nil {
		return nil, rowsErr
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return &page, nil
}

// PageExistsInSpace reports whether pageID is a live page in spaceID, without fetching the
// row — callers that only need to 404 on a missing page avoid hauling the page body.
func (s *Store) PageExistsInSpace(pageID, spaceID string) (bool, error) {
	return s.pageExistsInSpace(s.db, pageID, spaceID)
}

// pageExistsInSpace is PageExistsInSpace against an explicit executor, so callers inside a
// transaction observe that transaction's own view (e.g. its uncommitted writes and row locks)
// rather than reading through a separate pooled connection.
func (s *Store) pageExistsInSpace(e sqlx.ExtContext, pageID, spaceID string) (bool, error) {
	if pageID == "" {
		return false, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return false, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}
	query := s.getQueryBuilder().
		Select("1").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
		Where(liveNonSnapshotFilter(""))
	var exists int
	if err := s.getBuilder(e, &exists, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, errors.Wrap(err, "failed to check page existence")
	}
	return true, nil
}

// GetPageParentInSpace returns the ParentId of the live page pageID in spaceID, without fetching
// the row — the move paths only need the current parent for their parent-change probes, so they
// avoid hauling the page body. The bool reports whether the page exists in the space.
func (s *Store) GetPageParentInSpace(pageID, spaceID string) (string, bool, error) {
	if pageID == "" {
		return "", false, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return "", false, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}
	query := s.getQueryBuilder().
		Select("ParentId").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
		Where(liveNonSnapshotFilter(""))
	var parentID string
	if err := s.getBuilder(s.db, &parentID, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, errors.Wrap(err, "failed to get page parent")
	}
	return parentID, true, nil
}

// GetPageChildren fetches metadata summaries for direct live children of a page, ordered by the
// caller-maintained SortOrder (ascending), with CreateAt then Id as stable tie-breakers.
// spaceID scopes the query: children in any other space never match. The parent's own
// liveness is not re-checked here — deletes reparent or cascade to children in the same
// transaction, so no live row can point at a deleted parent. limit must be > 0.
func (s *Store) GetPageChildren(pageID, spaceID string, offset, limit int) ([]*model.PageSummary, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}
	if err := requirePositiveLimit("Page", limit); err != nil {
		return nil, err
	}

	query := s.getQueryBuilder().
		Select(pageSummaryColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{"p.ParentId": pageID, "p.SpaceId": spaceID}).
		Where(liveNonSnapshotFilter("p.")).
		OrderBy("p.SortOrder ASC", "p.CreateAt ASC", "p.Id ASC")

	query = applyLimitOffset(query, offset, limit)

	pages := []*model.PageSummary{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find children for page_id=%s", pageID)
	}

	return pages, nil
}

// fetchDescendantRows runs the descendants CTE and its cap checks against e (a db handle or
// transaction). Returns ErrLimitExceeded rather than silently truncating when the subtree
// exceeds MaxPageDescendantsLimit (too many rows) or extends more than MaxPageHierarchyDepth
// levels below the requested page (too deep — depth counts edges below the page, so a direct
// child is depth 1; the CTE recurses one level past the cap so an over-deep subtree surfaces a
// depth > MaxPageHierarchyDepth row, not a drop).
func (s *Store) fetchDescendantRows(e sqlx.ExtContext, pageID string) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := pageDescendantsCTE +
		fmt.Sprintf(" LIMIT %d", MaxPageDescendantsLimit+1)

	var rows []struct {
		model.Page
		Depth int `db:"depth"`
	}
	if err := s.selectAll(e, &rows, query, pageID); err != nil {
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

// GetSpacePages fetches metadata summaries for a paginated set of live pages in a space, ordered
// by CreateAt DESC with Id DESC as a stable tie-breaker. Filters on SpaceId (not ChannelId) so
// pages from a prior soft-deleted space that shared the channel are never returned.
// limit must be > 0.
func (s *Store) GetSpacePages(spaceID string, offset, limit int) ([]*model.PageSummary, error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}
	if err := requirePositiveLimit("Page", limit); err != nil {
		return nil, err
	}

	query := s.getQueryBuilder().
		Select(pageSummaryColumnsP...).
		From("DOCS_Page p").
		Where(sq.Eq{"p.SpaceId": spaceID}).
		Where(liveNonSnapshotFilter("p.")).
		OrderBy("p.CreateAt DESC, p.Id DESC")

	query = applyLimitOffset(query, offset, limit)

	pages := []*model.PageSummary{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find pages for space_id=%s", spaceID)
	}

	return pages, nil
}

// PublishDraft atomically writes a page (create or update) and deletes the user's draft for
// that page in a single transaction. On the new-page path a PK collision returns ErrConflict so
// the caller can adopt the concurrent winner without a half-state. On the edit path an EditAt
// mismatch (stale optimistic-lock baseline) returns ErrConflict. In both conflict cases the whole
// transaction is rolled back.
//
// draftUpdateAt pins the draft delete to the version the caller read; a concurrent autosave rolls
// the publish back as a ReasonConcurrentAutosave conflict rather than committing older content.
func (s *Store) PublishDraft(isNewPage bool, page *model.Page, userID, spaceID string, force bool, maxDepth int, draftUpdateAt int64) (_ *model.Page, err error) {
	if page == nil {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "page", Value: nil}
	}
	if userID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "userID", Value: userID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "spaceID", Value: spaceID}
	}
	// spaceID is the space from the caller's request; the page must live in it. A mismatch means the page
	// was relocated by a concurrent move-to-space (edit path) or the caller built it for the wrong
	// space — reject rather than write under the stale/foreign space.
	if page.SpaceId != spaceID {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: page.SpaceId}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	var result *model.Page

	if isNewPage {
		// Lock the space row to serialize with DeleteSpace / RestoreSpace.
		spaceChannelID, spErr := s.lockLiveSpaceChannel(tx, page.SpaceId)
		if spErr != nil {
			return nil, spErr
		}
		page.ChannelId = spaceChannelID

		if page.ParentId != "" {
			if pErr := s.lockLiveParent(tx, page.ParentId, page.SpaceId, "Page"); pErr != nil {
				return nil, pErr
			}
			// Enforce the depth cap against the parent's locked, current depth — atomic with the
			// insert, the same guard CreatePage applies.
			parentDepth, depthErr := s.pageDepth(tx, page.ParentId)
			if depthErr != nil {
				return nil, depthErr
			}
			if capErr := depthCapError("Page parent_id="+page.ParentId+" (publish depth)", parentDepth, 0, maxDepth); capErr != nil {
				return nil, capErr
			}
		}

		sortOrder, sortErr := s.nextSortOrder(tx, page.ChannelId, page.ParentId)
		if sortErr != nil {
			return nil, sortErr
		}
		page.SortOrder = sortOrder

		page.PreSave()
		if validErr := page.IsValid(); validErr != nil {
			return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
		}

		insertQ := s.getQueryBuilder().
			Insert("DOCS_Page").
			Columns(pageColumnList...).
			Values(pageToSlice(page)...)

		if _, execErr := s.execBuilder(tx, insertQ); execErr != nil {
			if isUniqueViolation(execErr) {
				return nil, &ErrConflict{Resource: "Page id=" + page.Id}
			}
			return nil, errors.Wrap(execErr, "failed to insert page on publish")
		}
		result = page
	} else {
		// Edit path: lock the live row, apply the draft's content, CAS on EditAt.
		selectQ := s.getQueryBuilder().
			Select(pageColumnList...).
			From("DOCS_Page").
			Where(sq.Eq{"Id": page.Id, "SpaceId": page.SpaceId}).
			Where(liveNonSnapshotFilter("")).
			Suffix("FOR UPDATE")

		var current model.Page
		if txErr := s.getBuilder(tx, &current, selectQ); txErr != nil {
			if errors.Is(txErr, sql.ErrNoRows) {
				return nil, &ErrNotFound{EntityName: "Page", ID: page.Id}
			}
			return nil, errors.Wrap(txErr, "failed to lock page for publish")
		}

		// Optimistic-lock: page.EditAt carries the baseline the caller last saw. Unless force,
		// a mismatch against the locked current row is a concurrent-edit conflict.
		if !force && current.EditAt != page.EditAt {
			return nil, &ErrConflict{Resource: "Page id=" + page.Id, Reason: ReasonConcurrentEdit}
		}

		// Apply only the fields the draft carried against the locked row, preserving current's value
		// for any empty (unset) field. An empty Title/Body means "not sent" (a cleared document is
		// EmptyTipTapJSON, not ""), so a partial autosave never wipes an untouched field — and a
		// force-publish cannot revert a concurrent edit to a field this draft did not change. Props
		// follow the same rule: a non-empty map replaces, an empty/nil map preserves current's props.
		if page.Title != "" {
			current.Title = page.Title
		}
		if page.Body != "" {
			current.Body = page.Body
			current.SearchText = page.SearchText
		}
		if len(page.Props) > 0 {
			current.Props = page.Props
		}
		current.LastModifiedBy = page.LastModifiedBy
		current.PreUpdate()
		if validErr := current.IsValid(); validErr != nil {
			return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
		}

		// Keep EditAt and UpdateAt strictly monotonic, matching UpdatePage: EditAt is the CAS token
		// for content edits; UpdateAt may already be ahead of it from a prior structural op.
		now := nextMonotonic(mmmodel.GetMillis(), max(current.EditAt, current.UpdateAt))
		updateQ := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("Title", current.Title).
			Set("Body", current.Body).
			Set("SearchText", current.SearchText).
			Set("LastModifiedBy", current.LastModifiedBy).
			Set("Props", current.GetProps()).
			Set("UpdateAt", now).
			Set("EditAt", now).
			Where(sq.Eq{"Id": page.Id, "SpaceId": page.SpaceId}).
			Where(liveNonSnapshotFilter(""))

		res, execErr := s.execBuilder(tx, updateQ)
		if execErr != nil {
			return nil, errors.Wrap(execErr, "failed to update page on publish")
		}
		if raErr := checkRowsAffected(res, "Page", page.Id); raErr != nil {
			return nil, raErr
		}
		current.UpdateAt = now
		current.EditAt = now
		result = &current
	}

	// Delete the draft atomically with the page write, but only if it still holds the content the
	// caller published. A concurrent autosave bumps UpdateAt, so it matches no row and the publish
	// is rolled back — the newer draft survives and the client can publish it.
	//
	// UpdateAt is the only version token, so a bulk maintenance write that moves it without changing
	// content (a page delete reparenting a pending child draft, a move-to-space re-homing it) also
	// trips this CAS and surfaces a ReasonConcurrentAutosave conflict when no autosave occurred. The
	// failure is safe — clean rollback, no data loss, self-heals when the client republishes — so we
	// accept it rather than add a separate content-only version column for this narrow race.
	deleteDraftQ := s.getQueryBuilder().
		Delete("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "PageId": page.Id, "SpaceId": spaceID, "UpdateAt": draftUpdateAt})
	dRes, dErr := s.execBuilder(tx, deleteDraftQ)
	if dErr != nil {
		return nil, errors.Wrap(dErr, "failed to delete draft on publish")
	}
	dRows, dRowsErr := dRes.RowsAffected()
	if dRowsErr != nil {
		return nil, errors.Wrap(dRowsErr, "failed to read rows affected deleting draft on publish")
	}
	if dRows == 0 {
		return nil, &ErrConflict{Resource: "Draft page_id=" + page.Id, Reason: ReasonConcurrentAutosave}
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return result, nil
}
