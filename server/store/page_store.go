// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"

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
	// Derive ChannelId from the locked space row (single source of truth via
	// uq_docs_space_channel_id) rather than trusting the caller-supplied value.
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
		if parentDepth+1 > maxDepth {
			return nil, &ErrInvalidInput{Entity: "Page", Field: "ParentId", Value: page.ParentId, Reason: ReasonMaxDepthExceeded}
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

// CreatePageSubtree inserts a root page plus its descendants (a DuplicatePage copy) atomically in a
// single transaction, so a failure partway through cannot leave an orphaned partial subtree behind.
// pages[0] is the root, with SpaceId/ParentId already set to the destination; every entry's Id must
// already be assigned by the caller, since pages[1:]'s ParentId references an earlier entry's Id in
// this same slice. pages[1:] must be in pre-order (mirroring GetPageDescendants). maxDepth <= 0
// disables the depth re-check.
func (s *Store) CreatePageSubtree(pages []*model.Page, maxDepth int) (_ []*model.Page, err error) {
	if len(pages) == 0 {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pages", Value: "empty"}
	}
	if len(pages)-1 > MaxPageDescendantsLimit {
		return nil, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pages[0].Id + " (size)", Limit: MaxPageDescendantsLimit}
	}
	root := pages[0]

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the space row for the lifetime of this insert so it serializes with DeleteSpace, matching
	// CreatePage.
	spaceLockQuery := s.getQueryBuilder().
		Select("ChannelId").
		From("DOCS_Space").
		Where(sq.Eq{"Id": root.SpaceId, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	var spaceChannelID string
	if spErr := s.getBuilder(tx, &spaceChannelID, spaceLockQuery); spErr != nil {
		if errors.Is(spErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: root.SpaceId}
		}
		return nil, errors.Wrap(spErr, "failed to lock space for page subtree create")
	}

	if root.ParentId != "" {
		if pErr := s.lockLiveParent(tx, root.ParentId, root.SpaceId, "Page"); pErr != nil {
			return nil, pErr
		}
	}
	// Re-validate the depth cap under the parent's lock (or, at the space root, with no lock needed
	// since there is no parent row to race against): the app layer's pre-check ran on an unlocked
	// read and can be stale if a concurrent move deepened the destination since, mirroring
	// validateMoveDestination's re-check for MovePage/MovePageToSpace.
	if maxDepth > 0 {
		destDepth := 0
		if root.ParentId != "" {
			var depthErr error
			destDepth, depthErr = s.pageDepth(tx, root.ParentId)
			if depthErr != nil {
				return nil, depthErr
			}
		}
		// Computed from the slice's own ParentId links rather than the DB: the pages are not yet
		// inserted, so pageSubtreeMaxDepth (which queries live rows) cannot see them. Checked in two
		// steps — mirroring the app layer's checkDepthCap — so the Reason carried back matches the
		// id checkDepthCap itself would have given for the same condition, not a generic fallback.
		if destDepth+1 > maxDepth {
			return nil, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + root.Id + " (depth)", Limit: maxDepth, Reason: "app.page.move.max_depth_exceeded.app_error"}
		}
		if destDepth+1+model.MaxDepthOfPreOrderedPages(pages, pages[0].Id) > maxDepth {
			return nil, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + root.Id + " (depth)", Limit: maxDepth, Reason: "app.page.move.subtree_max_depth_exceeded.app_error"}
		}
	}

	rootSortOrder, sortErr := s.nextSortOrder(tx, spaceChannelID, root.ParentId)
	if sortErr != nil {
		return nil, sortErr
	}

	// sortCounters assigns SortOrder within each new-parent group as descendants are encountered in
	// pre-order; every key is a brand-new id introduced by this call, so each group starts empty.
	sortCounters := make(map[string]int64, len(pages))
	for i, p := range pages {
		p.SpaceId = root.SpaceId
		p.ChannelId = spaceChannelID
		if i == 0 {
			p.SortOrder = rootSortOrder
		} else {
			sortCounters[p.ParentId]++
			p.SortOrder = sortCounters[p.ParentId]
		}
		p.PreSave()
		if validErr := p.IsValid(); validErr != nil {
			return nil, &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
		}
	}

	// Chunked so a max-size subtree (MaxPageDescendantsLimit rows * len(pageColumnList) bind
	// params each) can't exceed Postgres's 65535 bind-parameter limit for a single statement.
	const insertChunkSize = 1000
	for i := 0; i < len(pages); i += insertChunkSize {
		chunk := pages[i:min(i+insertChunkSize, len(pages))]
		insertQuery := s.getQueryBuilder().Insert("DOCS_Page").Columns(pageColumnList...)
		for _, p := range chunk {
			insertQuery = insertQuery.Values(pageToSlice(p)...)
		}
		if _, execErr := s.execBuilder(tx, insertQuery); execErr != nil {
			if isUniqueViolation(execErr) {
				return nil, &ErrConflict{Resource: "Page subtree root id=" + root.Id}
			}
			return nil, errors.Wrap(execErr, "failed to save Page subtree")
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return pages, nil
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

// GetPageForDuplicate fetches the source page for DuplicatePage, scoped to sourceSpaceID, plus its
// live descendants when includeChildren is set — all under one transaction. The root is locked
// against a concurrent whole-subtree move (MovePageToSpace), so that move cannot interleave with
// this read. Descendant rows are not locked, so a targeted mutation of a single descendant may land
// outside the root's snapshot.
func (s *Store) GetPageForDuplicate(pageID, sourceSpaceID string, includeChildren bool) (_ *model.Page, _ []*model.Page, err error) {
	if pageID == "" {
		return nil, nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if sourceSpaceID == "" {
		return nil, nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: sourceSpaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	sel := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": sourceSpaceID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR SHARE")
	var page model.Page
	if e := s.getBuilder(tx, &page, sel); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, nil, errors.Wrap(e, "failed to get page for duplicate")
	}

	if !includeChildren {
		if err = tx.Commit(); err != nil {
			return nil, nil, errors.Wrap(err, "commit_transaction")
		}
		return &page, nil, nil
	}

	descendants, e := s.fetchDescendantRows(tx, pageID)
	if e != nil {
		return nil, nil, e
	}

	if err = tx.Commit(); err != nil {
		return nil, nil, errors.Wrap(err, "commit_transaction")
	}
	return &page, descendants, nil
}

// UpdatePage applies patch to a live page under a row lock. baseEditAt is the EditAt the
// caller last saw. When force is false it compare-and-swaps on EditAt, returning ErrConflict
// if a concurrent writer has advanced it. When force is true the CAS is skipped, but the
// patch is still merged into the freshly-locked row, so fields the patch leaves untouched
// keep any concurrent edit rather than being clobbered by a stale snapshot. lastModifiedBy
// records the editor.
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

// MovePage reparents a live page under newParentID (nil = leave the parent unchanged) within the
// same space and positions it in the destination sibling group: newIndex nil appends it to the
// end; a non-nil newIndex places it at that zero-based position (clamped to the group) by
// renumbering the group's SortOrders. Returns the updated page. Optimistic-locked on
// expectedUpdateAt unless force. A non-root destination parent must be a live page in the same
// space, and must not be the page itself or one of its descendants (cycle guard, re-checked under
// lock).
func (s *Store) MovePage(pageID, spaceID string, newParentID *string, newIndex *int64, expectedUpdateAt int64, force bool, maxDepth int) (_ *model.Page, err error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "Id", Value: pageID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the caller's space row first (space-before-page order, matching DeletePage/CreatePage) so
	// the move serializes with concurrent creates/deletes/moves without deadlocking against them. This
	// row lock is held until commit — Postgres cannot release a single row lock mid-transaction — so
	// when newIndex is set, it also covers reindexSiblingGroup's bulk renumber below, serializing
	// every other structural mutation in the space for that duration. The group is capped at
	// MaxPageSiblingsLimit (5000) and renumbered in one statement (not a per-row loop), so this is a
	// bounded, accepted contention window rather than an unbounded stall; narrowing it further would
	// require replacing this row lock with a different concurrency-control mechanism, which is a
	// larger redesign than this fix.
	if lockErr := s.lockLiveSpace(tx, spaceID); lockErr != nil {
		return nil, lockErr
	}

	// Lock the page row, scoped to the caller's space: a page relocated out of the {space_id, page_id}
	// URL by a concurrent move-to-space (or a stale URL) finds no row here and reads as not-found
	// rather than being moved under the wrong space. The lock predicate guarantees page.SpaceId == spaceID.
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
		return nil, errors.Wrap(txErr, "failed to get page for move")
	}

	if !force && page.UpdateAt != expectedUpdateAt {
		return nil, &ErrConflict{Resource: "Page id=" + pageID + " (concurrent move)"}
	}

	destParentID := page.ParentId
	if newParentID != nil {
		destParentID = *newParentID
	}
	parentChanging := destParentID != page.ParentId

	// Nothing to do: same parent and no requested reposition.
	if !parentChanging && newIndex == nil {
		if err = tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "commit_transaction")
		}
		return &page, nil
	}

	if parentChanging && destParentID != "" {
		if destParentID == pageID {
			return nil, &ErrCircularReference{PageID: pageID, DestParentID: destParentID}
		}
		// A non-root destination parent must be a live page in the same space. Cycle and depth-cap
		// are re-checked under the held lock: the app layer's pre-check ran on an unlocked read and
		// can be stale if a concurrent same-space move deepened the destination's ancestry.
		if err = s.validateMoveDestination(tx, destParentID, page.SpaceId, pageID, maxDepth); err != nil {
			return nil, err
		}
	}

	now := nextMonotonic(mmmodel.GetMillis(), page.UpdateAt)

	if newIndex == nil {
		// Append to the destination sibling group (MAX SortOrder + 1 under the group advisory lock).
		sortOrder, sortErr := s.nextSortOrder(tx, page.ChannelId, destParentID)
		if sortErr != nil {
			return nil, sortErr
		}
		updateQuery := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("ParentId", destParentID).
			Set("SortOrder", sortOrder).
			Set("UpdateAt", now).
			Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
		result, execErr := s.execBuilder(tx, updateQuery)
		if execErr != nil {
			return nil, errors.Wrap(execErr, "failed to move page")
		}
		if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
			return nil, raErr
		}
		page.ParentId = destParentID
		page.SortOrder = sortOrder
		page.UpdateAt = now
	} else {
		// Reparent first (if changing) so the page joins the destination group, then renumber the
		// group to place it at newIndex.
		if parentChanging {
			reparent := s.getQueryBuilder().
				Update("DOCS_Page").
				Set("ParentId", destParentID).
				Set("UpdateAt", now).
				Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
			result, execErr := s.execBuilder(tx, reparent)
			if execErr != nil {
				return nil, errors.Wrap(execErr, "failed to reparent page")
			}
			if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
				return nil, raErr
			}
			page.ParentId = destParentID
		}
		if reErr := s.reindexSiblingGroup(tx, page.ChannelId, destParentID, pageID, *newIndex, now); reErr != nil {
			return nil, reErr
		}
		// Re-read the moved page for its final SortOrder/UpdateAt.
		var refreshed model.Page
		refreshQuery := s.getQueryBuilder().Select(pageColumnList...).From("DOCS_Page").Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
		if e := s.getBuilder(tx, &refreshed, refreshQuery); e != nil {
			return nil, errors.Wrap(e, "failed to re-read moved page")
		}
		page = refreshed
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return &page, nil
}

