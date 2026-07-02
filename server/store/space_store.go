// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

var spaceSelectColumns = []string{
	"Id", "ChannelId", "TeamId", "CreatorId", "Title", "Description", "Icon", "Props",
	"CreateAt", "UpdateAt", "DeleteAt", "SortOrder",
}

func (s *Store) spaceSelectQuery() sq.SelectBuilder {
	return s.getQueryBuilder().
		Select(spaceSelectColumns...).
		From("DOCS_Space")
}

// CreateSpace inserts a space row. It fills in defaults and rejects an invalid space itself, so
// the caller need not prepare or validate it beforehand.
func (s *Store) CreateSpace(space *model.Space) (*model.Space, error) {
	if space == nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "space", Value: nil}
	}

	space.PreSave()
	if validErr := space.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	builder := s.getQueryBuilder().
		Insert("DOCS_Space").
		Columns(spaceSelectColumns...).
		Values(space.Id, space.ChannelId, space.TeamId, space.CreatorId, space.Title, space.Description, space.Icon, space.GetProps(),
			space.CreateAt, space.UpdateAt, space.DeleteAt, space.SortOrder)

	if _, err := s.execBuilder(s.db, builder); err != nil {
		if isUniqueViolation(err) {
			if constraintName(err) == "uq_docs_space_channel_id" {
				return nil, &ErrConflict{Resource: "Space channel_id=" + space.ChannelId}
			}
			return nil, &ErrConflict{Resource: "Space id=" + space.Id}
		}
		return nil, errors.Wrap(err, "unable_to_save_space")
	}

	return space, nil
}

// GetSpace returns the space with the given ID, returning ErrNotFound if not found or deleted.
func (s *Store) GetSpace(id string) (*model.Space, error) {
	if id == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "id", Value: id}
	}

	builder := s.spaceSelectQuery().Where(sq.Eq{"Id": id, "DeleteAt": 0})

	var space model.Space
	if err := s.getBuilder(s.db, &space, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: id}
		}
		return nil, errors.Wrap(err, "unable_to_get_space")
	}
	return &space, nil
}

// GetSpaceForChannel returns the active space for the given channel, or ErrNotFound.
func (s *Store) GetSpaceForChannel(channelID string) (*model.Space, error) {
	if channelID == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "channelID", Value: channelID}
	}

	builder := s.spaceSelectQuery().Where(sq.Eq{"ChannelId": channelID, "DeleteAt": 0})

	var space model.Space
	if err := s.getBuilder(s.db, &space, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space for channel", ID: channelID}
		}
		return nil, errors.Wrap(err, "unable_to_get_space_for_channel")
	}
	return &space, nil
}

// GetSpacesForTeam returns live spaces for the given team, ordered by SortOrder ascending,
// with CreateAt then Id as stable tie-breakers. limit must be > 0.
func (s *Store) GetSpacesForTeam(teamID string, offset, limit int) ([]*model.Space, error) {
	if teamID == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "teamID", Value: teamID}
	}
	if err := requirePositiveLimit("Space", limit); err != nil {
		return nil, err
	}

	builder := s.spaceSelectQuery().
		Where(sq.Eq{"TeamId": teamID, "DeleteAt": 0}).
		OrderBy("SortOrder ASC", "CreateAt DESC", "Id ASC")

	builder = applyLimitOffset(builder, offset, limit)

	spaces := []*model.Space{}
	if err := s.selectBuilder(s.db, &spaces, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_spaces_for_team")
	}
	return spaces, nil
}

