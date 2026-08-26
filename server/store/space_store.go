// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/jmoiron/sqlx"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

var spaceSelectColumns = []string{
	"Id", "ChannelId", "TeamId", "CreatorId", "Title", "Description", "Icon", "Props", "ViewAccess",
	"CreateAt", "UpdateAt", "DeleteAt", "SortOrder",
}

func (s *Store) spaceSelectQuery() sq.SelectBuilder {
	return s.getQueryBuilder().
		Select(spaceSelectColumns...).
		From("DOCS_Space")
}

// CreateSpace inserts a space row, fills in defaults and validates before inserting.
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
		Values(space.Id, space.ChannelId, space.TeamId, space.CreatorId, space.Title, space.Description, space.Icon, space.GetProps(), space.ViewAccess,
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

// GetSpace returns the space with the given ID, returning ErrNotFound if not found. When
// includeDeleted is false, soft-deleted spaces are also treated as not found.
func (s *Store) GetSpace(spaceID string, includeDeleted bool) (*model.Space, error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "id", Value: spaceID}
	}

	builder := s.spaceSelectQuery().Where(sq.Eq{"Id": spaceID})
	if !includeDeleted {
		builder = builder.Where(sq.Eq{"DeleteAt": 0})
	}

	var space model.Space
	if err := s.getBuilder(s.db, &space, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: spaceID}
		}
		return nil, errors.Wrap(err, "unable_to_get_space")
	}
	return &space, nil
}

// GetSpacesForTeam returns one page of the given team's live spaces visible to userID, ordered by
// SortOrder ascending with CreateAt then Id as stable tie-breakers. A space is visible when the
// caller is an active member of its team and a member of its backing channel (a read-only EXISTS
// against core's TeamMembers and ChannelMembers tables: space ("S") channels are excluded from the
// generic channel-listing plugin APIs, so the caller cannot supply its visible-channel set), or when
// the space is ViewAccess='open' and
// callerHasOpenFallthrough is true — a single app-layer-computed boolean carrying the caller's
// team-active/read_public_channel/compliance-mode conjunct (see the app-layer read resolver);
// the store never evaluates permissions itself. There is deliberately no unfiltered variant, so a
// listing can never bypass this predicate. limit must be > 0.
func (s *Store) GetSpacesForTeam(teamID, userID string, callerHasOpenFallthrough bool, offset, limit int) ([]*model.Space, error) {
	if teamID == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "teamID", Value: teamID}
	}
	if userID == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "userID", Value: userID}
	}
	if err := requirePositiveLimit("Space", limit); err != nil {
		return nil, err
	}

	visible := sq.Or{sq.Expr(`
		EXISTS (
			SELECT 1
			FROM ChannelMembers cm
			INNER JOIN TeamMembers tm
				ON tm.UserId = cm.UserId
				AND tm.TeamId = sp.TeamId
				AND tm.DeleteAt = 0
			WHERE cm.ChannelId = sp.ChannelId AND cm.UserId = ?
		)`, userID)}
	if callerHasOpenFallthrough {
		visible = append(visible, sq.Eq{"sp.ViewAccess": model.ViewAccessOpen})
	}

	builder := s.getQueryBuilder().
		Select(columnsWithAlias("sp", spaceSelectColumns)...).
		From("DOCS_Space sp").
		Where(sq.Eq{"sp.TeamId": teamID, "sp.DeleteAt": 0}).
		Where(visible).
		OrderBy("sp.SortOrder ASC", "sp.CreateAt DESC", "sp.Id ASC")
	builder = applyLimitOffset(builder, offset, limit)

	spaces := []*model.Space{}
	if err := s.selectBuilder(s.db, &spaces, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_spaces_for_team")
	}
	return spaces, nil
}