// reindexSiblingGroup renumbers a live sibling group to contiguous SortOrders (1..n) in its
// current (SortOrder, CreateAt, Id) order, but with movedPageID repositioned to newIndex. newIndex
// is clamped to the group bounds by design, not rejected: a negative index clamps to the front, an
// index past the end clamps to the end. movedPageID must already belong to the group (its ParentId
// set) before this call.
func (s *Store) reindexSiblingGroup(tx *sqlx.Tx, channelID, parentID, movedPageID string, newIndex, now int64) error {
	// Serialize with concurrent appends (nextSortOrder) into the same group so a CreatePage
	// running alongside this renumber cannot compute a stale MAX(SortOrder) and collide.
	if lockErr := s.lockSiblingGroup(tx, channelID, parentID); lockErr != nil {
		return lockErr
	}
	sel := s.getQueryBuilder().
		Select("Id").
		From("DOCS_Page").
		Where(sq.Eq{"ChannelId": channelID, "ParentId": parentID, "DeleteAt": 0, "OriginalId": ""}).
		OrderBy("SortOrder ASC", "CreateAt ASC", "Id ASC").
		Limit(MaxPageSiblingsLimit + 1).
		Suffix("FOR UPDATE")
	q, args, sqlErr := sel.ToSql()
	if sqlErr != nil {
		return errors.Wrap(sqlErr, "build sibling reindex query")
	}
	var ids []string
	if e := s.selectAll(tx, &ids, q, args...); e != nil {
		return errors.Wrap(e, "read siblings for reindex")
	}
	if len(ids) > MaxPageSiblingsLimit {
		return &ErrLimitExceeded{Resource: "Page siblings for parent_id=" + parentID, Limit: MaxPageSiblingsLimit}
	}

	cur := slices.Index(ids, movedPageID)
	if cur == -1 {
		return &ErrNotFound{EntityName: "Page", ID: movedPageID}
	}
	if newIndex < 0 {
		newIndex = 0
	}
	if newIndex > int64(len(ids)-1) {
		newIndex = int64(len(ids) - 1)
	}
	ids = slices.Delete(ids, cur, cur+1)
	ids = slices.Insert(ids, int(newIndex), movedPageID)

	// Renumber the whole group in a single UPDATE ... FROM (VALUES ...) rather than one statement per
	// sibling, bounding the group's lock-hold duration to one round-trip (the group size is capped at
	// MaxPageSiblingsLimit above). squirrel cannot express UPDATE ... FROM (VALUES ...), so the
	// statement is built directly; the ids come from the locked SELECT above (never user input) and
	// every value is bound as a parameter.
	var b strings.Builder
	// Only movedPageID's UpdateAt is bumped (GREATEST keeps it monotonic against a concurrent update
	// that already advanced it past now) — the other siblings' SortOrder shifts as a side effect of
	// the renumber, not a substantive change, so their UpdateAt/optimistic-lock baseline is untouched.
	b.WriteString("UPDATE DOCS_Page SET SortOrder = v.sort_order, UpdateAt = CASE WHEN v.id = ? THEN GREATEST(DOCS_Page.UpdateAt + 1, ?) ELSE DOCS_Page.UpdateAt END FROM (VALUES ")
	updArgs := make([]any, 0, 2+len(ids)*2)
	updArgs = append(updArgs, movedPageID, now)
	for i, id := range ids {
		if i > 0 {
			b.WriteString(", ")
		}
		// Cast the first row so the VALUES column types are unambiguous; later rows inherit them.
		if i == 0 {
			b.WriteString("(?::text, ?::bigint)")
		} else {
			b.WriteString("(?, ?)")
		}
		updArgs = append(updArgs, id, int64(i+1))
	}
	b.WriteString(") AS v(id, sort_order) WHERE DOCS_Page.Id = v.id AND DOCS_Page.DeleteAt = 0 AND DOCS_Page.OriginalId = ''")
	if _, e := s.exec(tx, b.String(), updArgs...); e != nil {
		return errors.Wrap(e, "renumber sibling group")
	}
	return nil
}

