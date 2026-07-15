// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// draftCycleCheckMaxDepth bounds the parent-chain walk in checkNoDraftCycle. Must be at
// least as large as the page hierarchy depth cap enforced by the app layer.
const draftCycleCheckMaxDepth = 10

// MaxDraftsPerUserPerSpace is the maximum number of draft rows a single user may hold in one
// space. Enforced atomically inside UpsertDraft after the space lock, so it holds under
// concurrent creates. The app layer may also use this constant for a fast-path pre-check.
const MaxDraftsPerUserPerSpace = 100

// maxActiveEditorsPerPage caps the number of user IDs returned by GetPageActiveEditors. A page
// with more simultaneous editors than this cap is pathological; the cap prevents unbounded result
// sets if LastActiveAt filtering alone is insufficient.
const maxActiveEditorsPerPage = 100

var draftSelectColumns = []string{
	"UserId", "SpaceId", "PageId", "ParentId", "Title", "Body", "FileIds", "Props", "CreateAt", "UpdateAt", "LastActiveAt",
}

// draftMetaColumns is the metadata column set for draft queries — Body omitted because it can be up to PageBodyMaxBytes per draft.
var draftMetaColumns = []string{
	"UserId", "SpaceId", "PageId", "ParentId", "Title", "FileIds", "Props", "CreateAt", "UpdateAt", "LastActiveAt",
}

// applyDraftLivenessFilter adds the space-liveness JOIN and page-liveness condition shared by
// the draft read queries: the space must be live, and the draft's page must be either absent
// (a new-page draft) or a live page in the draft's own space. The draft table
// must be aliased "d".
func applyDraftLivenessFilter(q sq.SelectBuilder) sq.SelectBuilder {
	return q.
		Join("DOCS_Space s ON s.Id = d.SpaceId AND s.DeleteAt = 0").
		LeftJoin("DOCS_Page p ON p.Id = d.PageId").
		Where(sq.Or{
			sq.Eq{"p.Id": nil},
			sq.And{sq.Eq{"p.DeleteAt": 0}, sq.Eq{"p.OriginalId": ""}, sq.Expr("p.SpaceId = d.SpaceId")},
		})
}

// deleteDraftsForPage hard-deletes every user's draft for pageID scoped to spaceID. Drafts have
// no soft-delete and must be cleaned up when the page is deleted. The spaceID predicate prevents
// a delete in one space from removing drafts for the same pageID in another space. Must run
// inside tx.
func (s *Store) deleteDraftsForPage(tx *sqlx.Tx, pageID, spaceID string) error {
	query := s.getQueryBuilder().
		Delete("DOCS_Draft").
		Where(sq.Eq{"PageId": pageID, "SpaceId": spaceID})
	if _, err := s.execBuilder(tx, query); err != nil {
		return errors.Wrap(err, "failed to delete page drafts")
	}
	return nil
}

// reparentDraftsForPage reparents every new-page draft pointing at pageID to newParentID,
// so drafts don't retain a soft-deleted page as their pending parent. Must run inside tx.
//
// UpdateAt uses GREATEST(now, UpdateAt+1) for the same reason as UpsertDraft: it must be a
// strictly-monotonic token so PublishDraft's CAS-delete cannot match a row that a concurrent
// autosave already advanced past this reparent.
func (s *Store) reparentDraftsForPage(tx *sqlx.Tx, pageID, newParentID string, now int64) error {
	query := s.getQueryBuilder().
		Update("DOCS_Draft").
		Set("ParentId", newParentID).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"ParentId": pageID})
	if _, err := s.execBuilder(tx, query); err != nil {
		return errors.Wrap(err, "failed to reparent page drafts")
	}
	return nil
}