// UpdateSpace replaces a space's mutable fields (Title, Description, Icon, Props).
// Full-replacement: zero-valued fields overwrite stored values, so callers must supply
// a complete row. space.UpdateAt is the optimistic-lock baseline (first-one-wins): a
// mismatch returns ErrConflict. Passing force skips the check (last-write-wins).
func (s *Store) UpdateSpace(space *model.Space, force bool) (_ *model.Space, err error) {
	if space == nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "space", Value: nil}
	}
	if space.Id == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "Id", Value: space.Id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	var existing model.Space
	selectQuery := s.spaceSelectQuery().
		Where(sq.Eq{"Id": space.Id, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	if txErr := s.getBuilder(tx, &existing, selectQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: space.Id}
		}
		return nil, errors.Wrap(txErr, "failed to get space for update")
	}

	if !force && space.UpdateAt != existing.UpdateAt {
		return nil, &ErrConflict{Resource: "Space id=" + space.Id}
	}

	oldUpdateAt := existing.UpdateAt
	existing.Title = space.Title
	existing.Description = space.Description
	existing.Icon = space.Icon
	existing.Props = space.Props

	existing.PreUpdate()
	// Keep UpdateAt strictly monotonic (same-millisecond writes still advance the CAS token).
	existing.UpdateAt = nextMonotonic(existing.UpdateAt, oldUpdateAt)
	if validErr := existing.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	updateBuilder := s.getQueryBuilder().
		Update("DOCS_Space").
		Set("Title", existing.Title).
		Set("Description", existing.Description).
		Set("Icon", existing.Icon).
		Set("Props", existing.GetProps()).
		Set("UpdateAt", existing.UpdateAt).
		Where(sq.Eq{"Id": existing.Id, "DeleteAt": 0})

	result, txErr := s.execBuilder(tx, updateBuilder)
	if txErr != nil {
		return nil, errors.Wrap(txErr, "unable_to_update_space")
	}
	if raErr := checkRowsAffected(result, "Space", existing.Id); raErr != nil {
		return nil, raErr
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	return &existing, nil
}