// MovePageToSpace moves a page and its entire live subtree to a different space in one
// transaction: every node's SpaceId/ChannelId is rewritten (live rows and version snapshots) and
// the moved root is reparented (parentPageID nil/"" = target root) and appended to the destination
// sibling group. The target's ChannelId is derived from its locked row, never trusted from the
// caller, so it always matches the target space's single backing channel. A subtree
// deeper than MaxPageHierarchyDepth is rejected rather than silently truncated. Cross-owner
// resources (page-comment Posts, FileInfo) are not re-homed here. The caller validates the target
// space, parent, and depth before calling; cycle-safety is re-checked here.
func (s *Store) MovePageToSpace(pageID, sourceSpaceID, targetSpaceID string, parentPageID *string, expectedUpdateAt int64, force bool, maxDepth int) (_ *model.Page, err error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "Id", Value: pageID}
	}
	if sourceSpaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: sourceSpaceID}
	}
	if targetSpaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "TargetSpaceId", Value: targetSpaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the source and target spaces before touching any page row, capturing each locked row's
	// ChannelId along the way. sourceSpaceID comes from the caller's {space_id, page_id} URL.
	// Locking source and target serializes the move against a concurrent DeleteSpace on either side;
	// ordering the two locks by id avoids deadlocking with a reverse-direction move.
	firstSpace, secondSpace := sourceSpaceID, targetSpaceID
	if firstSpace > secondSpace {
		firstSpace, secondSpace = secondSpace, firstSpace
	}
	channelIDBySpace := make(map[string]string, 2)
	firstChannelID, lockErr := s.lockLiveSpaceChannel(tx, firstSpace)
	if lockErr != nil {
		return nil, lockErr
	}
	channelIDBySpace[firstSpace] = firstChannelID
	if secondSpace != firstSpace {
		secondChannelID, lockErr := s.lockLiveSpaceChannel(tx, secondSpace)
		if lockErr != nil {
			return nil, lockErr
		}
		channelIDBySpace[secondSpace] = secondChannelID
	}
	targetChannelID := channelIDBySpace[targetSpaceID]

	// Lock the moving page, scoped to the caller's source space: a page relocated out of the URL by a
	// concurrent move-to-space (or a stale URL) finds no row here and reads as not-found rather than
	// being moved under the wrong source space. The predicate also confirms it is a live page and
	// guarantees page.SpaceId == sourceSpaceID.
	var page model.Page
	sel := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": sourceSpaceID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR UPDATE")
	if e := s.getBuilder(tx, &page, sel); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, errors.Wrap(e, "failed to get page for move-to-space")
	}

	// Optimistic-lock the moved root on its UpdateAt, mirroring MovePage: a stale baseline means a
	// concurrent edit/move advanced the page since the caller last read it. force skips the CAS.
	if !force && page.UpdateAt != expectedUpdateAt {
		return nil, &ErrConflict{Resource: "Page id=" + pageID + " (concurrent move-to-space)"}
	}

	newParentID := ""
	if parentPageID != nil {
		newParentID = *parentPageID
	}

	// Cycle guard re-checked under the held locks: the destination parent may not be the moved page
	// or one of its descendants. The app layer pre-checks this on an unlocked read; re-walking here
	// closes the TOCTOU window (for a same-space reparent, the destination shares the moving tree).
	// Locking the destination parent (live, in the target space) also closes the window where a
	// concurrent delete/move of that parent would land the subtree under a dead/cross-space parent.
	if newParentID != "" {
		if err = s.validateMoveDestination(tx, newParentID, targetSpaceID, pageID, maxDepth); err != nil {
			return nil, err
		}
	}

	// Collect the live subtree ids (the root is included — every node is re-homed).
	ids, e := s.collectLiveSubtreeIDs(tx, pageID)
	if e != nil {
		return nil, e
	}

	now := nextMonotonic(mmmodel.GetMillis(), page.UpdateAt)

	// Append the moved root to the destination sibling group. The arriving subtree is not
	// re-channeled yet, so MAX(SortOrder) sees only the pre-existing target siblings.
	sortOrder, sortErr := s.nextSortOrder(tx, targetChannelID, newParentID)
	if sortErr != nil {
		return nil, sortErr
	}
	rootUpd := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("ParentId", newParentID).
		Set("SortOrder", sortOrder).
		Set("UpdateAt", now).
		Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
	result, e := s.execBuilder(tx, rootUpd)
	if e != nil {
		return nil, errors.Wrap(e, "failed to reparent moved root")
	}
	if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
		return nil, raErr
	}

	// Rewrite SpaceId/ChannelId across the subtree (live rows + version snapshots + drafts).
	if e := s.rewriteSubtreeSpace(tx, ids, targetSpaceID, targetChannelID, now); e != nil {
		return nil, e
	}

	// Re-read the moved root inside the transaction for its final state (the root was reparented and
	// re-homed above, and the subtree rewrite bumps its UpdateAt again via GREATEST). Reading here,
	// before commit, means a read failure rolls the whole move back and surfaces as an error — unlike
	// an unlocked post-commit fetch, which could misreport an already-committed move as a failure.
	var moved model.Page
	movedQuery := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
	if e := s.getBuilder(tx, &moved, movedQuery); e != nil {
		return nil, errors.Wrap(e, "failed to re-read moved page")
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return &moved, nil
}