// draftParentExistsTx reports whether userID has a draft for parentID in spaceID, under the
// same visibility rule as GetDraft, reading within tx so it observes uncommitted state. It locks
// the matched parent-draft row (FOR UPDATE OF d) so a concurrent DeleteDraft cannot remove the
// parent between this check and the child-draft insert. Only the draft row is locked, since the
// liveness filter LEFT JOINs the nullable page side, which cannot be a FOR UPDATE target.
func (s *Store) draftParentExistsTx(tx *sqlx.Tx, userID, spaceID, parentID string) (bool, error) {
	builder := applyDraftLivenessFilter(
		s.getQueryBuilder().
			Select("1").
			From("DOCS_Draft d"),
	).Where(sq.Eq{"d.UserId": userID, "d.SpaceId": spaceID, "d.PageId": parentID}).
		Suffix("FOR UPDATE OF d")

	var one int
	if err := s.getBuilder(tx, &one, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, errors.Wrap(err, "failed to check draft parent")
	}
	return true, nil
}

// countDraftsForUser returns the number of draft rows the user holds in spaceID, using the given executor.
func (s *Store) countDraftsForUser(e sqlx.ExtContext, userID, spaceID string) (int, error) {
	var count int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "SpaceId": spaceID})
	if err := s.getBuilder(e, &count, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_count_drafts_for_user")
	}
	return count, nil
}

// draftExistsTx reports whether a draft row keyed by (userID, pageID) currently exists, read within
// tx so it observes the transaction's own view (including the page-row lock the caller already holds).
func (s *Store) draftExistsTx(tx *sqlx.Tx, userID, pageID string) (bool, error) {
	var one int
	builder := s.getQueryBuilder().
		Select("1").
		From("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "PageId": pageID})
	switch err := s.getBuilder(tx, &one, builder); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, errors.Wrap(err, "failed to check draft existence")
	}
}

// checkNoDraftCycle walks the parent chain from startParentID through the caller's draft rows and
// returns an error if leafPageID appears anywhere in the chain (cycle) or if the total depth
// (draft chain + live-page ancestor) would exceed MaxPageHierarchyDepth. A published-page
// ancestor (no matching draft row) terminates the recursion early. Squirrel cannot express
// recursive CTEs, so raw SQL is used here.
func (s *Store) checkNoDraftCycle(tx *sqlx.Tx, userID, leafPageID, startParentID string) error {
	// The live_ancestor subquery finds the deepest node in the draft chain that has no draft row
	// (i.e. the live-page boundary). COALESCE converts NULL to '' so the struct scan never fails.
	query := fmt.Sprintf(`
WITH RECURSIVE chain(node, depth) AS (
    SELECT $1::varchar(26), 0
    UNION ALL
    SELECT d.ParentId, chain.depth + 1
    FROM DOCS_Draft d
    JOIN chain ON d.PageId = chain.node
    WHERE d.UserId = $2
      AND chain.node <> ''
      AND chain.depth < %d
)
SELECT
    COALESCE(bool_or(node = $3), false)  AS is_cycle,
    COALESCE(max(depth) >= %d, false)    AS too_deep,
    COALESCE(max(depth), 0)              AS chain_depth,
    COALESCE((
        SELECT c.node FROM chain c
        WHERE c.node <> ''
          AND NOT EXISTS (SELECT 1 FROM DOCS_Draft d2 WHERE d2.UserId = $2 AND d2.PageId = c.node)
        ORDER BY c.depth DESC LIMIT 1
    ), '')                               AS live_ancestor
FROM chain`, draftCycleCheckMaxDepth, draftCycleCheckMaxDepth)

	var result struct {
		IsCycle      bool   `db:"is_cycle"`
		TooDeep      bool   `db:"too_deep"`
		ChainDepth   int    `db:"chain_depth"`
		LiveAncestor string `db:"live_ancestor"`
	}
	if err := s.get(tx, &result, query, startParentID, userID, leafPageID); err != nil {
		return errors.Wrap(err, "cycle check: failed to read ancestor chain")
	}
	if result.IsCycle {
		return &ErrInvalidInput{Entity: "Draft", Field: "ParentId", Value: startParentID, Reason: ReasonDraftCycle}
	}
	if result.TooDeep {
		return &ErrInvalidInput{Entity: "Draft", Field: "ParentId", Value: startParentID, Reason: ReasonDraftTooDeep}
	}
	// Include the live-page ancestor's depth: a draft chain valid on its own can still exceed
	// the publishing limit when the live ancestor is already deeply nested.
	if result.LiveAncestor != "" {
		liveDepth, err := s.pageDepth(tx, result.LiveAncestor)
		if err != nil {
			return errors.Wrap(err, "cycle check: failed to read live ancestor depth")
		}
		// liveDepth counts the ancestor itself; +1 for the new leaf being validated.
		if liveDepth+result.ChainDepth+1 > draftCycleCheckMaxDepth {
			return &ErrInvalidInput{Entity: "Draft", Field: "ParentId", Value: startParentID, Reason: ReasonDraftTooDeep}
		}
	}
	return nil
}

