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

// CreateSpace inserts a space row, applying PreSave and validation internally.
func (s *Store) CreateSpace(space *model.Space) (*model.Space, error) {
	space.PreSave()
	if validErr := space.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "IsValid", Value: validErr.Error()}
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
	builder := s.spaceSelectQuery().Where(sq.Eq{"ChannelId": channelID, "DeleteAt": 0})

	var space model.Space
	if err := s.getBuilder(s.db, &space, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: channelID}
		}
		return nil, errors.Wrap(err, "unable_to_get_space_for_channel")
	}
	return &space, nil
}

// GetSpacesForTeam returns spaces for the given team, ordered by SortOrder.
// limit <= 0 returns all rows.
func (s *Store) GetSpacesForTeam(teamID string, includeDeleted bool, offset, limit int) ([]*model.Space, error) {
	builder := s.spaceSelectQuery().Where(sq.Eq{"TeamId": teamID})

	if !includeDeleted {
		builder = builder.Where(sq.Eq{"DeleteAt": 0})
	}

	builder = builder.OrderBy("SortOrder ASC", "CreateAt DESC", "Id ASC")

	if limit > 0 {
		if offset < 0 {
			offset = 0
		}
		builder = builder.Limit(uint64(limit)).Offset(uint64(offset)) //nolint:gosec // limit>0 and offset>=0 enforced above
	}

	spaces := []*model.Space{}
	if err := s.selectBuilder(s.db, &spaces, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_spaces_for_team")
	}
	return spaces, nil
}

// UpdateSpace replaces a space's mutable fields (Title, Description, Icon, Props).
// Full-replacement: zero-valued fields overwrite stored values, so callers must supply
// a complete row. A non-zero space.UpdateAt is treated as the optimistic-lock baseline
// (first-one-wins); zero opts out (last-write-wins).
func (s *Store) UpdateSpace(space *model.Space) (_ *model.Space, err error) {
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

	if space.UpdateAt != 0 && space.UpdateAt != existing.UpdateAt {
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
		return nil, &ErrInvalidInput{Entity: "Space", Field: "IsValid", Value: validErr.Error()}
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
	now := mmmodel.GetMillis()

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the space before its pages (space-before-page order) so page ops never deadlock
	// with this cascade.
	spaceQuery := s.getQueryBuilder().
		Update("DOCS_Space").
		Set("DeleteAt", now).
		Where(sq.Eq{"Id": id, "DeleteAt": 0})

	result, txErr := s.execBuilder(tx, spaceQuery)
	if txErr != nil {
		return errors.Wrap(txErr, "unable_to_delete_space")
	}
	if rowsErr := checkRowsAffected(result, "Space", id); rowsErr != nil {
		return rowsErr
	}

	// Cascade to live pages. OriginalId='' matches idx_docs_page_spaceid's partial
	// predicate (version snapshots always have DeleteAt>0 so they're already excluded).
	pagesQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", now).
		Where(sq.Eq{"SpaceId": id, "OriginalId": "", "DeleteAt": 0})

	if _, pErr := s.execBuilder(tx, pagesQuery); pErr != nil {
		return errors.Wrap(pErr, "unable_to_delete_space_pages")
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}