// collectLiveSubtreeIDs returns the ids of pageID's whole live subtree (the root included — every
// node is re-homed by MovePageToSpace), run within tx so it observes locked, uncommitted state. It
// errors — rather than silently truncating — when the subtree exceeds MaxPageDescendantsLimit or
// MaxPageHierarchyDepth (see pageSubtreeCTE for the recursion/cap mechanics), re-enforcing the app
// layer's own unlocked pre-check, which can be stale by the time this runs.
func (s *Store) collectLiveSubtreeIDs(tx *sqlx.Tx, pageID string) ([]string, error) {
	subtreeCTE := pageSubtreeCTE + fmt.Sprintf(`
		SELECT Id, depth FROM page_subtree WHERE NOT is_cycle ORDER BY depth, Id LIMIT %d`, MaxPageDescendantsLimit+2)
	var subtreeRows []struct {
		ID    string `db:"id"`
		Depth int    `db:"depth"`
	}
	if e := s.selectAll(tx, &subtreeRows, subtreeCTE, pageID); e != nil {
		return nil, errors.Wrap(e, "failed to collect page subtree")
	}
	if len(subtreeRows) == 0 {
		return nil, &ErrNotFound{EntityName: "Page", ID: pageID}
	}
	// Exclude the root from the descendant-count cap (subtreeRows always holds at least the root).
	if len(subtreeRows)-1 > MaxPageDescendantsLimit {
		return nil, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pageID + " (size)", Limit: MaxPageDescendantsLimit}
	}
	ids := make([]string, 0, len(subtreeRows))
	for _, row := range subtreeRows {
		if row.Depth > MaxPageHierarchyDepth {
			return nil, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pageID + " (depth)", Limit: MaxPageHierarchyDepth}
		}
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// rewriteSubtreeSpace re-homes ids (a subtree collected by collectLiveSubtreeIDs) onto
// targetSpaceID/targetChannelID, chunked, within tx. It rewrites SpaceId/ChannelId across three
// tables — live DOCS_Page rows, DOCS_Page version snapshots, and DOCS_Draft rows.
func (s *Store) rewriteSubtreeSpace(tx *sqlx.Tx, ids []string, targetSpaceID, targetChannelID string, now int64) error {
	const chunkSize = 1000
	for i := 0; i < len(ids); i += chunkSize {
		chunk := ids[i:min(i+chunkSize, len(ids))]

		liveUpd := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("SpaceId", targetSpaceID).
			Set("ChannelId", targetChannelID).
			// GREATEST keeps UpdateAt monotonic per row so a descendant bumped to a later value than
			// the move's now does not regress its optimistic-lock baseline.
			Set("UpdateAt", sq.Expr("GREATEST(UpdateAt + 1, ?)", now)).
			Where(sq.Eq{"Id": chunk}).
			Where(liveNonSnapshotFilter(""))
		if _, e := s.execBuilder(tx, liveUpd); e != nil {
			return errors.Wrap(e, "failed to update subtree SpaceId/ChannelId")
		}

		snapUpd := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("SpaceId", targetSpaceID).
			Set("ChannelId", targetChannelID).
			Set("UpdateAt", now).
			Where(sq.And{sq.Eq{"OriginalId": chunk}, sq.Gt{"DeleteAt": 0}})
		if _, e := s.execBuilder(tx, snapUpd); e != nil {
			return errors.Wrap(e, "failed to update subtree snapshots")
		}

		// Re-home drafts onto the target space: draft reads are scoped to the page's current space,
		// so a draft left behind in the source space would become unreadable after the move. PageId
		// matches a moved page's in-progress edit; ParentId matches a pending new-page draft parented
		// within the subtree.
		draftUpd := s.getQueryBuilder().
			Update("DOCS_Draft").
			Set("SpaceId", targetSpaceID).
			Set("UpdateAt", now).
			Where(sq.Or{sq.Eq{"PageId": chunk}, sq.Eq{"ParentId": chunk}})
		if _, e := s.execBuilder(tx, draftUpd); e != nil {
			return errors.Wrap(e, "failed to update subtree drafts")
		}
	}
	return nil
}