// DeleteSpace soft-deletes a space and cascades to its live pages in the same
// transaction. Pages are scoped by SpaceId so a reused ChannelId is not affected.
func (s *Store) DeleteSpace(id string) (err error) {
	if id == "" {
		return &ErrInvalidInput{Entity: "Space", Field: "id", Value: id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the live space before its pages (space-before-page order) so page ops never deadlock
	// with this cascade; ErrNotFound if it is already gone.
	if lockErr := s.lockLiveSpace(tx, id); lockErr != nil {
		return lockErr
	}

	// Stamp the space and the pages it cascades with one cascade marker strictly greater than
	// any DeleteAt already on the space's non-snapshot pages. RestoreSpace matches on this exact
	// value, so a page deleted individually beforehand — even in the same millisecond — keeps a
	// smaller DeleteAt and is never swept back in. The space row is held by the lock above, and
	// DeletePage also takes that lock, so no page in this space can be deleted concurrently and
	// land an equal stamp after this read.
	now := mmmodel.GetMillis()
	var maxPageDeleteAt int64
	// DeleteAt > 0 both matches idx_docs_page_spaceid_deleted's predicate (so the planner
	// can use it) and is harmless: live rows contribute only 0 to the MAX anyway.
	maxQuery := s.getQueryBuilder().
		Select("COALESCE(MAX(DeleteAt), 0)").
		From("DOCS_Page").
		Where(sq.Eq{"SpaceId": id, "OriginalId": ""}).
		Where(sq.Gt{"DeleteAt": 0})
	if mErr := s.getBuilder(tx, &maxPageDeleteAt, maxQuery); mErr != nil {
		return errors.Wrap(mErr, "failed to compute space cascade stamp")
	}
	cascadeAt := now
	if maxPageDeleteAt >= cascadeAt {
		cascadeAt = maxPageDeleteAt + 1
	}

	spaceQuery := s.getQueryBuilder().
		Update("DOCS_Space").
		Set("DeleteAt", cascadeAt).
		// Advance the CAS token (UpdateSpace optimistic-locks on UpdateAt) so a delete
		// invalidates stale client baselines. GREATEST(UpdateAt+1, ?) is nextMonotonic in SQL.
		Set("UpdateAt", sq.Expr("GREATEST(UpdateAt + 1, ?)", cascadeAt)).
		Where(sq.Eq{"Id": id, "DeleteAt": 0})
	result, txErr := s.execBuilder(tx, spaceQuery)
	if txErr != nil {
		return errors.Wrap(txErr, "unable_to_delete_space")
	}
	if rowsErr := checkRowsAffected(result, "Space", id); rowsErr != nil {
		return rowsErr
	}

	// Cascade to live pages with the same stamp. OriginalId='' excludes version snapshots
	// (always DeleteAt>0, so already excluded by the DeleteAt=0 predicate too).
	pagesQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", cascadeAt).
		Set("UpdateAt", cascadeAt).
		Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", cascadeAt)).
		Where(sq.Eq{"SpaceId": id, "OriginalId": "", "DeleteAt": 0})

	if _, pErr := s.execBuilder(tx, pagesQuery); pErr != nil {
		return errors.Wrap(pErr, "unable_to_delete_space_pages")
	}

	// Drafts are left untouched: this soft-delete is reversible (RestoreSpace), so a user's
	// in-progress work must survive the round trip. Drafts have no DeleteAt, so they are purged
	// only when their page is deleted (DeletePage).
	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}

// RestoreSpace un-deletes a soft-deleted space and the pages that DeleteSpace cascaded,
// matched by the shared DeleteAt stamp. Pages deleted individually before the space, and
// version snapshots, stay deleted. Returns ErrNotFound
// when the space ID does not exist, ErrInvalidInput when the space exists but is already live
// (not deleted), and ErrConflict when another live space now owns the same backing channel.
func (s *Store) RestoreSpace(id string) (err error) {
	if id == "" {
		return &ErrInvalidInput{Entity: "Space", Field: "id", Value: id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	now := mmmodel.GetMillis()

	// Lock the soft-deleted space row; its DeleteAt scopes the page un-cascade below.
	var deleted struct {
		ChannelID string
		DeleteAt  int64
	}
	selectQuery := s.getQueryBuilder().
		Select("ChannelId", "DeleteAt").
		From("DOCS_Space").
		Where(sq.Eq{"Id": id}).
		Suffix("FOR UPDATE")
	if txErr := s.getBuilder(tx, &deleted, selectQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Space", ID: id}
		}
		return errors.Wrap(txErr, "failed to read space for restore")
	}

	// Already live: nothing to restore. Decided under the row lock so a concurrent restore
	// cannot turn this into a misleading not-found.
	if deleted.DeleteAt == 0 {
		return &ErrInvalidInput{Entity: "Space", Field: "id", Value: id, Reason: ReasonNotDeleted}
	}

	spaceQuery := s.getQueryBuilder().
		Update("DOCS_Space").
		Set("DeleteAt", 0).
		// Advance the CAS token monotonically so a delete→restore round trip — even within one
		// millisecond, or after a prior synthetic UpdateAt — invalidates stale client baselines.
		Set("UpdateAt", sq.Expr("GREATEST(UpdateAt + 1, ?)", now)).
		Where(sq.And{sq.Eq{"Id": id}, sq.NotEq{"DeleteAt": 0}})
	result, txErr := s.execBuilder(tx, spaceQuery)
	if txErr != nil {
		// A live space already holds this channel: restoring would breach uq_docs_space_channel_id.
		if isUniqueViolation(txErr) {
			return &ErrConflict{Resource: "Space channel_id=" + deleted.ChannelID}
		}
		return errors.Wrap(txErr, "unable_to_restore_space")
	}
	if rowsErr := checkRowsAffected(result, "Space", id); rowsErr != nil {
		return rowsErr
	}

	// Un-cascade only the pages this space's delete took down (same DeleteAt stamp); their
	// SortOrder/ParentId are untouched by the cascade, so the pre-delete tree reconstitutes.
	pagesQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", 0).
		Set("UpdateAt", now).
		Set("EditAt", sq.Expr("GREATEST(EditAt + 1, ?)", now)).
		Where(sq.Eq{"SpaceId": id, "OriginalId": "", "DeleteAt": deleted.DeleteAt})
	if _, pErr := s.execBuilder(tx, pagesQuery); pErr != nil {
		return errors.Wrap(pErr, "unable_to_restore_space_pages")
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}