// UpsertDraft creates or replaces the draft keyed by (UserId, PageId). It fills in defaults and
// rejects an invalid draft itself, so the caller need not prepare or validate it beforehand.
//
// An autosave may carry only the fields the editor changed, so on the update path an empty
// ParentId, Title, Body, or FileIds means "not sent", not "cleared", and the stored value is kept (a
// cleared document is EmptyTipTapJSON, not ""). Props are merged key-wise over the stored map.
// CreateAt keeps the existing row's original value. UpdateAt is bumped strictly monotonically
// (GREATEST(incoming, stored+1)), so it is a collision-free version token: publish CAS-deletes the
// draft on this value, and two saves within the same millisecond can no longer share it. All of this
// happens inside the single upsert statement, so two concurrent autosaves cannot lose a field by
// merging against a stale read. The stored row is returned.
//
// parentID encodes the write intent for the ParentId column: nil means "omitted — preserve the
// existing stored parent", a pointer to "" means "clear to root", and a pointer to a valid ID
// means "set to that ID". The draft struct's own ParentId field is not used on the write path.
//
// The draft's space must be live; the PageId must belong to the draft's space.
// fileIDs encodes the write intent for the FileIds column: nil means "omitted — preserve the
// existing stored value", a pointer to an empty slice means "clear to no attachments", and a
// pointer to a non-empty slice means "replace with these IDs". This mirrors parentID's
// preserve/clear/set semantics.
func (s *Store) UpsertDraft(draft *model.Draft, parentID *string, fileIDs *mmmodel.StringArray) (_ *model.Draft, err error) {
	if draft == nil {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "draft", Value: nil}
	}

	// parentIDParam is the SQL-level parameter: nil → SQL NULL (preserve on conflict), non-nil
	// → the explicit value (stored via COALESCE on INSERT, used directly on UPDATE).
	var parentIDParam any
	if parentID != nil {
		parentIDParam = *parentID
	}

	// fileIDsParam follows the same nil/non-nil semantics as parentIDParam.
	var fileIDsParam any
	if fileIDs != nil {
		fileIDsParam = mmmodel.ArrayToJSON([]string(*fileIDs))
	}

	draft.PreSave()
	if validErr := draft.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	if lockErr := s.lockLiveSpace(tx, draft.SpaceId); lockErr != nil {
		return nil, lockErr
	}

	// Quota check: enforce MaxDraftsPerUserPerSpace atomically inside the space lock so
	// concurrent CreateSpaceDraft calls in the same space cannot both pass a stale pre-check
	// and each insert a row that pushes the total past the cap.
	// Skip the check on the UPDATE path (existing draft) to avoid an unnecessary count query
	// on every autosave.
	isExisting, existErr := s.draftExistsTx(tx, draft.UserId, draft.PageId)
	if existErr != nil {
		return nil, existErr
	}
	if !isExisting {
		count, countErr := s.countDraftsForUser(tx, draft.UserId, draft.SpaceId)
		if countErr != nil {
			return nil, countErr
		}
		if count >= MaxDraftsPerUserPerSpace {
			return nil, &ErrLimitExceeded{Resource: "Draft", Limit: MaxDraftsPerUserPerSpace, Reason: ReasonDraftQuotaExceeded}
		}
	}

	var page struct {
		SpaceID    string
		DeleteAt   int64
		OriginalId string
		EditAt     int64
	}
	pageLockQuery := s.getQueryBuilder().
		Select("SpaceId", "DeleteAt", "OriginalId", "EditAt").
		From("DOCS_Page").
		Where(sq.Eq{"Id": draft.PageId}).
		Suffix("FOR UPDATE")
	switch pErr := s.getBuilder(tx, &page, pageLockQuery); {
	case pErr == nil:
		// A page row exists: the draft edits it, so it must be a live page in the
		// draft's own space.
		if page.DeleteAt != 0 || page.OriginalId != "" || page.SpaceID != draft.SpaceId {
			return nil, &ErrInvalidInput{Entity: "Draft", Field: "PageId", Value: draft.PageId}
		}
		// Refuse to resurrect a draft a concurrent publish already consumed. When this autosave's
		// edit-session baseline is behind the page's current EditAt, the page advanced under it (a
		// publish or another edit). A still-existing draft row may keep saving — the conflict is
		// deferred to publish — but if no row exists, this upsert would re-INSERT a phantom draft
		// that a just-committed publish deleted, so reject it as a stale edit instead. Holding the
		// page row FOR UPDATE serializes this with PublishDraft's own draft delete, so the existence
		// check is stable within the transaction.
		conflictReason := ""
		if base, ok := draft.EditBaseline(); ok && page.EditAt > base {
			conflictReason = ReasonConcurrentEdit
		} else if !ok {
			// New-page autosave: no optimistic-lock baseline was set. The page row now exists,
			// which means a concurrent publish claimed this page id. If the draft no longer
			// exists (publish deleted it), reject rather than resurrect it — a re-INSERT here
			// would leave stale recoverable content and ghost presence behind. Holding the page
			// row FOR UPDATE serializes this check with PublishDraft's draft delete.
			conflictReason = ReasonConcurrentAutosave
		}
		if conflictReason != "" {
			// Re-read draft existence now that the page row is locked: a concurrent PublishDraft
			// may have committed (deleting the draft) between the pre-lock draftExistsTx above
			// and this point, making the earlier isExisting result stale.
			isExistingNow, reErr := s.draftExistsTx(tx, draft.UserId, draft.PageId)
			if reErr != nil {
				return nil, reErr
			}
			if !isExistingNow {
				return nil, &ErrConflict{Resource: "Draft page_id=" + draft.PageId, Reason: conflictReason}
			}
		}
	case errors.Is(pErr, sql.ErrNoRows):
		// New-page draft: no page row to lock.
	default:
		return nil, errors.Wrap(pErr, "failed to lock page for draft upsert")
	}

	// Re-validate the parent under the same transaction. A parent is valid when it is a live
	// page in the draft's space (see tryLockLiveParent) or a draft of the same user in the same
	// space — a child draft may sit under a not-yet-published draft parent, and publish gates
	// on the parent being published. Anything else is rejected.
	// A nil parentID means "preserve existing"; it skips validation. An explicit "" clears to root
	// and also skips validation. Only a non-empty parentID needs the liveness and cycle checks.
	if parentID != nil && *parentID != "" {
		ok, parentErr := s.tryLockLiveParent(tx, *parentID, draft.SpaceId)
		if parentErr != nil {
			return nil, parentErr
		}
		if !ok {
			ok, parentErr = s.draftParentExistsTx(tx, draft.UserId, draft.SpaceId, *parentID)
			if parentErr != nil {
				return nil, parentErr
			}
		}
		if !ok {
			return nil, &ErrInvalidInput{Entity: "Draft", Field: "ParentId", Value: *parentID, Reason: ReasonParentNotLive}
		}
		if cycleErr := s.checkNoDraftCycle(tx, draft.UserId, draft.PageId, *parentID); cycleErr != nil {
			return nil, cycleErr
		}
	}

	// parentIDParam is SQL NULL when parentID is nil (preserve on conflict), and the dereferenced
	// string otherwise. The COALESCE in VALUES ensures NOT NULL is satisfied on INSERT; the CASE in
	// the ON CONFLICT clause reads the original bound parameter (not EXCLUDED.ParentId) to
	// distinguish nil ("omit, preserve") from "" ("explicit clear to root").
	builder := s.getQueryBuilder().
		Insert("DOCS_Draft").
		Columns(draftSelectColumns...).
		Values(draft.UserId, draft.SpaceId, draft.PageId, sq.Expr("COALESCE(?::varchar(26), '')", parentIDParam), draft.Title, draft.Body, sq.Expr("COALESCE(?::text, '[]')", fileIDsParam), draft.GetProps(), draft.CreateAt, draft.UpdateAt, draft.LastActiveAt).
		Suffix(`ON CONFLICT (UserId, PageId) DO UPDATE SET
			SpaceId = DOCS_Draft.SpaceId,
			ParentId = CASE WHEN ?::varchar(26) IS NULL THEN DOCS_Draft.ParentId ELSE EXCLUDED.ParentId END,
			Title = COALESCE(NULLIF(EXCLUDED.Title, ''), DOCS_Draft.Title),
			Body = COALESCE(NULLIF(EXCLUDED.Body, ''), DOCS_Draft.Body),
			FileIds = CASE WHEN ?::text IS NULL THEN DOCS_Draft.FileIds ELSE EXCLUDED.FileIds END,
			Props = DOCS_Draft.Props || EXCLUDED.Props,
			UpdateAt = GREATEST(EXCLUDED.UpdateAt, DOCS_Draft.UpdateAt + 1),
			LastActiveAt = GREATEST(EXCLUDED.LastActiveAt, DOCS_Draft.LastActiveAt)
		RETURNING `+strings.Join(draftSelectColumns, ", "), parentIDParam, fileIDsParam)

	// Read the stored row back: the omitted-field preserve and the props merge happen in the
	// statement above, so the returned row — not the caller's struct — is the saved draft.
	var stored model.Draft
	if cErr := s.getBuilder(tx, &stored, builder); cErr != nil {
		return nil, errors.Wrap(cErr, "unable_to_upsert_draft")
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return &stored, nil
}