// lockLiveSpace FOR UPDATE-locks the live space row.
// Returns ErrNotFound if the space does not exist or is already soft-deleted.
func (s *Store) lockLiveSpace(tx *sqlx.Tx, spaceID string) error {
	_, err := s.lockLiveSpaceChannel(tx, spaceID)
	return err
}

// lockLiveSpaceChannel is lockLiveSpace but also returns the space's backing ChannelId, so a
// caller can derive it from the locked row (single source of truth via uq_docs_space_channel_id,
// mirroring CreatePage) instead of trusting a separately-supplied value.
func (s *Store) lockLiveSpaceChannel(tx *sqlx.Tx, spaceID string) (string, error) {
	query := s.getQueryBuilder().
		Select("ChannelId").
		From("DOCS_Space").
		Where(sq.Eq{"Id": spaceID, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	var channelID string
	if err := s.getBuilder(tx, &channelID, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", &ErrNotFound{EntityName: "Space", ID: spaceID}
		}
		return "", errors.Wrap(err, "failed to lock space")
	}
	return channelID, nil
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

// pageHasAncestor reports whether ancestorID is startID itself or any of its ancestors, walking
// ParentId upward within tx so it observes locked, uncommitted state. The walk is bounded by
// MaxPageHierarchyDepth; a missing/broken link ends it. Used as a cycle guard under lock by the
// move operations.
func (s *Store) pageHasAncestor(tx *sqlx.Tx, startID, ancestorID string) (bool, error) {
	var found bool
	cte := moveAncestorsCTE + `
		SELECT EXISTS(SELECT 1 FROM ancestors WHERE Id = $2 AND NOT is_cycle)`
	if err := s.get(tx, &found, cte, startID, ancestorID); err != nil {
		return false, errors.Wrap(err, "failed to walk ancestors for cycle check")
	}
	return found, nil
}

// pageDepth returns the depth of pageID from its root (a root page = 1), walking ParentId upward
// within tx so it observes locked, uncommitted state. Bounded by MaxPageHierarchyDepth.
func (s *Store) pageDepth(tx *sqlx.Tx, pageID string) (int, error) {
	var depth int
	cte := moveAncestorsCTE + `
		SELECT COALESCE(MAX(depth), 0) FROM ancestors WHERE NOT is_cycle`
	if err := s.get(tx, &depth, cte, pageID); err != nil {
		return 0, errors.Wrap(err, "failed to walk ancestors for depth check")
	}
	return depth, nil
}

// pageSubtreeMaxDepth returns the depth of the deepest live descendant below rootID relative to
// rootID (0 if it is a leaf), computed within tx so it observes locked, uncommitted state. Used to
// re-check the move depth cap under lock, closing the window where the app layer's unlocked
// pre-check sees a stale ancestor/subtree depth.
func (s *Store) pageSubtreeMaxDepth(tx *sqlx.Tx, rootID string) (int, error) {
	cte := pageSubtreeCTE + `
		SELECT COALESCE(MAX(depth), 0) FROM page_subtree WHERE NOT is_cycle`
	var maxDepth int
	if err := s.get(tx, &maxDepth, cte, rootID); err != nil {
		return 0, errors.Wrap(err, "failed to compute subtree depth")
	}
	return maxDepth, nil
}

// checkMoveDepthUnderLock re-validates, under the held lock, that placing the page (whose subtree
// extends subtreeMax levels below it) beneath destParentID would not exceed maxDepth. destParentID
// "" (a move to the space root) can never deepen the tree, so it is a no-op; maxDepth <= 0 disables
// the app-cap re-check (the MaxPageHierarchyDepth hard bound is still enforced elsewhere).
func (s *Store) checkMoveDepthUnderLock(tx *sqlx.Tx, destParentID, pageID string, maxDepth int) error {
	if maxDepth <= 0 || destParentID == "" {
		return nil
	}
	destDepth, err := s.pageDepth(tx, destParentID)
	if err != nil {
		return err
	}
	subtreeMax, err := s.pageSubtreeMaxDepth(tx, pageID)
	if err != nil {
		return err
	}
	// The moved page lands one level below its destination; its deepest descendant sits subtreeMax
	// levels below that. Checked in two steps — mirroring the app layer's checkDepthCap — so the
	// Reason carried back matches the id checkDepthCap itself would have given for the same
	// condition, not a generic fallback.
	if destDepth+1 > maxDepth {
		return &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pageID + " (move depth)", Limit: maxDepth, Reason: "app.page.move.max_depth_exceeded.app_error"}
	}
	if destDepth+1+subtreeMax > maxDepth {
		return &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pageID + " (move depth)", Limit: maxDepth, Reason: "app.page.move.subtree_max_depth_exceeded.app_error"}
	}
	return nil
}

