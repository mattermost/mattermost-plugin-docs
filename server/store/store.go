// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/morph"
	ps "github.com/mattermost/morph/drivers/postgres"
	"github.com/mattermost/morph/sources/embedded"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"
	"github.com/wiggin77/merror"
)

// pgUniqueViolationCode is the PostgreSQL SQLSTATE for a unique_violation.
const pgUniqueViolationCode = "23505"

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == pgUniqueViolationCode
}

// pgSerializationFailureCode is the PostgreSQL SQLSTATE for a serialization_failure, raised e.g.
// when a locking read under REPEATABLE READ encounters a row that a concurrent transaction
// updated and committed after this transaction's snapshot was taken.
const pgSerializationFailureCode = "40001"

// isSerializationFailure reports whether err is a PostgreSQL serialization failure.
func isSerializationFailure(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == pgSerializationFailureCode
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

// pluginMaxOpenConns caps the plugin's own connection pool well below the server's shared
// master-DB pool (default MaxOpenConns 300), so the plugin can never exhaust that pool on its
// own even if every connection is held for the maximum lock-wait duration.
const pluginMaxOpenConns = 15

// pluginMaxIdleConns keeps idle connections available for typical Docs usage without holding
// onto more of the shared pool than necessary.
const pluginMaxIdleConns = 5

// pluginConnMaxLifetime bounds how long a pooled connection is reused, as routine hygiene.
const pluginConnMaxLifetime = 5 * time.Minute

// migrationLockTimeout must exceed the statement timeout: an early expiry could drop the
// distributed migration lock before DDL completes.
const migrationLockTimeout = 70 * time.Minute

// Store holds the database handle used by the Docs plugin.
type Store struct {
	db      *sqlx.DB
	builder sq.StatementBuilderType
	// log may be nil in tests; warnings are dropped when unset.
	log *pluginapi.LogService
}

// New creates a Store wrapping the given master DB handle. log may be nil in tests
// that do not exercise migration warnings.
func New(db *sql.DB, driverName string, log *pluginapi.LogService) (*Store, error) {
	if driverName != "postgres" {
		return nil, fmt.Errorf("docs plugin only supports PostgreSQL; got %q", driverName)
	}

	sqlxDB := sqlx.NewDb(db, driverName)
	sqlxDB.SetMaxOpenConns(pluginMaxOpenConns)
	sqlxDB.SetMaxIdleConns(pluginMaxIdleConns)
	sqlxDB.SetConnMaxLifetime(pluginConnMaxLifetime)

	s := &Store{
		db:      sqlxDB,
		builder: sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		log:     log,
	}
	return s, nil
}

// RunMigrations applies all pending morph migrations. Concurrent runs across an HA
// cluster are serialized internally by a distributed DB-table lock; no external cluster
// mutex is required.
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
		return migrations.ReadFile(path.Join("migrations", name))
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

// columnsWithAlias returns the given columns prefixed with alias (e.g. "p.Id" for alias "p").
func columnsWithAlias(alias string, cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = alias + "." + c
	}
	return out
}

// finalizeTransaction rolls tx back unless already committed (Rollback returns sql.ErrTxDone
// when already committed, which is ignored). On failure, preserves the original typed error
// as the head of a merror chain so errors.As classification still works even if rollback also fails.
func (s *Store) finalizeTransaction(tx *sqlx.Tx, perr *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		if *perr == nil {
			*perr = errors.Wrap(err, "failed to rollback transaction")
		} else {
			*perr = merror.Append(*perr, errors.Wrap(err, "failed to rollback transaction"))
		}
	}
}