// GetDraft returns the draft keyed by (userID, pageID), or ErrNotFound. It is gated the same
// way as GetDraftsForSpace: the draft is returned only when its space is live and its page is
// either not yet created (a new-page draft) or a live page in the same space.
func (s *Store) GetDraft(userID, pageID string) (*model.Draft, error) {
	if userID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "pageId", Value: pageID}
	}

	builder := applyDraftLivenessFilter(
		s.getQueryBuilder().
			Select(columnsWithAlias("d", draftSelectColumns)...).
			From("DOCS_Draft d"),
	).Where(sq.Eq{"d.UserId": userID, "d.PageId": pageID})

	var draft model.Draft
	if err := s.getBuilder(s.db, &draft, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Draft", ID: pageID}
		}
		return nil, errors.Wrap(err, "unable_to_get_draft")
	}

	return &draft, nil
}

// DeleteDraft removes the draft keyed by (userID, pageID), or returns ErrNotFound.
func (s *Store) DeleteDraft(userID, pageID string) error {
	if userID == "" {
		return &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if pageID == "" {
		return &ErrInvalidInput{Entity: "Draft", Field: "pageId", Value: pageID}
	}

	builder := s.getQueryBuilder().
		Delete("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "PageId": pageID})

	result, err := s.execBuilder(s.db, builder)
	if err != nil {
		return errors.Wrap(err, "unable_to_delete_draft")
	}

	return checkRowsAffected(result, "Draft", pageID)
}

// DeleteDraftVersion deletes the draft keyed by (userID, pageID) only if its UpdateAt still equals
// expectedUpdateAt. It returns true when a row was deleted, and false — without error — when the
// version no longer matches (a newer autosave exists and must be left intact) or no draft exists.
// Use it to discard a draft the caller believes it has finished with, without clobbering a
// concurrent autosave that landed in the meantime.
func (s *Store) DeleteDraftVersion(userID, pageID string, expectedUpdateAt int64) (bool, error) {
	if userID == "" {
		return false, &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if pageID == "" {
		return false, &ErrInvalidInput{Entity: "Draft", Field: "pageId", Value: pageID}
	}

	builder := s.getQueryBuilder().
		Delete("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "PageId": pageID, "UpdateAt": expectedUpdateAt})

	result, err := s.execBuilder(s.db, builder)
	if err != nil {
		return false, errors.Wrap(err, "unable_to_delete_draft_version")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, errors.Wrap(err, "unable_to_read_rows_affected_delete_draft_version")
	}
	return rows > 0, nil
}

// DeleteDraftReparenting atomically reparents the calling user's child drafts (those with
// ParentId = pageID) to the deleted draft's own parent, then deletes the draft keyed by
// (userID, pageID). This prevents child drafts from holding a dangling parent after a discard.
// Returns ErrNotFound when no draft exists for (userID, pageID). pageWasLive is true when the
// deleted draft was an edit draft (the page exists as a live page), false for new-page drafts;
// callers use this to decide whether a presence broadcast is needed.
func (s *Store) DeleteDraftReparenting(userID, pageID string) (pageWasLive bool, err error) {
	if userID == "" {
		return false, &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if pageID == "" {
		return false, &ErrInvalidInput{Entity: "Draft", Field: "pageId", Value: pageID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return false, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the draft row and read its parent and space for reparenting.
	var draft struct {
		ParentId string
		SpaceId  string
	}
	lockQ := s.getQueryBuilder().
		Select("ParentId", "SpaceId").
		From("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "PageId": pageID}).
		Suffix("FOR UPDATE")
	switch lockErr := s.getBuilder(tx, &draft, lockQ); {
	case lockErr == nil:
	case errors.Is(lockErr, sql.ErrNoRows):
		return false, &ErrNotFound{EntityName: "Draft", ID: pageID}
	default:
		return false, errors.Wrap(lockErr, "failed to lock draft for delete")
	}

	// Reparent child drafts only when discarding a new-page draft. For edit drafts (page is live),
	// children's ParentId still points at a valid live page and must not be changed.
	pageIsLive, liveErr := s.PageExistsInSpace(pageID, draft.SpaceId)
	if liveErr != nil {
		return false, errors.Wrap(liveErr, "failed to check page liveness for reparenting")
	}
	if !pageIsLive {
		now := mmmodel.GetMillis()
		reparentQ := s.getQueryBuilder().
			Update("DOCS_Draft").
			Set("ParentId", draft.ParentId).
			Set("UpdateAt", monotonicBump("UpdateAt", now)).
			Where(sq.Eq{"UserId": userID, "ParentId": pageID})
		if _, rErr := s.execBuilder(tx, reparentQ); rErr != nil {
			return false, errors.Wrap(rErr, "failed to reparent child drafts")
		}
	}

	deleteQ := s.getQueryBuilder().
		Delete("DOCS_Draft").
		Where(sq.Eq{"UserId": userID, "PageId": pageID})
	result, dErr := s.execBuilder(tx, deleteQ)
	if dErr != nil {
		return false, errors.Wrap(dErr, "unable_to_delete_draft")
	}
	if err = checkRowsAffected(result, "Draft", pageID); err != nil {
		return false, err
	}

	if err = tx.Commit(); err != nil {
		return false, errors.Wrap(err, "commit_transaction")
	}
	return pageIsLive, nil
}

// GetDraftsForSpace returns the user's drafts in the given space, most-recently-updated first,
// with Body omitted (metadata only — see draftMetaColumns). Results pass applyDraftLivenessFilter,
// so a soft-deleted space lists no drafts (they survive the soft-delete and reappear after
// RestoreSpace) and a draft whose page is soft-deleted is excluded from results. Results are
// paginated via offset/limit (see applyLimitOffset); callers must pass a positive limit.
func (s *Store) GetDraftsForSpace(userID, spaceID string, offset, limit int) ([]*model.DraftSummary, error) {
	if userID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "spaceId", Value: spaceID}
	}
	if err := requirePositiveLimit("Draft", limit); err != nil {
		return nil, err
	}

	builder := applyDraftLivenessFilter(
		s.getQueryBuilder().
			Select(columnsWithAlias("d", draftMetaColumns)...).
			From("DOCS_Draft d"),
	).
		Where(sq.Eq{"d.UserId": userID, "d.SpaceId": spaceID}).
		OrderBy("d.UpdateAt DESC, d.PageId")
	builder = applyLimitOffset(builder, offset, limit)

	drafts := make([]*model.DraftSummary, 0, limit)
	if err := s.selectBuilder(s.db, &drafts, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_drafts_for_space")
	}

	return drafts, nil
}

// CountDraftsForUser returns the number of draft rows the user owns in the given space.
// It counts all draft rows regardless of page liveness, so it reflects the true storage usage
// for quota enforcement.
func (s *Store) CountDraftsForUser(userID, spaceID string) (int, error) {
	if userID == "" {
		return 0, &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if spaceID == "" {
		return 0, &ErrInvalidInput{Entity: "Draft", Field: "spaceId", Value: spaceID}
	}
	return s.countDraftsForUser(s.db, userID, spaceID)
}

// GetPageActiveEditors returns the user IDs who last saved a draft on the page in spaceID at or
// after minActiveAt — users recently editing it. Presence is derived from the shared DOCS_Draft
// table so it is consistent across all cluster nodes.
//
// The result is scoped to spaceID: a draft is keyed by (UserId, PageId) without a space, and an
// unpublished new-page draft has no page row to bound it, so two users in different spaces holding
// a draft at the same reserved page id would otherwise appear in each other's presence set. Scoping
// on the draft's own SpaceId keeps a broadcast to one space from disclosing the other space's editor.
//
// The filter is LastActiveAt, not UpdateAt: UpdateAt also moves when a bulk maintenance write
// touches the row (a page delete reparents its pending child drafts; a move-to-space re-homes
// them), which would report the draft's owner as editing a page they never opened.
func (s *Store) GetPageActiveEditors(pageID, spaceID string, minActiveAt int64) ([]string, error) {
	if pageID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "pageId", Value: pageID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "spaceId", Value: spaceID}
	}

	// Initialize non-nil: a page with no active editors is the common case, and the caller marshals
	// this straight into the active-editors REST response and the page_presence_updated WS payload,
	// which must carry [] rather than null.
	userIDs := []string{}
	builder := applyDraftLivenessFilter(
		s.getQueryBuilder().
			Select("d.UserId").
			From("DOCS_Draft d"),
	).Where(sq.Eq{"d.PageId": pageID, "d.SpaceId": spaceID}).
		Where(sq.GtOrEq{"d.LastActiveAt": minActiveAt}).
		OrderBy("d.LastActiveAt DESC").
		Limit(maxActiveEditorsPerPage)

	if err := s.selectBuilder(s.db, &userIDs, builder); err != nil {
		return nil, errors.Wrap(err, "failed to get active editors for page")
	}

	return userIDs, nil
}
