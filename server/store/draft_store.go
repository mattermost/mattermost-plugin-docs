// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

var draftSelectColumns = []string{
	"UserId", "SpaceId", "PageId", "ParentId", "Title", "Body", "FileIds", "Props", "CreateAt", "UpdateAt",
}

// draftMetaColumns omits Body: the sidebar/tree listing only needs metadata, and Body can be
// up to PageBodyMaxBytes per draft, so loading it for every row would be wasteful.
var draftMetaColumns = []string{
	"UserId", "SpaceId", "PageId", "ParentId", "Title", "FileIds", "Props", "CreateAt", "UpdateAt",
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

// UpsertDraft creates or replaces the draft keyed by (UserId, PageId). It fills in defaults and
// rejects an invalid draft itself, so the caller need not prepare or validate it beforehand.
// If a draft already exists for that key every field is overwritten (no field-level merge),
// except CreateAt, which keeps the existing row's original value.
//
// The write is transactional and follows CreatePage's space-before-page lock order: it requires
// a live space, and — when a page already exists for the draft's PageId — requires that page to
// be live and in the same space, locking it FOR UPDATE so a concurrent DeletePage cannot
// soft-delete the page (and purge this draft) underneath the write. A PageId with no page row is
// a new-page draft (a legal orphan) and is accepted without a page lock.
func (s *Store) UpsertDraft(draft *model.Draft) (_ *model.Draft, err error) {
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

	var page struct {
		SpaceID    string
		DeleteAt   int64
		OriginalId string
	}
	pageLockQuery := s.getQueryBuilder().
		Select("SpaceId", "DeleteAt", "OriginalId").
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
	case errors.Is(pErr, sql.ErrNoRows):
		// New-page draft: no page row to lock.
	default:
		return nil, errors.Wrap(pErr, "failed to lock page for draft upsert")
	}

	// The pending hierarchy parent must be a live page in the draft's space, the same liveness
	// CreatePage enforces (DeleteAt=0 excludes snapshots). Scoping the lock by
	// SpaceId keeps it within the space already locked above, so a cross-space ParentId simply
	// finds no row and is rejected. A ParentId whose page does not exist yet (a draft target with
	// no page row) is likewise rejected, matching CreatePage's parent requirement.
	if draft.ParentId != "" {
		if parentErr := s.lockLiveParent(tx, draft.ParentId, draft.SpaceId, "Draft"); parentErr != nil {
			return nil, parentErr
		}
	}

	builder := s.getQueryBuilder().
		Insert("DOCS_Draft").
		Columns(draftSelectColumns...).
		Values(draft.UserId, draft.SpaceId, draft.PageId, draft.ParentId, draft.Title, draft.Body, draft.FileIds, draft.GetProps(), draft.CreateAt, draft.UpdateAt).
		Suffix("ON CONFLICT (UserId, PageId) DO UPDATE SET SpaceId = EXCLUDED.SpaceId, ParentId = EXCLUDED.ParentId, Title = EXCLUDED.Title, Body = EXCLUDED.Body, FileIds = EXCLUDED.FileIds, Props = EXCLUDED.Props, UpdateAt = EXCLUDED.UpdateAt RETURNING CreateAt")

	// On the update path CreateAt keeps the existing row's value (it is not in the SET list),
	// which can differ from the caller-supplied one, so read the stored value back.
	var storedCreateAt int64
	if cErr := s.getBuilder(tx, &storedCreateAt, builder); cErr != nil {
		return nil, errors.Wrap(cErr, "unable_to_upsert_draft")
	}
	draft.CreateAt = storedCreateAt

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return draft, nil
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

// GetDraftsForSpace returns the user's drafts in the given space, most-recently-updated first,
// with Body omitted (metadata only — see draftMetaColumns). Results pass applyDraftLivenessFilter,
// so a soft-deleted space lists no drafts (they survive the soft-delete and reappear after
// RestoreSpace) and a draft whose page is soft-deleted is dropped rather than rendered as a
// phantom tree node. The result is capped at MaxRowsPerQuery; ErrLimitExceeded is returned
// rather than truncating when more rows match.
func (s *Store) GetDraftsForSpace(userID, spaceID string) ([]*model.Draft, error) {
	if userID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "userId", Value: userID}
	}
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Draft", Field: "spaceId", Value: spaceID}
	}

	builder := applyDraftLivenessFilter(
		s.getQueryBuilder().
			Select(columnsWithAlias("d", draftMetaColumns)...).
			From("DOCS_Draft d"),
	).
		Where(sq.Eq{"d.UserId": userID, "d.SpaceId": spaceID}).
		OrderBy("d.UpdateAt DESC").
		Limit(uint64(MaxRowsPerQuery + 1))

	drafts := []*model.Draft{}
	if err := s.selectBuilder(s.db, &drafts, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_drafts_for_space")
	}
	if len(drafts) > MaxRowsPerQuery {
		return nil, &ErrLimitExceeded{Resource: "Drafts for user_id=" + userID + " space_id=" + spaceID, Limit: MaxRowsPerQuery}
	}

	return drafts, nil
}