// validateMoveDestination locks destParentID (live, in destSpaceID), then re-checks the cycle guard
// and depth cap under that lock. Shared by MovePage and MovePageToSpace, whose app-layer callers
// pre-check both on an unlocked read; re-running them here under lock closes the TOCTOU window
// against a concurrent move that reparents or deepens the destination.
func (s *Store) validateMoveDestination(tx *sqlx.Tx, destParentID, destSpaceID, pageID string, maxDepth int) error {
	if lockErr := s.lockLiveParent(tx, destParentID, destSpaceID, "Page"); lockErr != nil {
		return lockErr
	}
	cyclic, cycErr := s.pageHasAncestor(tx, destParentID, pageID)
	if cycErr != nil {
		return cycErr
	}
	if cyclic {
		return &ErrCircularReference{PageID: pageID, DestParentID: destParentID}
	}
	return s.checkMoveDepthUnderLock(tx, destParentID, pageID, maxDepth)
}

// lockSiblingGroup acquires the advisory lock for the (channelID, parentID) sibling group, held
// until the transaction ends. Every SortOrder writer into a group takes it — the append path
// (nextSortOrder) and the positional renumber (reindexSiblingGroup) — so concurrent writers
// serialize and neither computes a stale MAX(SortOrder) against the other's uncommitted rows.
// hashtextextended maps the key to a single bigint, so a hash collision only over-serializes two
// unrelated sibling groups — added contention (each group still computes its own MAX), never
// corruption, with negligible probability. Must be called inside tx.
func (s *Store) lockSiblingGroup(tx *sqlx.Tx, channelID, parentID string) error {
	lockKey := channelID + ":" + parentID
	if _, lockErr := s.exec(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); lockErr != nil {
		return errors.Wrap(lockErr, "failed to acquire advisory lock for sibling group")
	}
	return nil
}