// beginBoundedTx starts a transaction bounded by defaultQueryTimeout, and must be used by any
// transaction that can run while its caller already holds WithSpaceMembershipLock's dedicated
// connection. Such a caller needs a second pooled connection while holding one, so an unbounded
// acquisition can wait forever on a saturated pool while itself holding a connection that pool
// needs in order to drain.
//
// The timeout spans the whole transaction, not just connection acquisition: the context is handed
// to BeginTxx, so database/sql rolls the transaction back if it expires mid-flight. Callers must
// therefore finish inside defaultQueryTimeout, not merely start inside it. The returned cancel must
// be deferred, and runs after the commit or rollback that the caller's own defer performs.
func (s *Store) beginBoundedTx() (*sqlx.Tx, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return tx, cancel, nil
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
		return errors.Wrap(err, "failed to build query")
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
		return errors.Wrap(err, "failed to build query")
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
		return nil, errors.Wrap(err, "failed to build query")
	}
	return s.exec(e, query, args...)
}

// advisoryXactLock takes the transaction-scoped advisory lock for key, held until tx ends.
// It is a bare pg_advisory_xact_lock function-call SELECT, which squirrel does not model, so
// it is issued raw.
// hashtextextended maps the key to a single bigint, so a hash collision only over-serializes
// the two colliding keys' operations — added contention, never corruption, with negligible
// probability. Must be called inside tx.
func (s *Store) advisoryXactLock(tx *sqlx.Tx, key string) error {
	_, err := s.exec(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

// nextMonotonic advances now past prev so same-millisecond writes still move the
// CAS token forward, preventing a stale concurrent writer from matching.
func nextMonotonic(now, prev int64) int64 {
	if now <= prev {
		return prev + 1
	}
	return now
}

// monotonicBump is nextMonotonic in SQL: GREATEST(col + 1, now) sets col to at least col+1 even
// when now is not ahead of the stored value, so the write always moves the CAS token forward and
// a stale optimistic-lock baseline can never read as current again. Every write to a column used
// as an optimistic-lock token must set it through this expression, never to a plain value.
func monotonicBump(col string, now int64) sq.Sqlizer {
	return sq.Expr("GREATEST("+col+" + 1, ?)", now)
}

// requirePositiveLimit returns ErrInvalidInput when limit is not positive.
func requirePositiveLimit(entity string, limit int) error {
	if limit <= 0 {
		return &ErrInvalidInput{Entity: entity, Field: "limit", Value: limit}
	}
	return nil
}

// applyLimitOffset paginates a select query with a positive limit and a non-negative offset.
// Callers must reject limit <= 0 themselves (as ErrInvalidInput) before calling this.
func applyLimitOffset(builder sq.SelectBuilder, offset, limit int) sq.SelectBuilder {
	if offset < 0 {
		offset = 0
	}
	return builder.Limit(uint64(limit)).Offset(uint64(offset)) //nolint:gosec // limit>0 and offset>=0 enforced above
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
	// Reason optionally carries either the originating AppError.Id (e.g. from a model IsValid
	// check) or one of the short Reason* codes below, enabling callers to surface a specific
	// error key instead of a generic one.
	Reason string
}

// Reason codes for invariants decided atomically under a row lock (see RestorePage, RestoreSpace,
// CreatePage). Callers map these to their own error keys.
const (
	ReasonNotRestorable             = "not_restorable"
	ReasonNotDeleted                = "not_deleted"
	ReasonMaxDepthExceeded          = "max_depth_exceeded"
	ReasonSubtreeMaxDepthExceeded   = "subtree_max_depth_exceeded"
	ReasonParentNotLive             = "parent_not_live"
	ReasonSubtreeNotOwned           = "subtree_not_owned"
	ReasonDraftCycle                = "draft_cycle"
	ReasonDraftTooDeep              = "draft_too_deep"
	ReasonDraftQuotaExceeded        = "draft_quota_exceeded"
	ReasonSubtreeTotalBytesExceeded = "subtree_total_bytes_exceeded"
	// ReasonPageNotLive marks an autosave whose target is not addressable in the request's space:
	// the page was deleted, snapshotted, or moved out of it, the existing draft belongs to another
	// space, or the page id was never reserved at all (no page row and no draft). The caller maps
	// it to 404 rather than a generic 400 — from the requester's view the page does not exist in
	// that space.
	ReasonPageNotLive = "page_not_live"
)

func (e *ErrInvalidInput) Error() string {
	return fmt.Sprintf("invalid input: %s field %s=%v", e.Entity, e.Field, e.Value)
}

// IsErrInvalidInput reports whether err is an ErrInvalidInput.
func IsErrInvalidInput(err error) bool {
	var e *ErrInvalidInput
	return errors.As(err, &e)
}

// InvalidInputReason returns the Reason of the ErrInvalidInput in err's chain, or "" if err is not
// an ErrInvalidInput or carries no reason.
func InvalidInputReason(err error) string {
	var e *ErrInvalidInput
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

// Conflict reasons let a caller tell one CAS failure from another without parsing Resource. An
// ErrConflict with no Reason is an unqualified conflict (e.g. a primary-key collision).
const (
	// ReasonConcurrentEdit: the page's EditAt no longer matches the baseline the caller published
	// against — someone else edited the page.
	ReasonConcurrentEdit = "concurrent_edit"
	// ReasonConcurrentAutosave: the draft's version token no longer matches what the caller read —
	// usually a concurrent autosave, but also a bulk write (a page delete reparenting a pending
	// draft, or a move-to-space re-homing it) that bumps the token without changing content.
	ReasonConcurrentAutosave = "concurrent_autosave"
)

// ErrConflict is returned when a unique constraint is violated or a CAS check fails. Reason, when
// set, names which CAS failed so a caller can map it to a specific response.
type ErrConflict struct {
	Resource string
	Reason   string
}

func (e *ErrConflict) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("conflict on %s: %s", e.Resource, e.Reason)
	}
	return fmt.Sprintf("conflict: %s", e.Resource)
}

// IsErrConflict reports whether err is an ErrConflict.
func IsErrConflict(err error) bool {
	var e *ErrConflict
	return errors.As(err, &e)
}

// ReasonLockTimeout marks an ErrConflict raised by a WithSpaceMembershipLock acquisition timeout,
// distinct from the default CAS/unique-constraint conflict.
const ReasonLockTimeout = "lock_timeout"

// IsErrLockTimeout reports whether err is an ErrConflict raised by a space-membership advisory
// lock acquisition timeout.
func IsErrLockTimeout(err error) bool {
	var e *ErrConflict
	return errors.As(err, &e) && e.Reason == ReasonLockTimeout
}

// ConflictReason returns the Reason of the ErrConflict in err's chain, or "" if err is not an
// ErrConflict or carries no reason.
func ConflictReason(err error) string {
	var e *ErrConflict
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

// ErrLimitExceeded is returned when a result set exceeds a hard size limit.
type ErrLimitExceeded struct {
	Resource string
	Limit    int
	// Reason optionally carries one of the short Reason* codes below, so a limit re-checked
	// under lock can communicate the same condition as any earlier pre-check; the app layer
	// maps the code to its own error key. Empty means storeAppError falls back to
	// app.store.too_large.app_error.
	Reason string
}

func (e *ErrLimitExceeded) Error() string {
	return fmt.Sprintf("%s exceeds limit of %d", e.Resource, e.Limit)
}

// IsErrLimitExceeded reports whether err is an ErrLimitExceeded.
func IsErrLimitExceeded(err error) bool {
	var e *ErrLimitExceeded
	return errors.As(err, &e)
}

// ErrCircularReference is returned when a move would make a page its own ancestor or descendant.
type ErrCircularReference struct {
	PageID       string
	DestParentID string
}

func (e *ErrCircularReference) Error() string {
	return fmt.Sprintf("page %s cannot move under %s: would create a cycle", e.PageID, e.DestParentID)
}

// IsErrCircularReference reports whether err is an ErrCircularReference.
func IsErrCircularReference(err error) bool {
	var e *ErrCircularReference
	return errors.As(err, &e)
}
