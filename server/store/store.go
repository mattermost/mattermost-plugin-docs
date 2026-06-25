// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/morph"
	ps "github.com/mattermost/morph/drivers/postgres"
	"github.com/mattermost/morph/sources/embedded"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"
)

// pgUniqueViolationCode is the PostgreSQL SQLSTATE for a unique_violation.
const pgUniqueViolationCode = "23505"

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == pgUniqueViolationCode
}

// constraintName returns the constraint/index name from a *pq.Error, or "" if err
// is not a *pq.Error.
func constraintName(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Constraint
	}
	return ""
}

//go:embed migrations/*.sql
var migrations embed.FS

const defaultQueryTimeout = 30 * time.Second

// migrationLockTimeout must exceed the statement timeout: the same context drives
// morph's lock-refresh, so an early expiry could drop the lock mid-DDL.
const migrationLockTimeout = 70 * time.Minute

// Store holds the database handle used by the Docs plugin.
type Store struct {
	db      *sqlx.DB
	builder sq.StatementBuilderType
	// log may be nil in tests; warnings are dropped when unset.
	log *pluginapi.LogService
}

// New creates a Store wrapping the given master DB handle.
func New(db *sql.DB, driverName string) (*Store, error) {
	if driverName != "postgres" {
		return nil, fmt.Errorf("docs plugin only supports PostgreSQL; got %q", driverName)
	}

	s := &Store{
		db:      sqlx.NewDb(db, driverName),
		builder: sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
	return s, nil
}

// SetLogger wires a logger into the store for non-fatal warnings.
func (s *Store) SetLogger(log *pluginapi.LogService) {
	s.log = log
}

// RunMigrations applies all pending morph migrations. Concurrent runs across an HA
// cluster are serialized internally by morph's distributed DB-table lock (WithLock,
// below); no external cluster mutex is required.
func (s *Store) RunMigrations() error {
	ctx, cancel := context.WithTimeout(context.Background(), migrationLockTimeout)
	defer cancel()

	driver, err := ps.WithInstance(s.db.DB)
	if err != nil {
		return errors.Wrap(err, "failed to create morph postgres driver")
	}

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return errors.Wrap(err, "failed to read embedded migrations dir")
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	src, err := embedded.WithInstance(embedded.Resource(names, func(name string) ([]byte, error) {
		return migrations.ReadFile(filepath.Join("migrations", name))
	}))
	if err != nil {
		return errors.Wrap(err, "failed to create morph embedded source")
	}

	engine, err := morph.New(ctx, driver, src,
		morph.WithLock("mm-docs-migration-lock"),
		morph.SetMigrationTableName("docs_db_migrations"),
		// One hour comfortably covers a GIN index build on a large Pages table while
		// still bounding a runaway DDL statement (morph's own default is 300s).
		morph.SetStatementTimeoutInSeconds(3600),
	)
	if err != nil {
		return errors.Wrap(err, "failed to create morph engine")
	}
	defer func() {
		if closeErr := engine.Close(); closeErr != nil && s.log != nil {
			s.log.Warn("failed to close migration engine; the DB-table lock row may persist until its TTL expires", "err", closeErr)
		}
	}()

	if err := engine.ApplyAll(); err != nil {
		return errors.Wrap(err, "failed to apply migrations")
	}

	return nil
}

// Close releases the database connection. *sql.DB.Close is idempotent.
func (s *Store) Close() error {
	return s.db.Close()
}

// getQueryBuilder returns a squirrel builder with Postgres $N placeholders.
func (s *Store) getQueryBuilder() sq.StatementBuilderType {
	return s.builder
}

// finalizeTransaction rolls tx back unless already committed. Callers defer this
// immediately after Beginx and commit explicitly on the success path (Rollback then
// returns sql.ErrTxDone and is ignored). When the body failed (*perr != nil) the
// original typed error is preserved so the app layer can classify it with errors.As;
// any rollback failure is logged instead of wrapped to avoid obscuring the root cause.
func (s *Store) finalizeTransaction(tx *sqlx.Tx, perr *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		if *perr == nil {
			*perr = errors.Wrap(err, "failed to rollback transaction")
		} else if s.log != nil {
			s.log.Warn("failed to rollback transaction after a prior error", "rollback_err", err.Error())
		}
	}
}

