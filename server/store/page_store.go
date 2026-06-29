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

// UpdatePage applies patch to a live page under a row lock. baseEditAt is the EditAt the
// caller last saw. When force is false it compare-and-swaps on EditAt, returning ErrConflict
// if a concurrent writer has advanced it. When force is true the CAS is skipped, but the
// patch is still merged into the freshly-locked row, so fields the patch leaves untouched
// keep any concurrent edit rather than being clobbered by a stale snapshot. lastModifiedBy
// records the editor.
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
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
		}).
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
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
		})

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

// lockLiveSpace FOR UPDATE-locks the live space row to hold the space-before-page lock
// order (shared with CreatePage/DeleteSpace). Returns ErrNotFound if the space is gone.
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

// lockLiveParent FOR UPDATE-locks the prospective parent page, requiring it to be a live page
// (DeleteAt=0, which excludes snapshots since snapshots are always deleted) in the given space.
// A cross-space or missing parent finds no row and yields ErrInvalidInput (Field "ParentId"); entity names the calling
// resource for the error. Shared by CreatePage and UpsertDraft.
func (s *Store) lockLiveParent(tx *sqlx.Tx, parentID, spaceID, entity string) error {
	query := s.getQueryBuilder().
		Select("1").
		From("DOCS_Page").
		Where(sq.Eq{"Id": parentID, "SpaceId": spaceID, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	var exists int
	if err := s.getBuilder(tx, &exists, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &ErrInvalidInput{Entity: entity, Field: "ParentId", Value: parentID}
		}
		return errors.Wrap(err, "failed to lock parent page")
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
// parent, as a contiguous block in the page's slot so their order survives. Locks are
// space-before-page, and the page row is locked FOR UPDATE before its children are read so
// a concurrent CreatePage can't leave a live child under it. Snapshots (OriginalId != "")
// are never deleted; un-deleting the page does not pull the promoted children back (matching Confluence).
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
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
		})
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
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
		}).
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

		for i, childID := range childIDs {
			blockQuery := s.getQueryBuilder().
				Update("DOCS_Page").
				Set("ParentId", deleted.ParentID).
				Set("SortOrder", deleted.SortOrder+int64(i)).
				Set("UpdateAt", now).
				Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
				Where(sq.Eq{"Id": childID, "ParentId": pageID, "DeleteAt": 0})
			result, txErr := s.execBuilder(tx, blockQuery)
			if txErr != nil {
				return errors.Wrap(txErr, "failed to reparent child into block")
			}
			// The children were locked FOR UPDATE, so a 0-row update means inconsistent state
			// (the child no longer matches) rather than benign contention. Fail loudly with a
			// plain error so the app layer maps it to 500, not the 404 a sentinel would yield.
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return errors.Wrap(rowsErr, "failed to read rows affected for child reparent")
			}
			if rows == 0 {
				return errors.Errorf("child page %s vanished after FOR UPDATE lock during reparent", childID)
			}
		}
	}

	deleteQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", now).
		Set("UpdateAt", now).
		Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"DeleteAt": 0},
			sq.Eq{"OriginalId": ""},
		})
	result, txErr := s.execBuilder(tx, deleteQuery)
	if txErr != nil {
		return errors.Wrap(txErr, "failed to delete page")
	}
	if rowsErr := checkRowsAffected(result, "Page", pageID); rowsErr != nil {
		return rowsErr
	}

	// Hard-delete every user's draft for this page. A draft is unpublished work on the page,
	// so deleting the page ends its life (mirrors core deleting drafts associated with a
	// deleted post). Drafts are hard rows with no soft-delete.
	deleteDraftsQuery := s.getQueryBuilder().
		Delete("DOCS_Draft").
		Where(sq.Eq{"PageId": pageID})
	if _, draftErr := s.execBuilder(tx, deleteDraftsQuery); draftErr != nil {
		return errors.Wrap(draftErr, "failed to delete page drafts")
	}

	// A new-page draft parented under this page is a pending child of it. Mirror the live-child
	// promotion above and reparent it to this page's parent; otherwise it would stay readable
	// while pointing at a soft-deleted parent. The deleted page's parent is live (or root) by the
	// "no live page under a deleted parent" invariant.
	reparentDraftsQuery := s.getQueryBuilder().
		Update("DOCS_Draft").
		Set("ParentId", deleted.ParentID).
		Set("UpdateAt", now).
		Where(sq.Eq{"ParentId": pageID})
	if _, draftErr := s.execBuilder(tx, reparentDraftsQuery); draftErr != nil {
		return errors.Wrap(draftErr, "failed to reparent page drafts")
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}

// RestorePage un-deletes a soft-deleted page. Promoted children are NOT pulled back
// (matching Confluence). The page returns under its original parent, or falls back to the
// space root if that parent is gone — so un-deleting never fails for a deleted parent. Only a
// soft-deleted live row (OriginalId == "", DeleteAt > 0) in a live space is un-deletable; space and
// parent are locked FOR UPDATE in space→parent order.
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

	// Read space/channel/parent in-tx so the checks and the restore are atomic.
	var target struct {
		SpaceID   string
		ChannelID string
		ParentID  string
	}
	targetQuery := s.getQueryBuilder().
		Select("SpaceId", "ChannelId", "ParentId").
		From("DOCS_Page").
		Where(sq.And{
			sq.Eq{"Id": pageID},
			sq.Eq{"OriginalId": ""},
			sq.NotEq{"DeleteAt": 0},
		})
	if txErr := s.getBuilder(tx, &target, targetQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(txErr, "failed to read page for restore")
	}

	// Lock the live space first (space-before-page); a deleted space has nowhere to restore into.
	if spErr := s.lockLiveSpace(tx, target.SpaceID); spErr != nil {
		return spErr
	}

	// Restore under the original parent if it's still live; otherwise fall back to the space
	// root rather than failing (matching Confluence — root always exists once the space is live).
	restoreParentID := target.ParentID
	if target.ParentID != "" {
		parentLockQuery := s.getQueryBuilder().
			Select("1").
			From("DOCS_Page").
			Where(sq.And{
				sq.Eq{"Id": target.ParentID},
				sq.Eq{"SpaceId": target.SpaceID},
				sq.Eq{"DeleteAt": 0},
				sq.Eq{"OriginalId": ""},
			}).
			Suffix("FOR UPDATE")
		var parentExists int
		if pErr := s.getBuilder(tx, &parentExists, parentLockQuery); pErr != nil {
			if !errors.Is(pErr, sql.ErrNoRows) {
				return errors.Wrap(pErr, "failed to lock parent for restore")
			}
			restoreParentID = ""
		}
	}

	// The pre-delete SortOrder is stale (its slot was reused; siblings may have changed), so
	// append at the end of the destination group under CreatePage's advisory lock to avoid
	// collisions.
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

	query = applyLimitOffset(query, offset, limit)

	pages := []*model.Page{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find children for page_id=%s", pageID)
	}
	if limit <= 0 && len(pages) > MaxRowsPerQuery {
		return nil, &ErrLimitExceeded{Resource: "Page children for page_id=" + pageID, Limit: MaxRowsPerQuery}
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

	query = applyLimitOffset(query, offset, limit)

	pages := []*model.Page{}
	if err := s.selectBuilder(s.db, &pages, query); err != nil {
		return nil, errors.Wrapf(err, "failed to find pages for space_id=%s", spaceID)
	}
	if limit <= 0 && len(pages) > MaxRowsPerQuery {
		return nil, &ErrLimitExceeded{Resource: "Pages for space_id=" + spaceID, Limit: MaxRowsPerQuery}
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
