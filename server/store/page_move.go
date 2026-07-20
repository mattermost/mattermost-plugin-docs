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

// MovePage reparents a live page under newParentID (nil = leave the parent unchanged) within the
// same space and positions it in the destination sibling group: newIndex nil appends it to the
// end; a non-nil newIndex places it at that zero-based position (clamped to the group) by
// renumbering the group's SortOrders. Returns the updated page plus the parent it had under the
// row lock, so callers publishing an old-parent reference use the value the move actually
// displaced rather than a pre-lock read a concurrent (or forced) move may have outdated.
// Optimistic-locked on expectedUpdateAt unless force. A non-root destination parent must be a
// live page in the same space, and must not be the page itself or one of its descendants (cycle
// guard, re-checked under lock).
//
// The CAS baseline guards the page's own identity, parent, and content: a sibling whose SortOrder
// merely shifts because some other page moved into or out of its group deliberately keeps its
// UpdateAt (see reindexSiblingGroup and DeletePage's child promotion), so such a shift does not
// invalidate that sibling's baseline.
func (s *Store) MovePage(pageID, spaceID string, newParentID *string, newIndex *int64, expectedUpdateAt int64, force bool, maxDepth int) (_ *model.Page, priorParentID string, moved bool, err error) {
	if pageID == "" {
		return nil, "", false, &ErrInvalidInput{Entity: "Page", Field: "Id", Value: pageID}
	}
	if spaceID == "" {
		return nil, "", false, &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: spaceID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, "", false, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the caller's space row first (space-before-page order, matching DeletePage/CreatePage) so
	// the move serializes with concurrent creates/deletes/moves without deadlocking against them.
	// The lock is held until commit, so it also covers reindexSiblingGroup's bulk renumber below;
	// see reindexSiblingGroup for why that contention window is bounded.
	if lockErr := s.lockLiveSpace(tx, spaceID); lockErr != nil {
		return nil, "", false, lockErr
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
			return nil, "", false, &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, "", false, errors.Wrap(txErr, "failed to get page for move")
	}

	if !force && page.UpdateAt != expectedUpdateAt {
		return nil, "", false, &ErrConflict{Resource: "Page id=" + pageID + " (concurrent move)"}
	}

	lockedParentID := page.ParentId

	destParentID := page.ParentId
	if newParentID != nil {
		destParentID = *newParentID
	}
	parentChanging := destParentID != page.ParentId

	// Nothing to do: same parent and no requested reposition.
	if !parentChanging && newIndex == nil {
		if err = tx.Commit(); err != nil {
			return nil, "", false, errors.Wrap(err, "commit_transaction")
		}
		return &page, page.ParentId, false, nil
	}

	if parentChanging && destParentID != "" {
		if destParentID == pageID {
			return nil, "", false, &ErrCircularReference{PageID: pageID, DestParentID: destParentID}
		}
		// A non-root destination parent must be a live page in the same space. Cycle and depth-cap
		// are re-checked under the held lock: a concurrent same-space move could have deepened the
		// destination's ancestry between the initial check and now.
		if err = s.validateMoveDestination(tx, destParentID, page.SpaceId, pageID, nil, maxDepth); err != nil {
			return nil, "", false, err
		}
	}

	now := nextMonotonic(mmmodel.GetMillis(), page.UpdateAt)

	if newIndex == nil {
		// Append to the destination sibling group (MAX SortOrder + 1 under the group advisory lock).
		sortOrder, sortErr := s.nextSortOrder(tx, page.ChannelId, destParentID)
		if sortErr != nil {
			return nil, "", false, sortErr
		}
		updateQuery := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("ParentId", destParentID).
			Set("SortOrder", sortOrder).
			Set("UpdateAt", now).
			Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
		result, execErr := s.execBuilder(tx, updateQuery)
		if execErr != nil {
			return nil, "", false, errors.Wrap(execErr, "failed to move page")
		}
		if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
			return nil, "", false, raErr
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
				return nil, "", false, errors.Wrap(execErr, "failed to reparent page")
			}
			if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
				return nil, "", false, raErr
			}
			page.ParentId = destParentID
		}
		if reErr := s.reindexSiblingGroup(tx, page.ChannelId, destParentID, pageID, *newIndex, now); reErr != nil {
			return nil, "", false, reErr
		}
		// Re-read the moved page for its final SortOrder/UpdateAt.
		var refreshed model.Page
		refreshQuery := s.getQueryBuilder().Select(pageColumnList...).From("DOCS_Page").Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
		if e := s.getBuilder(tx, &refreshed, refreshQuery); e != nil {
			return nil, "", false, errors.Wrap(e, "failed to re-read moved page")
		}
		page = refreshed
	}

	if err = tx.Commit(); err != nil {
		return nil, "", false, errors.Wrap(err, "commit_transaction")
	}
	return &page, lockedParentID, true, nil
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
		Where(sq.Eq{"ChannelId": channelID, "ParentId": parentID}).
		Where(liveNonSnapshotFilter("")).
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
	if capErr := siblingCapError(parentID, int64(len(ids)), 0); capErr != nil {
		return capErr
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
// transaction: every live node's SpaceId/ChannelId is rewritten and the moved root is
// reparented (parentPageID nil/"" = target root) and appended to the destination sibling group.
// Alongside the moved root it returns the parent the root had under the row lock, for callers
// publishing an old-parent reference (see MovePage).
// The target's ChannelId is derived from its locked row, never trusted from the caller, so it
// always matches the target space's single backing channel. Target space, parent, depth, and
// cycle-safety are all re-validated under lock, so the move is safe regardless of concurrent
// operations between the caller's pre-checks and this call. Cross-owner resources
// (page-comment Posts, FileInfo) are not re-homed here.
func (s *Store) MovePageToSpace(pageID, sourceSpaceID, targetSpaceID, moverUserID string, parentPageID *string, expectedUpdateAt int64, force bool, maxDepth int) (_ *model.Page, priorParentID string, err error) {
	if pageID == "" {
		return nil, "", &ErrInvalidInput{Entity: "Page", Field: "Id", Value: pageID}
	}
	if sourceSpaceID == "" {
		return nil, "", &ErrInvalidInput{Entity: "Page", Field: "SpaceId", Value: sourceSpaceID}
	}
	if targetSpaceID == "" {
		return nil, "", &ErrInvalidInput{Entity: "Page", Field: "TargetSpaceId", Value: targetSpaceID}
	}
	// moverUserID scopes the draft-quota guard in rewriteSubtreeSpace; an empty or malformed value
	// would match no owner and silently skip the target-space quota check.
	if !mmmodel.IsValidId(moverUserID) {
		return nil, "", &ErrInvalidInput{Entity: "Page", Field: "MoverUserId", Value: moverUserID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, "", errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the source and target spaces before touching any page row, capturing the target's
	// ChannelId along the way. sourceSpaceID comes from the caller's {space_id, page_id} URL.
	// Locking source and target serializes the move against a concurrent DeleteSpace on either side;
	// ordering the two locks by id avoids deadlocking with a reverse-direction move.
	firstSpace, secondSpace := sourceSpaceID, targetSpaceID
	if firstSpace > secondSpace {
		firstSpace, secondSpace = secondSpace, firstSpace
	}
	channelA, lockErr := s.lockLiveSpaceChannel(tx, firstSpace)
	if lockErr != nil {
		return nil, "", lockErr
	}
	channelB := channelA
	if secondSpace != firstSpace {
		channelB, lockErr = s.lockLiveSpaceChannel(tx, secondSpace)
		if lockErr != nil {
			return nil, "", lockErr
		}
	}
	targetChannelID := channelA
	if secondSpace == targetSpaceID {
		targetChannelID = channelB
	}

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
			return nil, "", &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return nil, "", errors.Wrap(e, "failed to get page for move-to-space")
	}

	// Optimistic-lock the moved root on its UpdateAt, mirroring MovePage: a stale baseline means a
	// concurrent edit/move advanced the page since the caller last read it. force skips the CAS.
	if !force && page.UpdateAt != expectedUpdateAt {
		return nil, "", &ErrConflict{Resource: "Page id=" + pageID + " (concurrent move-to-space)"}
	}

	newParentID := ""
	if parentPageID != nil {
		newParentID = *parentPageID
	}

	// Collect the live subtree ids (the root is included — every node is re-homed) along with the
	// subtree's max relative depth, reused by the depth-cap check below instead of a second walk.
	ids, subtreeMax, e := s.collectLiveSubtreeIDs(tx, pageID)
	if e != nil {
		return nil, "", e
	}

	if newParentID != "" {
		// Cycle guard, checked under the held locks against the just-collected subtree: the
		// destination parent may not be the moved page or one of its descendants. Checked before
		// validateMoveDestination because on a cross-space move such a parent still lives in the
		// source space, so the destination-parent lock below would misreport it as a plain
		// invalid parent instead of a cycle.
		if slices.Contains(ids, newParentID) {
			return nil, "", &ErrCircularReference{PageID: pageID, DestParentID: newParentID}
		}
		// Locking the destination parent (live, in the target space) closes the window where a
		// concurrent delete/move of that parent would land the subtree under a dead/cross-space
		// parent, and re-checks cycle and depth under that lock.
		if err = s.validateMoveDestination(tx, newParentID, targetSpaceID, pageID, &subtreeMax, maxDepth); err != nil {
			return nil, "", err
		}
	}

	now := nextMonotonic(mmmodel.GetMillis(), page.UpdateAt)

	// Append the moved root to the destination sibling group. The arriving subtree is not
	// re-channeled yet, so MAX(SortOrder) sees only the pre-existing target siblings.
	sortOrder, sortErr := s.nextSortOrder(tx, targetChannelID, newParentID)
	if sortErr != nil {
		return nil, "", sortErr
	}
	rootUpd := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("ParentId", newParentID).
		Set("SortOrder", sortOrder).
		Set("UpdateAt", now).
		Where(sq.Eq{"Id": pageID}).Where(liveNonSnapshotFilter(""))
	result, e := s.execBuilder(tx, rootUpd)
	if e != nil {
		return nil, "", errors.Wrap(e, "failed to reparent moved root")
	}
	if raErr := checkRowsAffected(result, "Page", pageID); raErr != nil {
		return nil, "", raErr
	}

	// Rewrite SpaceId/ChannelId across the subtree (live rows and drafts).
	if e := s.rewriteSubtreeSpace(tx, ids, sourceSpaceID, targetSpaceID, targetChannelID, moverUserID, now); e != nil {
		return nil, "", e
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
		return nil, "", errors.Wrap(e, "failed to re-read moved page")
	}

	if err = tx.Commit(); err != nil {
		return nil, "", errors.Wrap(err, "commit_transaction")
	}
	return &moved, page.ParentId, nil
}

// collectLiveSubtreeIDs returns the ids of pageID's whole live subtree (the root included) plus
// the subtree's max relative depth (levels below the root, 0 for a leaf — the same value
// pageSubtreeMaxDepth computes, read here at no extra cost), run within tx so it observes locked,
// uncommitted state. It errors — rather than silently truncating — when the subtree exceeds
// MaxPageDescendantsLimit or MaxPageHierarchyDepth (see pageSubtreeCTE for the recursion/cap
// mechanics).
func (s *Store) collectLiveSubtreeIDs(tx *sqlx.Tx, pageID string) ([]string, int, error) {
	subtreeCTE := pageSubtreeCTE + fmt.Sprintf(`
		SELECT Id, depth FROM page_subtree ORDER BY depth, Id LIMIT %d`, MaxPageDescendantsLimit+2)
	var subtreeRows []struct {
		ID    string
		Depth int
	}
	if e := s.selectAll(tx, &subtreeRows, subtreeCTE, pageID); e != nil {
		return nil, 0, errors.Wrap(e, "failed to collect page subtree")
	}
	if len(subtreeRows) == 0 {
		return nil, 0, &ErrNotFound{EntityName: "Page", ID: pageID}
	}
	// Exclude the root from the descendant-count cap (subtreeRows always holds at least the root).
	if len(subtreeRows)-1 > MaxPageDescendantsLimit {
		return nil, 0, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pageID + " (size)", Limit: MaxPageDescendantsLimit}
	}
	ids := make([]string, 0, len(subtreeRows))
	maxRelDepth := 0
	for _, row := range subtreeRows {
		if row.Depth > MaxPageHierarchyDepth {
			return nil, 0, &ErrLimitExceeded{Resource: "Page subtree for page_id=" + pageID + " (depth)", Limit: MaxPageHierarchyDepth}
		}
		if row.Depth > maxRelDepth {
			maxRelDepth = row.Depth
		}
		ids = append(ids, row.ID)
	}
	return ids, maxRelDepth, nil
}

// rewriteSubtreeSpace re-homes the given page IDs onto
// targetSpaceID/targetChannelID, chunked, within tx. It rewrites SpaceId/ChannelId across
// live DOCS_Page rows, their version snapshots (OriginalId IN ids), and DOCS_Draft rows.
// Every user's drafts follow their page, not just the mover's: a draft is unpublished work its
// owner has not consented to lose, so a move must not destroy it as a side effect. An owner who
// is not a member of targetSpaceID simply cannot reach the draft — the space-membership check on
// every read gates it — but the content survives and returns if they gain access.
func (s *Store) rewriteSubtreeSpace(tx *sqlx.Tx, ids []string, sourceSpaceID, targetSpaceID, targetChannelID, moverUserID string, now int64) error {
	// Quota guard: count the mover's drafts that will be re-homed into targetSpaceID (those in
	// source that cover moved pages or sit under them as new-page children) and ensure adding
	// them won't exceed MaxDraftsPerUserPerSpace in the target. This count is a lower bound for
	// the total re-homed set (the cascade loop below can pick up transitively nested new-page
	// drafts), so a failure here is correct, but a pass does not guarantee the cascade is safe;
	// the cascade is bounded by DraftCycleCheckMaxDepth and the count remains low in practice.
	//
	// Only the mover is quota-checked. Other users' re-homed drafts can push them past the cap in
	// the target space, which is accepted: the cap is a soft storage bound, and re-homing moves
	// existing rows rather than creating new ones. Failing a mover's move because an unrelated
	// user sits at quota would be worse than briefly exceeding a soft cap.
	//
	// Unlike the re-home writes below, this count runs against the full id set in one query rather
	// than in chunks: the predicate is an OR of two INs, so a draft whose PageId falls in one chunk
	// and ParentId in another would be counted once per chunk. ids is bounded by
	// MaxPageDescendantsLimit, well within Postgres's parameter limit, and this is a rare move op.
	var movedDraftCount int
	movedCountQ := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_Draft").
		Where(sq.Eq{"UserId": moverUserID, "SpaceId": sourceSpaceID}).
		Where(sq.Or{sq.Eq{"PageId": ids}, sq.Eq{"ParentId": ids}})
	if err := s.getBuilder(tx, &movedDraftCount, movedCountQ); err != nil {
		return errors.Wrap(err, "failed to count mover drafts to re-home")
	}
	if movedDraftCount > 0 {
		targetDraftCount, err := s.countDraftsForUser(tx, moverUserID, targetSpaceID)
		if err != nil {
			return errors.Wrap(err, "failed to count mover drafts in target space")
		}
		if targetDraftCount+movedDraftCount > MaxDraftsPerUserPerSpace {
			return &ErrLimitExceeded{Resource: "Draft", Limit: MaxDraftsPerUserPerSpace, Reason: ReasonDraftQuotaExceeded}
		}
	}

	const chunkSize = 1000
	for i := 0; i < len(ids); i += chunkSize {
		chunk := ids[i:min(i+chunkSize, len(ids))]

		liveUpd := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("SpaceId", targetSpaceID).
			Set("ChannelId", targetChannelID).
			Set("UpdateAt", monotonicBump("UpdateAt", now)).
			Where(sq.Eq{"Id": chunk}).
			Where(liveNonSnapshotFilter(""))
		if _, e := s.execBuilder(tx, liveUpd); e != nil {
			return errors.Wrap(e, "failed to update subtree SpaceId/ChannelId")
		}

		// Version snapshots (OriginalId != "") are keyed by OriginalId, which points at a moved
		// page's Id. Re-home them so snapshot queries scoped to the target space find them.
		snapUpd := s.getQueryBuilder().
			Update("DOCS_Page").
			Set("SpaceId", targetSpaceID).
			Set("ChannelId", targetChannelID).
			Where(sq.Eq{"OriginalId": chunk})
		if _, e := s.execBuilder(tx, snapUpd); e != nil {
			return errors.Wrap(e, "failed to update subtree snapshots SpaceId/ChannelId")
		}

		// Re-home every owner's drafts for the moved pages, so a draft keeps matching its page's
		// space and stays readable to an owner who is a member of the target. UpdateAt uses
		// monotonicBump so it stays a valid CAS token even when the move and a concurrent autosave
		// share a millisecond boundary. SpaceId = sourceSpaceID prevents cross-space ID collisions
		// from re-homing unrelated drafts that happen to share a PageId or ParentId. LastActiveAt is
		// reset so a re-homed draft is not reported as an active editor in the target space until its
		// owner edits it there — otherwise a source-only owner's recent edit would surface to target
		// members through GetPageActiveEditors for the remainder of the active-editor window.
		draftUpd := s.getQueryBuilder().
			Update("DOCS_Draft").
			Set("SpaceId", targetSpaceID).
			Set("UpdateAt", monotonicBump("UpdateAt", now)).
			Set("LastActiveAt", 0).
			Where(sq.Eq{"SpaceId": sourceSpaceID}).
			Where(sq.Or{sq.Eq{"PageId": chunk}, sq.Eq{"ParentId": chunk}})
		if _, e := s.execBuilder(tx, draftUpd); e != nil {
			return errors.Wrap(e, "failed to re-home drafts for moved pages")
		}
	}

	// Cascade the space re-home to transitively-nested new-page drafts (draft B whose ParentId is
	// draft A's PageId, not a live page). The chunk loop above matched only drafts whose ParentId
	// was a live moved page; draft B is caught here. Draft nesting is same-owner only, so the join
	// pairs each draft with its parent on UserId rather than singling out the mover. Loop until
	// stable, bounded by DraftCycleCheckMaxDepth which caps the draft tree depth.
	// Squirrel cannot express UPDATE … FROM …, so the statement is built directly.
	for range DraftCycleCheckMaxDepth {
		result, e := s.exec(tx, `
			UPDATE DOCS_Draft d
			SET SpaceId = $1, UpdateAt = GREATEST(d.UpdateAt + 1, $2), LastActiveAt = 0
			FROM DOCS_Draft parent
			WHERE d.SpaceId = $3
			  AND parent.UserId = d.UserId
			  AND parent.SpaceId = $1
			  AND parent.PageId = d.ParentId`,
			targetSpaceID, now, sourceSpaceID)
		if e != nil {
			return errors.Wrap(e, "failed to cascade draft space to nested drafts")
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return errors.Wrap(rowsErr, "failed to read rows affected for nested draft cascade")
		}
		if rows == 0 {
			break
		}
	}

	return nil
}

// tryLockLiveParent FOR UPDATE-locks parentID and reports whether it is still a live page in spaceID.
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
// with ReasonParentNotLive so callers can map the condition without inspecting the field name.
// entity names the calling resource (e.g. "Page", "Draft") for the error.
func (s *Store) lockLiveParent(tx *sqlx.Tx, parentID, spaceID, entity string) error {
	ok, err := s.tryLockLiveParent(tx, parentID, spaceID)
	if err != nil {
		return err
	}
	if !ok {
		return &ErrInvalidInput{Entity: entity, Field: "ParentId", Value: parentID, Reason: ReasonParentNotLive}
	}
	return nil
}

// pageDepthAndHasAncestor walks startID's parent chain upward once within tx (observing locked,
// uncommitted state, bounded by MaxPageHierarchyDepth) and returns both startID's depth from its
// root (a root page = 1) and whether ancestorID is startID itself or any of its ancestors — the
// cycle guard and the depth cap need the same walk, so one round trip serves both.
func (s *Store) pageDepthAndHasAncestor(tx *sqlx.Tx, startID, ancestorID string) (int, bool, error) {
	var row struct {
		Depth  int  `db:"depth"`
		HasAnc bool `db:"has_anc"`
	}
	cte := moveAncestorsCTE + `
		SELECT COALESCE(MAX(depth), 0) AS depth, COALESCE(BOOL_OR(Id = $2), FALSE) AS has_anc FROM ancestors`
	if err := s.get(tx, &row, cte, startID, ancestorID); err != nil {
		return 0, false, errors.Wrap(err, "failed to walk ancestors for cycle and depth check")
	}
	return row.Depth, row.HasAnc, nil
}

// pageDepth returns the depth of pageID from its root (a root page = 1), walking ParentId upward
// within tx so it observes locked, uncommitted state. Bounded by MaxPageHierarchyDepth.
func (s *Store) pageDepth(tx *sqlx.Tx, pageID string) (int, error) {
	var depth int
	cte := moveAncestorsCTE + `
		SELECT COALESCE(MAX(depth), 0) FROM ancestors`
	if err := s.get(tx, &depth, cte, pageID); err != nil {
		return 0, errors.Wrap(err, "failed to walk ancestors for depth check")
	}
	return depth, nil
}

// pageSubtreeMaxDepth returns the depth of the deepest live descendant below rootID relative to
// rootID (0 if it is a leaf), computed within tx so it observes locked, uncommitted state.
func (s *Store) pageSubtreeMaxDepth(tx *sqlx.Tx, rootID string) (int, error) {
	cte := pageSubtreeCTE + `
		SELECT COALESCE(MAX(depth), 0) FROM page_subtree`
	var maxDepth int
	if err := s.get(tx, &maxDepth, cte, rootID); err != nil {
		return 0, errors.Wrap(err, "failed to compute subtree depth")
	}
	return maxDepth, nil
}

// depthCapError returns the ErrLimitExceeded for placing a page one level below destDepth with a
// subtree extending subtreeMax levels below it, or nil when the placement fits within maxDepth.
// Checked in two steps so the Reason distinguishes "the page itself lands too deep" from "its
// subtree would extend past the cap" — the app layer maps each Reason to a distinct message key.
func depthCapError(resource string, destDepth, subtreeMax, maxDepth int) error {
	if destDepth+1 > maxDepth {
		return &ErrLimitExceeded{Resource: resource, Limit: maxDepth, Reason: ReasonMaxDepthExceeded}
	}
	if destDepth+1+subtreeMax > maxDepth {
		return &ErrLimitExceeded{Resource: resource, Limit: maxDepth, Reason: ReasonSubtreeMaxDepthExceeded}
	}
	return nil
}

// siblingCapError returns the shared ErrLimitExceeded when a sibling group currently holding
// current live pages cannot absorb added more within MaxPageSiblingsLimit (added 0 validates the
// current size). Every path that grows or renumbers a group must apply this cap — under the
// group's advisory lock — so a group can never outgrow what reindexSiblingGroup can renumber in
// one statement.
func siblingCapError(parentID string, current, added int64) error {
	if current+added > MaxPageSiblingsLimit {
		return &ErrLimitExceeded{Resource: "Page siblings for parent_id=" + parentID, Limit: MaxPageSiblingsLimit}
	}
	return nil
}

// validateMoveDestination locks destParentID (live, in destSpaceID), then re-checks the cycle guard
// and depth cap under that lock, closing the TOCTOU window against a concurrent move that reparents
// or deepens the destination. destParentID must be non-empty (a move to the space root can never
// deepen the tree or form a cycle); maxDepth <= 0 disables the configurable depth cap (the
// MaxPageHierarchyDepth hard bound is still enforced elsewhere). subtreeMax, when non-nil, is the
// caller's already-computed max relative depth of pageID's live subtree (levels below the root);
// nil computes it here with one pageSubtreeCTE walk.
func (s *Store) validateMoveDestination(tx *sqlx.Tx, destParentID, destSpaceID, pageID string, subtreeMax *int, maxDepth int) error {
	if lockErr := s.lockLiveParent(tx, destParentID, destSpaceID, "Page"); lockErr != nil {
		return lockErr
	}
	destDepth, cyclic, walkErr := s.pageDepthAndHasAncestor(tx, destParentID, pageID)
	if walkErr != nil {
		return walkErr
	}
	if cyclic {
		return &ErrCircularReference{PageID: pageID, DestParentID: destParentID}
	}
	if maxDepth <= 0 {
		return nil
	}
	below := 0
	if subtreeMax != nil {
		below = *subtreeMax
	} else {
		var err error
		below, err = s.pageSubtreeMaxDepth(tx, pageID)
		if err != nil {
			return err
		}
	}
	return depthCapError("Page subtree for page_id="+pageID+" (move depth)", destDepth, below, maxDepth)
}

// lockSiblingGroup acquires the advisory lock for the (channelID, parentID) sibling group, held
// until the transaction ends. Every SortOrder write into a group — appends and positional renumbers —
// must take this lock so concurrent writers serialize. Must be called inside tx.
func (s *Store) lockSiblingGroup(tx *sqlx.Tx, channelID, parentID string) error {
	if lockErr := s.advisoryXactLock(tx, channelID+":"+parentID); lockErr != nil {
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
		Where(sq.Eq{"ChannelId": channelID, "ParentId": parentID}).
		Where(liveNonSnapshotFilter(""))
	var stats struct {
		MaxOrder int64 `db:"max_order"`
		Cnt      int64 `db:"cnt"`
	}
	if statsErr := s.getBuilder(tx, &stats, statsQuery); statsErr != nil {
		return 0, errors.Wrap(statsErr, "failed to get sibling group stats")
	}
	if capErr := siblingCapError(parentID, stats.Cnt, 1); capErr != nil {
		return 0, capErr
	}
	return stats.MaxOrder + 1, nil
}