// nextSortOrder acquires the sibling-group advisory lock and returns the next SortOrder (MAX+1).
// Must be called inside tx; the lock is held until the transaction ends. Rejects once the group is
// already at MaxPageSiblingsLimit, so a group cannot grow past the size reindexSiblingGroup can
// safely renumber in one statement.
func (s *Store) nextSortOrder(tx *sqlx.Tx, channelID, parentID string) (int64, error) {
	if lockErr := s.lockSiblingGroup(tx, channelID, parentID); lockErr != nil {
		return 0, lockErr
	}
	statsQuery := s.getQueryBuilder().
		Select("COALESCE(MAX(SortOrder), 0) AS max_order", "COUNT(*) AS cnt").
		From("DOCS_Page").
		Where(sq.Eq{"ChannelId": channelID, "ParentId": parentID, "DeleteAt": 0})
	var stats struct {
		MaxOrder int64 `db:"max_order"`
		Cnt      int64 `db:"cnt"`
	}
	if statsErr := s.getBuilder(tx, &stats, statsQuery); statsErr != nil {
		return 0, errors.Wrap(statsErr, "failed to get sibling group stats")
	}
	if stats.Cnt >= MaxPageSiblingsLimit {
		return 0, &ErrLimitExceeded{Resource: "Page siblings for parent_id=" + parentID, Limit: MaxPageSiblingsLimit}
	}
	return stats.MaxOrder + 1, nil
}