// UpdateSpace applies patch to a live space's mutable fields (Title, Description, Icon, Props,
// ViewAccess). The patch is merged into the row read under lock, so fields the patch leaves nil
// keep any concurrent writer's value rather than being overwritten by the caller's stale
// snapshot. expectedUpdateAt is the optimistic-lock baseline: a mismatch returns ErrConflict,
// so the first update to commit is the one kept. Passing force skips that check and applies the
// caller's update over whatever is stored, but the merge into the locked row still applies.
func (s *Store) UpdateSpace(spaceID string, patch *model.SpacePatch, expectedUpdateAt int64, force bool) (_ *model.Space, err error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "Id", Value: spaceID}
	}
	// Validate the patch before opening the transaction, so an invalid or empty patch never
	// locks the row or bumps UpdateAt. Enforced here, not only in the service, so any store
	// caller upholds the contract.
	if validErr := patch.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "Patch", Value: validErr.Error(), Reason: validErr.Id}
	}

	tx, cancel, err := s.beginBoundedTx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer cancel()
	defer s.finalizeTransaction(tx, &err)

	var existing model.Space
	selectQuery := s.spaceSelectQuery().
		Where(sq.Eq{"Id": spaceID, "DeleteAt": 0}).
		Suffix("FOR UPDATE")
	if txErr := s.getBuilder(tx, &existing, selectQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Space", ID: spaceID}
		}
		return nil, errors.Wrap(txErr, "failed to get space for update")
	}

	if !force && expectedUpdateAt != existing.UpdateAt {
		return nil, &ErrConflict{Resource: "Space id=" + spaceID}
	}

	oldUpdateAt := existing.UpdateAt
	existing.Patch(patch)

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
		Set("ViewAccess", existing.ViewAccess).
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
func (s *Store) DeleteSpace(spaceID string) (err error) {
	if spaceID == "" {
		return &ErrInvalidInput{Entity: "Space", Field: "id", Value: spaceID}
	}

	tx, err := s.beginUnboundedTx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the live space before its pages (space-before-page order) so page ops never deadlock
	// with this cascade; ErrNotFound if it is already gone.
	if lockErr := s.lockLiveSpace(tx, spaceID); lockErr != nil {
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
	// DeleteAt > 0 both matches the partial-index predicate on deleted pages (so the planner
	// can use it) and is harmless: live rows contribute only 0 to the MAX anyway.
	maxQuery := s.getQueryBuilder().
		Select("COALESCE(MAX(DeleteAt), 0)").
		From("DOCS_Page").
		Where(sq.Eq{"SpaceId": spaceID, "OriginalId": ""}).
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
		// invalidates stale client baselines.
		Set("UpdateAt", monotonicBump("UpdateAt", cascadeAt)).
		Where(sq.Eq{"Id": spaceID, "DeleteAt": 0})
	result, txErr := s.execBuilder(tx, spaceQuery)
	if txErr != nil {
		return errors.Wrap(txErr, "unable_to_delete_space")
	}
	if rowsErr := checkRowsAffected(result, "Space", spaceID); rowsErr != nil {
		return rowsErr
	}

	// Cascade to live pages with the same stamp. OriginalId='' excludes version snapshots
	// (always DeleteAt>0, so already excluded by the DeleteAt=0 predicate too).
	pagesQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", cascadeAt).
		Set("UpdateAt", monotonicBump("UpdateAt", cascadeAt)).
		Set("EditAt", monotonicBump("EditAt", cascadeAt)).
		Where(sq.Eq{"SpaceId": spaceID, "OriginalId": "", "DeleteAt": 0})

	if _, pErr := s.execBuilder(tx, pagesQuery); pErr != nil {
		return errors.Wrap(pErr, "unable_to_delete_space_pages")
	}

	// Drafts are left untouched: this soft-delete is reversible (RestoreSpace), so a user's
	// in-progress work must survive the round trip. Drafts have no DeleteAt, so they are purged
	// only when their page is explicitly deleted.
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
func (s *Store) RestoreSpace(spaceID string) (err error) {
	if spaceID == "" {
		return &ErrInvalidInput{Entity: "Space", Field: "id", Value: spaceID}
	}

	tx, err := s.beginUnboundedTx()
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
		Where(sq.Eq{"Id": spaceID}).
		Suffix("FOR UPDATE")
	if txErr := s.getBuilder(tx, &deleted, selectQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Space", ID: spaceID}
		}
		return errors.Wrap(txErr, "failed to read space for restore")
	}

	// Already live: nothing to restore. Decided under the row lock so a concurrent restore
	// cannot turn this into a misleading not-found.
	if deleted.DeleteAt == 0 {
		return &ErrInvalidInput{Entity: "Space", Field: "id", Value: spaceID, Reason: ReasonNotDeleted}
	}

	spaceQuery := s.getQueryBuilder().
		Update("DOCS_Space").
		Set("DeleteAt", 0).
		// Advance the CAS token so a delete→restore round trip — even within one millisecond,
		// or after a prior synthetic UpdateAt — invalidates stale client baselines.
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.And{sq.Eq{"Id": spaceID}, sq.NotEq{"DeleteAt": 0}})
	result, txErr := s.execBuilder(tx, spaceQuery)
	if txErr != nil {
		// A live space already holds this channel: restoring would breach the channel-uniqueness constraint.
		if isUniqueViolation(txErr) {
			return &ErrConflict{Resource: "Space channel_id=" + deleted.ChannelID}
		}
		return errors.Wrap(txErr, "unable_to_restore_space")
	}
	if rowsErr := checkRowsAffected(result, "Space", spaceID); rowsErr != nil {
		return rowsErr
	}

	// Un-cascade only the pages this space's delete took down (same DeleteAt stamp); their
	// SortOrder/ParentId are untouched by the cascade, so the pre-delete tree reconstitutes.
	pagesQuery := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("DeleteAt", 0).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Set("EditAt", monotonicBump("EditAt", now)).
		Where(sq.Eq{"SpaceId": spaceID, "OriginalId": "", "DeleteAt": deleted.DeleteAt})
	if _, pErr := s.execBuilder(tx, pagesQuery); pErr != nil {
		return errors.Wrap(pErr, "unable_to_restore_space_pages")
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}

const (
	// spaceMembershipLockAcquireTimeout bounds how long a membership mutation waits for another
	// holder of the same space's advisory lock. The current holder may be off making remote
	// channel calls, so an unbounded wait would let blocked waiters pile up, each holding a
	// pooled connection; on timeout the waiter gives up with a retryable ErrConflict instead.
	spaceMembershipLockAcquireTimeout = 10 * time.Second
	// spaceMembershipLockRetryInterval paces the try-lock polling loop below.
	spaceMembershipLockRetryInterval = 100 * time.Millisecond
)

// WithSpaceMembershipLock runs fn while holding spaceID's membership advisory lock, serializing
// membership mutations for one space across processes. Guards that span multiple non-database
// calls — read the member list, then mutate it — are atomic with respect to each other only
// under this lock. The lock is session-scoped on a dedicated pooled connection with no open
// transaction of its own.
//
// fn may do store work of its own and require another plugin-pool connection in addition to this
// lock's session connection. fn must therefore never
// reach a store method that begins through beginUnboundedTx, or a saturated pool can leave every
// holder waiting on a connection no other holder will release. fn must also stay short, since the
// lock connection is held for its whole duration.
//
// fn's error is returned unchanged. When the lock cannot be acquired within
// spaceMembershipLockAcquireTimeout, ErrConflict is returned so the caller can surface a
// retryable conflict.
func (s *Store) WithSpaceMembershipLock(spaceID string, fn func() error) error {
	return s.withSpaceMembershipLock(spaceID, spaceMembershipLockAcquireTimeout, fn)
}

func (s *Store) withSpaceMembershipLock(spaceID string, acquireTimeout time.Duration, fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), acquireTimeout)
	defer cancel()

	conn, err := s.db.Connx(ctx)
	if err != nil {
		return errors.Wrap(err, "get_connection")
	}
	defer func() {
		// ErrConnDone means discardConn already destroyed the connection, which is not a failure.
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) && s.log != nil {
			s.log.Warn("failed to return advisory lock connection to the pool", "space_id", spaceID, "err", closeErr)
		}
	}()

	key := "space_members:" + spaceID
	// The pg_try_advisory_lock / pg_advisory_unlock calls here are bare function-call SELECTs that
	// squirrel does not model, so they are issued raw.
	// Poll with pg_try_advisory_lock rather than blocking in pg_advisory_lock: a blocking wait
	// canceled by the deadline races against the server granting the lock in the same instant,
	// which would strand a granted lock on a connection headed back to the pool. Each try
	// returns its verdict immediately; error paths below avoid reusing a session that may hold it.
	for {
		var acquired bool
		if lockErr := conn.GetContext(ctx, &acquired, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key); lockErr != nil {
			// The reply was lost, so the lock may or may not have been granted; a best-effort
			// unlock keeps a granted lock from riding back into the pool on this session (it is
			// a no-op with a server-side warning when the lock is not held).
			if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key); unlockErr != nil {
				discardConn(conn)
				if s.log != nil {
					s.log.Warn("failed to release space membership advisory lock", "space_id", spaceID, "err", unlockErr)
				}
			}
			if ctx.Err() != nil {
				return &ErrConflict{Resource: "Space membership lock space_id=" + spaceID, Reason: ReasonLockTimeout}
			}
			return errors.Wrap(lockErr, "failed to acquire space membership advisory lock")
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			return &ErrConflict{Resource: "Space membership lock space_id=" + spaceID, Reason: ReasonLockTimeout}
		case <-time.After(spaceMembershipLockRetryInterval):
		}
	}
	defer func() {
		// Unlock on a fresh context: the acquisition deadline may have passed while fn ran. After
		// a failed unlock the session may still hold the lock, so the connection must not go back
		// to the pool where an unrelated caller would inherit it; discard it instead (closing the
		// session releases its advisory locks server-side).
		if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key); unlockErr != nil {
			discardConn(conn)
			if s.log != nil {
				s.log.Warn("failed to release space membership advisory lock", "space_id", spaceID, "err", unlockErr)
			}
		}
	}()

	return fn()
}

// discardConn marks conn's underlying driver connection broken so the deferred Close destroys it
// instead of returning it to the pool (database/sql discards a connection whose Raw callback
// returns driver.ErrBadConn). Used when an advisory unlock fails: the session may still hold the
// lock, and pooling it would leak the lock to an unrelated caller.
func discardConn(conn *sqlx.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// lockLiveSpace FOR UPDATE-locks the live space row.
// Returns ErrNotFound if the space does not exist or is already soft-deleted.
func (s *Store) lockLiveSpace(tx *sqlx.Tx, spaceID string) error {
	_, err := s.lockLiveSpaceChannel(tx, spaceID)
	return err
}

// lockLiveSpaceChannel is lockLiveSpace but also returns the space's backing ChannelId, so a
// caller can derive it from the locked row (single source of truth) instead of trusting a separately-supplied value.
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
