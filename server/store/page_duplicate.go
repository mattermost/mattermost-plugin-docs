// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"database/sql"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// validatePageSubtreeSlice checks the slice shape before any I/O: no nil entries, no duplicate
// IDs, and every descendant's ParentId must reference an earlier entry in the slice. The last
// rule prevents a descendant from being inserted under an unrelated external page, which would
// bypass the parent lock, sibling cap, and sort-order allocation that only cover pages[0].ParentId.
func validatePageSubtreeSlice(pages []*model.Page) error {
	seenIDs := make(map[string]struct{}, len(pages))
	for i, p := range pages {
		if p == nil {
			return &ErrInvalidInput{Entity: "Page", Field: "pages", Value: "nil entry"}
		}
		if !mmmodel.IsValidId(p.Id) {
			return &ErrInvalidInput{Entity: "Page", Field: "Id", Value: p.Id}
		}
		if i > 0 {
			if _, ok := seenIDs[p.ParentId]; !ok {
				return &ErrInvalidInput{Entity: "Page", Field: "ParentId", Value: p.ParentId}
			}
		}
		if _, dup := seenIDs[p.Id]; dup {
			return &ErrInvalidInput{Entity: "Page", Field: "Id", Value: p.Id}
		}
		seenIDs[p.Id] = struct{}{}
	}
	return nil
}

// CreatePageSubtree inserts a root page plus its descendants atomically in a single transaction,
// so a failure partway through cannot leave an orphaned partial subtree behind.
// pages[0] is the root, with SpaceId/ParentId already set to the destination; every entry's Id must
// already be assigned by the caller, since pages[1:]'s ParentId references an earlier entry's Id in
// this same slice. pages[1:] must be in pre-order. maxDepth <= 0 disables the depth re-check.
func (s *Store) CreatePageSubtree(pages []*model.Page, maxDepth int) (_ []*model.Page, err error) {
	if len(pages) == 0 {
		return nil, &ErrInvalidInput{Entity: "Page", Field: "pages", Value: "empty"}
	}
	// Validate shape (including nil-entry check) before using pages[0].Id.
	if vErr := validatePageSubtreeSlice(pages); vErr != nil {
		return nil, vErr
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
	spaceChannelID, spErr := s.lockLiveSpaceChannel(tx, root.SpaceId)
	if spErr != nil {
		return nil, spErr
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
		// inserted, so pageSubtreeMaxDepth (which queries live rows) cannot see them.
		if capErr := depthCapError("Page subtree for page_id="+root.Id+" (depth)", destDepth,
			model.MaxDepthOfPages(pages, pages[0].Id), maxDepth); capErr != nil {
			return nil, capErr
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

// GetPageForDuplicate fetches the source page, scoped to sourceSpaceID, plus its
// live descendants when includeChildren is set, as a consistent snapshot — concurrent subtree
// moves cannot interleave with the descendant reads.
func (s *Store) GetPageForDuplicate(pageID, sourceSpaceID string, includeChildren bool) (_ *model.Page, _ []*model.Page, err error) {
	if pageID == "" {
		return nil, nil, &ErrInvalidInput{Entity: "Page", Field: "pageID", Value: pageID}
	}
	if sourceSpaceID == "" {
		return nil, nil, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: sourceSpaceID}
	}

	sel := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID, "SpaceId": sourceSpaceID}).
		Where(liveNonSnapshotFilter(""))

	// A single-page copy reads one row, which a lone statement already sees consistently. Only the
	// subtree copy needs a transaction, to hold the source row against concurrent moves while its
	// descendants are read.
	if !includeChildren {
		var page model.Page
		if e := s.getBuilder(s.db, &page, sel); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, nil, &ErrNotFound{EntityName: "Page", ID: pageID}
			}
			return nil, nil, errors.Wrap(e, "failed to get page for duplicate")
		}
		return &page, nil, nil
	}

	page, descendants, err := s.getPageSubtreeForDuplicate(sel, pageID)
	if err != nil && isSerializationFailure(err) {
		// At REPEATABLE READ, if a concurrent writer is still in flight against the source row when
		// the FOR SHARE lock is requested, this transaction first blocks until that writer finishes;
		// if the writer committed a change, Postgres then aborts with a serialization failure (40001)
		// rather than let the lock read a post-snapshot version. A writer that had already committed
		// before the lock attempt fails the same way, just without the wait. Either way one retry
		// re-runs on a fresh snapshot; a second failure surfaces as a conflict the caller can retry.
		page, descendants, err = s.getPageSubtreeForDuplicate(sel, pageID)
		if err != nil && isSerializationFailure(err) {
			return nil, nil, &ErrConflict{Resource: "Page"}
		}
	}
	return page, descendants, err
}

// getPageSubtreeForDuplicate reads the source row (locked FOR SHARE) and its live descendants in
// one REPEATABLE READ transaction, so the returned subtree is a single consistent snapshot that
// concurrent structural changes cannot interleave with.
func (s *Store) getPageSubtreeForDuplicate(sel sq.SelectBuilder, pageID string) (_ *model.Page, _ []*model.Page, err error) {
	tx, err := s.db.BeginTxx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	var page model.Page
	if e := s.getBuilder(tx, &page, sel.Suffix("FOR SHARE")); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, nil, errors.Wrap(e, "failed to get page for duplicate")
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