// DeletePage soft-deletes a live page and promotes its live children to the page's own
// parent as a contiguous block in the page's slot so their relative order survives. It
// rejects snapshots (OriginalId != ""). The invariant that no live page sits under a
// deleted parent holds even under concurrent CreatePage calls.
func (s *Store) DeletePage(pageID, spaceID string) (err error) {
	if pageID == "" {
		return &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Lock the caller's space row first — space-before-page avoids deadlocking with a concurrent
	// DeleteSpace. spaceID comes from the caller's {space_id, page_id} URL.
	if lockErr := s.lockLiveSpace(tx, spaceID); lockErr != nil {
		return lockErr
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
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(lockErr, "failed to lock page for delete")
	}

	// Read the live children in GetPageChildren's (SortOrder, CreateAt, Id) order, locking
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
		return errors.Wrap(txErr, "failed to read children for promote")
	}

	// Place the children as a contiguous block in the deleted page's slot, keeping their
	// relative order. The space lock serializes this and SortOrder is non-unique, so the
	// renumber can't collide.
	if n := len(childIDs); n > 0 {
		// The promoted children join deleted.ParentID's sibling group. Enforce the same cap
		// nextSortOrder/reindexSiblingGroup apply at every other point a group gains a member,
		// otherwise repeated deletes of many-childed pages could grow a group past
		// MaxPageSiblingsLimit with no cap check on this path. The lock serializes the count
		// against a concurrent CreatePage/MovePage append into the same group.
		if lockErr := s.lockSiblingGroup(tx, deleted.ChannelID, deleted.ParentID); lockErr != nil {
			return lockErr
		}
		var destGroupCount int64
		countQuery := s.getQueryBuilder().
			Select("COUNT(*)").
			From("DOCS_Page").
			Where(sq.Eq{"ChannelId": deleted.ChannelID, "ParentId": deleted.ParentID, "DeleteAt": 0})
		if countErr := s.getBuilder(tx, &destGroupCount, countQuery); countErr != nil {
			return errors.Wrap(countErr, "failed to count destination sibling group for child promotion")
		}
		// destGroupCount currently includes pageID itself (still live pre-delete); it is replaced
		// by n promoted children.
		if destGroupCount-1+int64(n) > MaxPageSiblingsLimit {
			return &ErrLimitExceeded{Resource: "Page siblings for parent_id=" + deleted.ParentID, Limit: MaxPageSiblingsLimit}
		}
		// Make room: the freed slot fits one but the block needs n, so shift later siblings
		// up by n-1. A single child needs no shift.
		if n > 1 {
			shiftQuery := s.getQueryBuilder().
				Update("DOCS_Page").
				Set("SortOrder", sq.Expr("SortOrder + ?", n-1)).
				Set("UpdateAt", sq.Expr("GREATEST(UpdateAt + 1, ?)", now)).
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
			Where(sq.Eq{"Id": childIDs, "ParentId": pageID}).
			Where(liveNonSnapshotFilter(""))
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
		Set("UpdateAt", sq.Expr("GREATEST(UpdateAt + 1, ?)", now)).
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
// (matching Confluence). The page returns under its original parent, or falls back to the
// space root if that parent is gone or restoring under it would exceed maxDepth — so un-deleting
// never fails for a deleted or now-too-deep parent. Only a soft-deleted original page
// (OriginalId == "", DeleteAt > 0) in a live space is restorable; the space is locked FOR UPDATE,
// and the parent (when non-root) is also locked, in space→parent order.
func (s *Store) RestorePage(pageID, spaceID string, maxDepth int) (err error) {
	if pageID == "" {
		return &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if spaceID == "" {
		return &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Lock the live space first (space-before-page); a deleted space has nowhere to restore into.
	if spErr := s.lockLiveSpace(tx, spaceID); spErr != nil {
		return spErr
	}

	// Read channel/parent in-tx so the checks and the restore are atomic. Scoped to spaceID so a
	// page whose {space_id, page_id} URL no longer matches its space reads as not-found. Lock the
	// row regardless of state (live, deleted, or snapshot) so not-found vs not-restorable vs
	// already-live is decided atomically under the lock, with no pre-fetch race window for the
	// caller.
	var target struct {
		ChannelID  string
		ParentID   string
		OriginalId string
		DeleteAt   int64
	}
	targetQuery := s.getQueryBuilder().
		Select("ChannelId", "ParentId", "OriginalId", "DeleteAt").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": spaceID}).
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

	// Restore under the original parent if it's still live and not too deep; otherwise fall back
	// to the space root rather than failing (matching Confluence — root always exists once the
	// space is live). The parent may have moved deeper since this page was deleted, so the depth
	// cap is re-checked here under the parent's lock rather than trusted from delete time.
	restoreParentID := target.ParentID
	if target.ParentID != "" {
		parentLive, pErr := s.tryLockLiveParent(tx, target.ParentID, spaceID)
		if pErr != nil {
			return pErr
		}
		if !parentLive {
			restoreParentID = ""
		} else {
			parentDepth, depthErr := s.pageDepth(tx, target.ParentID)
			if depthErr != nil {
				return depthErr
			}
			if parentDepth+1 > maxDepth {
				restoreParentID = ""
			}
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
		Set("UpdateAt", sq.Expr("GREATEST(UpdateAt + 1, ?)", now)).
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
	return s.fetchDescendantRows(s.db, pageID)
}

// fetchDescendantRows runs the descendants CTE and its cap checks against e (the plain db handle or
// a transaction, e.g. GetPageForDuplicate's locked snapshot), shared so both callers stay in sync on
// the query and the ErrLimitExceeded bounds.
func (s *Store) fetchDescendantRows(e sqlx.ExtContext, pageID string) ([]*model.Page, error) {
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

// GetPageAncestorDepth returns the count of live ancestors (excluding the page itself). Depth checks
// that run inside a move/create transaction use pageDepth against the locked rows instead.
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

// GetPageAncestorIDs is the {Id}-only counterpart to GetPageAncestors, for callers (MovePage/
// MovePageToSpace's cycle and depth-cap pre-checks) that only need ancestor identity/count, not
// full page content (Body/SearchText/Props) — same cap behavior as GetPageAncestors. Returned as
// []*model.Page with only Id populated so callers can share code with GetPageAncestors callers.
func (s *Store) GetPageAncestorIDs(pageID string) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := pageAncestorIDsCTE + fmt.Sprintf(" LIMIT %d", MaxPageHierarchyDepth+1)

	pages := []*model.Page{}
	if err := s.selectAll(s.db, &pages, query, pageID); err != nil {
		return nil, errors.Wrapf(err, "failed to find ancestor ids for page_id=%s", pageID)
	}
	if len(pages) > MaxPageHierarchyDepth {
		return nil, &ErrLimitExceeded{Resource: "Page ancestors for page_id=" + pageID, Limit: MaxPageHierarchyDepth}
	}

	return pages, nil
}

// GetPageDescendantIDParents is the {Id, ParentId}-only counterpart to GetPageDescendants, for
// callers (MovePage/MovePageToSpace's cycle and depth-cap pre-checks) that only need descendant
// identity/shape, not full page content — same pre-order, cap, and error behavior as
// GetPageDescendants. Returned as []*model.Page with only Id/ParentId populated so callers can
// pass the result straight to model.MaxDepthOfPreOrderedPages.
func (s *Store) GetPageDescendantIDParents(pageID string) ([]*model.Page, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}

	query := pageDescendantIDsCTE + fmt.Sprintf(" LIMIT %d", MaxPageDescendantsLimit+1)

	var rows []struct {
		model.Page
		Depth int `db:"depth"`
	}
	if err := s.selectAll(s.db, &rows, query, pageID); err != nil {
		return nil, errors.Wrapf(err, "failed to find descendant ids for page_id=%s", pageID)
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