// get executes a query and scans one row into dest.
func (s *Store) get(e sqlx.ExtContext, dest any, query string, args ...any) error {
	query = e.Rebind(query)
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	return sqlx.GetContext(ctx, e, dest, query, args...)
}

// getBuilder is like get but accepts a squirrel builder.
func (s *Store) getBuilder(e sqlx.ExtContext, dest any, b sq.Sqlizer) error {
	query, args, err := b.ToSql()
	if err != nil {
		return err
	}
	return s.get(e, dest, query, args...)
}

// selectAll executes a query and scans all rows into dest.
func (s *Store) selectAll(e sqlx.ExtContext, dest any, query string, args ...any) error {
	query = e.Rebind(query)
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	return sqlx.SelectContext(ctx, e, dest, query, args...)
}

// selectBuilder is like selectAll but accepts a squirrel builder.
func (s *Store) selectBuilder(e sqlx.ExtContext, dest any, b sq.Sqlizer) error {
	query, args, err := b.ToSql()
	if err != nil {
		return err
	}
	return s.selectAll(e, dest, query, args...)
}

// exec runs a DML query and returns the result.
func (s *Store) exec(e sqlx.ExtContext, query string, args ...any) (sql.Result, error) {
	query = e.Rebind(query)
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	return e.ExecContext(ctx, query, args...)
}

// execBuilder is like exec but accepts a squirrel builder.
func (s *Store) execBuilder(e sqlx.ExtContext, b sq.Sqlizer) (sql.Result, error) {
	query, args, err := b.ToSql()
	if err != nil {
		return nil, err
	}
	return s.exec(e, query, args...)
}

// nextMonotonic advances now past prev so same-millisecond writes still move the
// CAS token forward, preventing a stale concurrent writer from matching.
func nextMonotonic(now, prev int64) int64 {
	if now <= prev {
		return prev + 1
	}
	return now
}

// checkRowsAffected returns ErrNotFound when the result reports zero rows affected.
func checkRowsAffected(result sql.Result, entityType, entityID string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to get rows affected")
	}
	if rows == 0 {
		return &ErrNotFound{EntityName: entityType, ID: entityID}
	}
	return nil
}

// --- store errors ---

// ErrNotFound is returned when a requested entity does not exist.
type ErrNotFound struct {
	EntityName string
	ID         string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("%s with id=%s not found", e.EntityName, e.ID)
}

// IsErrNotFound reports whether err is an ErrNotFound.
func IsErrNotFound(err error) bool {
	var e *ErrNotFound
	return errors.As(err, &e)
}

// ErrInvalidInput is returned when the caller supplies an invalid value.
type ErrInvalidInput struct {
	Entity string
	Field  string
	Value  any
}

func (e *ErrInvalidInput) Error() string {
	return fmt.Sprintf("invalid input: %s field %s=%v", e.Entity, e.Field, e.Value)
}

// IsErrInvalidInput reports whether err is an ErrInvalidInput.
func IsErrInvalidInput(err error) bool {
	var e *ErrInvalidInput
	return errors.As(err, &e)
}

// ErrConflict is returned when a unique constraint is violated or a CAS check fails.
type ErrConflict struct {
	Resource string
}

func (e *ErrConflict) Error() string {
	return fmt.Sprintf("conflict: %s", e.Resource)
}

// IsErrConflict reports whether err is an ErrConflict.
func IsErrConflict(err error) bool {
	var e *ErrConflict
	return errors.As(err, &e)
}

// ErrLimitExceeded is returned when a result set exceeds a hard size limit.
type ErrLimitExceeded struct {
	Resource string
	Limit    int
}

func (e *ErrLimitExceeded) Error() string {
	return fmt.Sprintf("%s exceeds limit of %d", e.Resource, e.Limit)
}

// IsErrLimitExceeded reports whether err is an ErrLimitExceeded.
func IsErrLimitExceeded(err error) bool {
	var e *ErrLimitExceeded
	return errors.As(err, &e)
}
