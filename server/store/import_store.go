// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"slices"

	"github.com/jmoiron/sqlx"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// importJobColumns are the DOCS_ImportJob columns, in the order used for both select and insert.
// The names match model.ImportJob's field names so sqlx maps them without explicit db tags.
var importJobColumns = []string{
	"Id", "ActorId", "TeamId",
	"TargetKind", "TargetSpaceId", "TargetSpaceExisted", "ConfirmedSpaceTitle", "ConfirmedSpaceDescription", "ProvisionedChannelId",
	"SourceSelectionMode", "SelectedImportSourceId", "SelectedSourceDisplayName", "PreflightMappingRevision",
	"State", "Phase", "TerminalIntent", "MappingInputsChanged", "InvalidationPending",
	"ProgressCurrent", "ProgressTotal", "StagedBytes", "RetainedBytes", "RetainedReservedBytes",
	"BundleSha256", "BundleSummary", "PreflightSummary", "PreflightRevision", "Confirmation", "FinalSummary",
	"ErrorCode", "ErrorMessage", "CancelRequestedAt",
	"CreateAt", "UpdateAt", "ConfirmedAt", "StartedAt", "FinishedAt", "RetainUntil",
}

// importStagedPageColumns are the DOCS_ImportStagedPage columns, in insert order.
var importStagedPageColumns = []string{
	"JobId", "Ordinal", "SourceLine", "ExternalId", "ParentExternalId", "SourceOrdinal", "Restricted",
	"Title", "CanonicalBody", "SearchText", "SourceUserProposal", "SourceAuthorAccountId",
	"SourceCreateAt", "SourceUpdateAt", "SourceProps",
	"IncomingSourceContentHash", "PreflightCurrentContentHash", "PreflightMappingContentHash",
	"PreflightCurrentParentId", "PreflightMappingParentId", "PreflightMappingUpdateAt",
	"PlannedAction", "PlannedPageId", "ResolvedUserId", "AuthorFallbackReason",
}

// importManifestUserColumns are the DOCS_ImportManifestUser columns, in insert order.
var importManifestUserColumns = []string{
	"JobId", "Ordinal", "AccountId", "ConfluenceUsername", "MattermostUsername",
}

// importIssueColumns are the DOCS_ImportIssue columns, in insert order.
var importIssueColumns = []string{
	"JobId", "Stage", "Ordinal", "Severity", "Code",
	"EntityType", "ExternalId", "LocalId", "Title", "Message", "Remediation", "Details",
}

// importSourceColumns are the DOCS_ImportSource columns. OrganizationId is NOT NULL DEFAULT ” in the
// schema, so no COALESCE is needed for the Go string model.
var importSourceColumns = []string{
	"Id", "SpaceId", "SourceType", "DisplayName", "OrganizationId",
	"ExternalSpaceKey", "ExternalSpaceName", "CreatedBy",
	"CreateAt", "UpdateAt", "LastImportAt", "LastSuccessfulJobId", "MappingRevision", "Props",
}

// Batch bounds for the staged-page insert. A single multi-row INSERT is capped both by row count
// (PostgreSQL allows at most 65535 bind parameters per statement, and each row binds
// len(importStagedPageColumns) of them) and by accumulated body bytes, since a staged page's
// CanonicalBody can approach PageBodyMaxBytes and thousands of rows in one statement would
// otherwise buffer hundreds of megabytes.
const (
	importStagedPageBatchRows  = 50
	importStagedPageBatchBytes = 8 * 1024 * 1024
)

// importRowBatchRows bounds one multi-row manifest-user or issue insert. Those rows are small, so
// only the bind parameter limit matters.
const importRowBatchRows = 200

// importStagedRowOverheadBytes is the per-staged-page *fixed* cost charged against the staged-byte
// budget: row header, ordinals, timestamps, booleans, and the fixed-width hash/id columns. Every
// variable-length column is measured directly (see AddPage) rather than folded into this allowance,
// because three 512-byte identifiers alone would exceed any plausible flat figure.
const importStagedRowOverheadBytes = 256

// importRetainedRowBytes is the conservative budget for one durable report row (a result or an issue,
// including its bounded Details JSON).
const importRetainedRowBytes = 1024

// importRetainedRowsPerEntity is how many durable rows one page or mapping may eventually produce:
// a preflight result, an execution result, and a bounded number of issues in each of those two
// stages. Reserving this per entity is what makes the "an admitted job can always record its
// terminal outcome" guarantee real rather than aspirational.
const importRetainedRowsPerEntity = 2 + 2*importRetainedIssuesPerEntity

// importRetainedIssuesPerEntity is the per-entity, per-stage issue allowance folded into the
// reservation. It is below the model's hard per-page code cap because aggregating repeated findings by
// stable code keeps the realistic count small.
const importRetainedIssuesPerEntity = 4

// ImportAdmissionLimits bounds aggregate import resource use. These are availability controls, not
// worker coordination: exceeding one rejects a *new* upload but can never strand an already admitted
// job without room for its mandatory terminal outcome.
type ImportAdmissionLimits struct {
	MaxNonterminalJobsPerActor  int
	MaxNonterminalJobsPerTarget int
	MaxStagedBytesPerJob        int64
	MaxStagedBytesPerActor      int64
	MaxStagedBytesPerTarget     int64
	MaxStagedBytesGlobal        int64
	// Retained bytes are the durable manifest-user, result, issue, and summary rows that outlive
	// staged-body cleanup. They are reserved up front so an admitted job always has room for the
	// mandatory terminal outcome it will eventually need to record.
	MaxRetainedBytesPerJob   int64
	MaxRetainedBytesPerActor int64
	MaxRetainedBytesGlobal   int64
}

// DefaultImportAdmissionLimits returns the first-release defaults. Tune them after load tests, but
// never remove the reservation guarantee they provide.
func DefaultImportAdmissionLimits() ImportAdmissionLimits {
	return ImportAdmissionLimits{
		MaxNonterminalJobsPerActor:  3,
		MaxNonterminalJobsPerTarget: 3,
		MaxStagedBytesPerJob:        256 * 1024 * 1024,
		MaxStagedBytesPerActor:      512 * 1024 * 1024,
		MaxStagedBytesPerTarget:     512 * 1024 * 1024,
		MaxStagedBytesGlobal:        1024 * 1024 * 1024,
		MaxRetainedBytesPerJob:      512 * 1024 * 1024,
		MaxRetainedBytesPerActor:    1024 * 1024 * 1024,
		MaxRetainedBytesGlobal:      8 * 1024 * 1024 * 1024,
	}
}

// ErrAdmissionExhausted is returned when an import cannot be admitted because a named capacity limit
// is already reached. Callers map it to 429 with a Retry-After rather than a client error: the request
// was valid and may succeed later.
type ErrAdmissionExhausted struct {
	// Limit names which bound was hit, for logs and the operator-facing message.
	Limit string
}

func (e *ErrAdmissionExhausted) Error() string {
	return "import admission exhausted: " + e.Limit
}

// IsErrAdmissionExhausted reports whether err is an ErrAdmissionExhausted.
func IsErrAdmissionExhausted(err error) bool {
	var e *ErrAdmissionExhausted
	return errors.As(err, &e)
}

// nonterminalImportStates are the states a job occupies while it still holds staged input.
var nonterminalImportStates = []string{
	string(model.ImportStateAwaitingSource), string(model.ImportStateQueuedPreflight),
	string(model.ImportStatePreflighting), string(model.ImportStateAwaitingConfirmation),
	string(model.ImportStateQueuedImport), string(model.ImportStateImporting),
	string(model.ImportStateTerminalizing),
}

func (s *Store) importJobSelectQuery() sq.SelectBuilder {
	return s.getQueryBuilder().Select(importJobColumns...).From("DOCS_ImportJob")
}

// jsonbMap normalizes a nil props map to an empty one, so a nil field persists as the JSON object
// '{}' the jsonb columns default to rather than a JSON 'null' a reader would have to special-case.
func jsonbMap(m mmmodel.StringInterface) mmmodel.StringInterface {
	if m == nil {
		return mmmodel.StringInterface{}
	}
	return m
}

// ImportStagingWriter accepts normalized rows inside the open upload transaction. The inspector
// streams into it one page at a time so neither the request nor the transaction ever holds the whole
// bundle in memory.
type ImportStagingWriter interface {
	AddPage(p *model.ImportStagedPage) error
	AddManifestUser(u *model.ImportManifestUser) error
	AddIssue(i *model.ImportIssueRecord) error
}

// importStagingWriter batches rows within one transaction and tracks the byte totals admission needs.
type importStagingWriter struct {
	store *Store
	tx    sqlx.ExtContext
	jobID string

	pageBatch      sq.InsertBuilder
	pageRows       int
	pageBatchBytes int
	pageCount      int
	stagedBytes    int64

	userBatch sq.InsertBuilder
	userRows  int
	userCount int

	issueBatch sq.InsertBuilder
	issueRows  int
	issueCount int

	maxStagedBytesPerJob int64
}

// AddPage stages one normalized page, flushing the batch when it reaches its row or byte bound.
func (w *importStagingWriter) AddPage(p *model.ImportStagedPage) error {
	if p == nil {
		return &ErrInvalidInput{Entity: "ImportStagedPage", Field: "page", Value: nil}
	}
	if validErr := p.IsValid(); validErr != nil {
		return &ErrInvalidInput{Entity: "ImportStagedPage", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	propsBytes, err := jsonByteLen(jsonbMap(p.SourceProps))
	if err != nil {
		return err
	}
	rowBytes := int64(len(p.CanonicalBody) + len(p.SearchText) + len(p.Title) + len(p.SourceUserProposal) +
		len(p.ExternalId) + len(p.ParentExternalId) + len(p.SourceAuthorAccountId) +
		len(p.PlannedPageId) + len(p.ResolvedUserId) + len(p.AuthorFallbackReason) +
		propsBytes + importStagedRowOverheadBytes)
	// Fail fast on the per-job bound: there is no point streaming the rest of a bundle that can never
	// be admitted, and the caller's transaction rolls everything back anyway.
	if w.stagedBytes+rowBytes > w.maxStagedBytesPerJob {
		return &ErrAdmissionExhausted{Limit: "staged bytes per job"}
	}
	w.stagedBytes += rowBytes

	w.pageBatch = w.pageBatch.Values(
		w.jobID, p.Ordinal, p.SourceLine, p.ExternalId, p.ParentExternalId, p.SourceOrdinal, p.Restricted,
		p.Title, p.CanonicalBody, p.SearchText, p.SourceUserProposal, p.SourceAuthorAccountId,
		p.SourceCreateAt, p.SourceUpdateAt, jsonbMap(p.SourceProps),
		p.IncomingSourceContentHash, p.PreflightCurrentContentHash, p.PreflightMappingContentHash,
		p.PreflightCurrentParentId, p.PreflightMappingParentId, p.PreflightMappingUpdateAt,
		string(p.PlannedAction), p.PlannedPageId, p.ResolvedUserId, p.AuthorFallbackReason,
	)
	w.pageRows++
	w.pageCount++
	w.pageBatchBytes += len(p.CanonicalBody) + len(p.SearchText)
	if w.pageRows >= importStagedPageBatchRows || w.pageBatchBytes >= importStagedPageBatchBytes {
		return w.flushPages()
	}
	return nil
}

func (w *importStagingWriter) flushPages() error {
	if w.pageRows == 0 {
		return nil
	}
	if _, err := w.store.execBuilder(w.tx, w.pageBatch); err != nil {
		if isUniqueViolation(err) {
			return &ErrConflict{Resource: "ImportStagedPage job_id=" + w.jobID}
		}
		return errors.Wrap(err, "unable_to_save_import_staged_pages")
	}
	w.pageBatch = w.store.getQueryBuilder().Insert("DOCS_ImportStagedPage").Columns(importStagedPageColumns...)
	w.pageRows, w.pageBatchBytes = 0, 0
	return nil
}

// AddManifestUser stages one manifest user mapping.
func (w *importStagingWriter) AddManifestUser(u *model.ImportManifestUser) error {
	if u == nil {
		return &ErrInvalidInput{Entity: "ImportManifestUser", Field: "user", Value: nil}
	}
	if validErr := u.IsValid(); validErr != nil {
		return &ErrInvalidInput{Entity: "ImportManifestUser", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	w.userBatch = w.userBatch.Values(w.jobID, u.Ordinal, u.AccountId, u.ConfluenceUsername, u.MattermostUsername)
	w.userRows++
	w.userCount++
	if w.userRows >= importRowBatchRows {
		return w.flushUsers()
	}
	return nil
}

func (w *importStagingWriter) flushUsers() error {
	if w.userRows == 0 {
		return nil
	}
	if _, err := w.store.execBuilder(w.tx, w.userBatch); err != nil {
		if isUniqueViolation(err) {
			return &ErrConflict{Resource: "ImportManifestUser job_id=" + w.jobID}
		}
		return errors.Wrap(err, "unable_to_save_import_manifest_users")
	}
	w.userBatch = w.store.getQueryBuilder().Insert("DOCS_ImportManifestUser").Columns(importManifestUserColumns...)
	w.userRows = 0
	return nil
}

// AddIssue stages one issue row.
func (w *importStagingWriter) AddIssue(i *model.ImportIssueRecord) error {
	if i == nil {
		return &ErrInvalidInput{Entity: "ImportIssue", Field: "issue", Value: nil}
	}
	if validErr := i.IsValid(); validErr != nil {
		return &ErrInvalidInput{Entity: "ImportIssue", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	w.issueBatch = w.issueBatch.Values(
		w.jobID, string(i.Stage), i.Ordinal, string(i.Severity), i.Code,
		i.EntityType, i.ExternalId, i.LocalId, i.Title, i.Message, i.Remediation, jsonbMap(i.Details),
	)
	w.issueRows++
	w.issueCount++
	if w.issueRows >= importRowBatchRows {
		return w.flushIssues()
	}
	return nil
}

func (w *importStagingWriter) flushIssues() error {
	if w.issueRows == 0 {
		return nil
	}
	if _, err := w.store.execBuilder(w.tx, w.issueBatch); err != nil {
		if isUniqueViolation(err) {
			return &ErrConflict{Resource: "ImportIssue job_id=" + w.jobID}
		}
		return errors.Wrap(err, "unable_to_save_import_issues")
	}
	w.issueBatch = w.store.getQueryBuilder().Insert("DOCS_ImportIssue").Columns(importIssueColumns...)
	w.issueRows = 0
	return nil
}

// flushAll writes any partial batches.
func (w *importStagingWriter) flushAll() error {
	if err := w.flushPages(); err != nil {
		return err
	}
	if err := w.flushUsers(); err != nil {
		return err
	}
	return w.flushIssues()
}

// jsonByteLen returns the marshaled length of a props map, used for staged-byte accounting.
func jsonByteLen(m mmmodel.StringInterface) (int, error) {
	v, err := m.Value()
	if err != nil {
		return 0, errors.Wrap(err, "unable_to_size_import_props")
	}
	switch t := v.(type) {
	case string:
		return len(t), nil
	case []byte:
		return len(t), nil
	default:
		return 0, nil
	}
}

// ImportStagingResult reports what one streamed upload persisted.
type ImportStagingResult struct {
	Pages         int
	ManifestUsers int
	Issues        int
	StagedBytes   int64
}

// CreateImportJobStreaming inserts an import job and streams its normalized staged pages, manifest
// users, and inspection issues into the same transaction, then admits the result against the shared
// capacity row. fill is called with a writer while the transaction is open, so the caller (the
// inspector) can hand over one page at a time instead of materializing the whole bundle.
//
// Nothing is durable unless the whole thing commits: a late validation, count-reconciliation, or
// admission failure rolls back the entire job exactly as the plan requires.
func (s *Store) CreateImportJobStreaming(
	job *model.ImportJob,
	limits ImportAdmissionLimits,
	fill func(w ImportStagingWriter) (model.ImportBundleSummary, error),
) (_ *model.ImportJob, _ *ImportStagingResult, err error) {
	if job == nil {
		return nil, nil, &ErrInvalidInput{Entity: "ImportJob", Field: "job", Value: nil}
	}
	if validErr := job.IsValid(); validErr != nil {
		return nil, nil, &ErrInvalidInput{Entity: "ImportJob", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Cheap pre-checks first: reject an over-subscribed actor or target before parsing a large bundle
	// into the transaction.
	if err = s.checkImportJobCounts(tx, job, limits); err != nil {
		return nil, nil, err
	}
	if err = s.insertImportJob(tx, job); err != nil {
		return nil, nil, err
	}

	w := &importStagingWriter{
		store:                s,
		tx:                   tx,
		jobID:                job.Id,
		pageBatch:            s.getQueryBuilder().Insert("DOCS_ImportStagedPage").Columns(importStagedPageColumns...),
		userBatch:            s.getQueryBuilder().Insert("DOCS_ImportManifestUser").Columns(importManifestUserColumns...),
		issueBatch:           s.getQueryBuilder().Insert("DOCS_ImportIssue").Columns(importIssueColumns...),
		maxStagedBytesPerJob: limits.MaxStagedBytesPerJob,
	}
	// The bundle summary is only known once streaming has reconciled its counts, so the caller
	// returns it here and it is persisted in the same transaction as the rows it describes.
	summary, err := fill(w)
	if err != nil {
		return nil, nil, err
	}
	if err = w.flushAll(); err != nil {
		return nil, nil, err
	}
	job.BundleSummary = summary

	// Reserve a conservative retained budget covering every durable row this job may still write: a
	// preflight and an execution result plus their issues for each staged page, and the same for the
	// stale entries a full mapping set could contribute. Add what upload already retained (manifest
	// users and inspection issues) so the reservation is never smaller than the actual usage.
	job.StagedBytes = w.stagedBytes
	job.RetainedBytes = int64(w.userCount+w.issueCount) * importRetainedRowBytes
	job.RetainedReservedBytes = job.RetainedBytes +
		int64(w.pageCount+model.ImportMaxMappingsPerSource)*importRetainedRowsPerEntity*importRetainedRowBytes
	job.ProgressTotal = int64(w.pageCount)

	if err = s.admitImportCapacity(tx, job, limits); err != nil {
		return nil, nil, err
	}

	// Persist the final counts now that streaming has settled them.
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("ProgressTotal", job.ProgressTotal).
		Set("StagedBytes", job.StagedBytes).
		Set("RetainedBytes", job.RetainedBytes).
		Set("RetainedReservedBytes", job.RetainedReservedBytes).
		Set("BundleSummary", job.BundleSummary).
		Where(sq.Eq{"Id": job.Id})
	if _, err = s.execBuilder(tx, updateBuilder); err != nil {
		return nil, nil, errors.Wrap(err, "unable_to_update_import_job_counts")
	}

	if err = tx.Commit(); err != nil {
		return nil, nil, errors.Wrap(err, "commit_transaction")
	}
	return job, &ImportStagingResult{
		Pages:         w.pageCount,
		ManifestUsers: w.userCount,
		Issues:        w.issueCount,
		StagedBytes:   w.stagedBytes,
	}, nil
}

// insertImportJob writes the job row. Must be called inside tx.
func (s *Store) insertImportJob(tx sqlx.ExtContext, job *model.ImportJob) error {
	builder := s.getQueryBuilder().
		Insert("DOCS_ImportJob").
		Columns(importJobColumns...).
		Values(
			job.Id, job.ActorId, job.TeamId,
			string(job.TargetKind), job.TargetSpaceId, job.TargetSpaceExisted, job.ConfirmedSpaceTitle, job.ConfirmedSpaceDescription, job.ProvisionedChannelId,
			string(job.SourceSelectionMode), job.SelectedImportSourceId, job.SelectedSourceDisplayName, job.PreflightMappingRevision,
			string(job.State), string(job.Phase), string(job.TerminalIntent), job.MappingInputsChanged, job.InvalidationPending,
			job.ProgressCurrent, job.ProgressTotal, job.StagedBytes, job.RetainedBytes, job.RetainedReservedBytes,
			job.BundleSha256, job.BundleSummary, job.PreflightSummary, job.PreflightRevision, job.Confirmation, job.FinalSummary,
			job.ErrorCode, job.ErrorMessage, job.CancelRequestedAt,
			job.CreateAt, job.UpdateAt, job.ConfirmedAt, job.StartedAt, job.FinishedAt, job.RetainUntil,
		)
	if _, err := s.execBuilder(tx, builder); err != nil {
		if isUniqueViolation(err) {
			return &ErrConflict{Resource: "ImportJob id=" + job.Id}
		}
		return errors.Wrap(err, "unable_to_save_import_job")
	}
	return nil
}

// checkImportJobCounts rejects an upload when the actor or target already holds too many nonterminal
// staged jobs. Must be called inside tx.
func (s *Store) checkImportJobCounts(tx sqlx.ExtContext, job *model.ImportJob, limits ImportAdmissionLimits) error {
	count := func(column, value string) (int, error) {
		var n int
		builder := s.getQueryBuilder().
			Select("COUNT(*)").
			From("DOCS_ImportJob").
			Where(sq.Eq{column: value, "State": nonterminalImportStates})
		if err := s.getBuilder(tx, &n, builder); err != nil {
			return 0, errors.Wrap(err, "unable_to_count_import_jobs")
		}
		return n, nil
	}
	actorJobs, err := count("ActorId", job.ActorId)
	if err != nil {
		return err
	}
	if actorJobs >= limits.MaxNonterminalJobsPerActor {
		return &ErrAdmissionExhausted{Limit: "concurrent import jobs per user"}
	}
	targetJobs, err := count("TargetSpaceId", job.TargetSpaceId)
	if err != nil {
		return err
	}
	if targetJobs >= limits.MaxNonterminalJobsPerTarget {
		return &ErrAdmissionExhausted{Limit: "concurrent import jobs per target Space"}
	}
	return nil
}

// admitImportCapacity locks the singleton capacity row, rechecks the per-actor, per-target, and
// global staged-byte totals, and increments the global reservations. Locking one row makes upload
// admission, preflight publication, terminalization, and cleanup share a single atomic accounting
// boundary so they cannot oversubscribe each other. Must be called inside tx.
func (s *Store) admitImportCapacity(tx sqlx.ExtContext, job *model.ImportJob, limits ImportAdmissionLimits) error {
	var capacity model.ImportCapacity
	lockBuilder := s.getQueryBuilder().
		Select("Id", "ReservedStagedBytes", "ReservedRetainedBytes", "UpdateAt").
		From("DOCS_ImportCapacity").
		Where(sq.Eq{"Id": 1}).
		Suffix("FOR UPDATE")
	if err := s.getBuilder(tx, &capacity, lockBuilder); err != nil {
		return errors.Wrap(err, "unable_to_lock_import_capacity")
	}

	if job.StagedBytes > limits.MaxStagedBytesPerJob {
		return &ErrAdmissionExhausted{Limit: "staged bytes per job"}
	}
	if capacity.ReservedStagedBytes+job.StagedBytes > limits.MaxStagedBytesGlobal {
		return &ErrAdmissionExhausted{Limit: "global staged bytes"}
	}

	sumStaged := func(column, value string) (int64, error) {
		var total int64
		builder := s.getQueryBuilder().
			Select("COALESCE(SUM(StagedBytes), 0)").
			From("DOCS_ImportJob").
			Where(sq.Eq{column: value}).
			Where(sq.NotEq{"Id": job.Id})
		if err := s.getBuilder(tx, &total, builder); err != nil {
			return 0, errors.Wrap(err, "unable_to_sum_import_staged_bytes")
		}
		return total, nil
	}
	actorBytes, err := sumStaged("ActorId", job.ActorId)
	if err != nil {
		return err
	}
	if actorBytes+job.StagedBytes > limits.MaxStagedBytesPerActor {
		return &ErrAdmissionExhausted{Limit: "staged bytes per user"}
	}
	targetBytes, err := sumStaged("TargetSpaceId", job.TargetSpaceId)
	if err != nil {
		return err
	}
	if targetBytes+job.StagedBytes > limits.MaxStagedBytesPerTarget {
		return &ErrAdmissionExhausted{Limit: "staged bytes per target Space"}
	}

	// The retained reservation is bounded on the same locked row, so a job is only admitted when the
	// durable rows it will eventually need are already accounted for.
	if job.RetainedReservedBytes > limits.MaxRetainedBytesPerJob {
		return &ErrAdmissionExhausted{Limit: "retained bytes per job"}
	}
	if capacity.ReservedRetainedBytes+job.RetainedReservedBytes > limits.MaxRetainedBytesGlobal {
		return &ErrAdmissionExhausted{Limit: "global retained bytes"}
	}
	var actorRetained int64
	retainedBuilder := s.getQueryBuilder().
		Select("COALESCE(SUM(RetainedReservedBytes), 0)").
		From("DOCS_ImportJob").
		Where(sq.Eq{"ActorId": job.ActorId}).
		Where(sq.NotEq{"Id": job.Id})
	if err := s.getBuilder(tx, &actorRetained, retainedBuilder); err != nil {
		return errors.Wrap(err, "unable_to_sum_import_retained_bytes")
	}
	if actorRetained+job.RetainedReservedBytes > limits.MaxRetainedBytesPerActor {
		return &ErrAdmissionExhausted{Limit: "retained bytes per user"}
	}

	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportCapacity").
		Set("ReservedStagedBytes", capacity.ReservedStagedBytes+job.StagedBytes).
		Set("ReservedRetainedBytes", capacity.ReservedRetainedBytes+job.RetainedReservedBytes).
		Set("UpdateAt", mmmodel.GetMillis()).
		Where(sq.Eq{"Id": 1})
	if _, err := s.execBuilder(tx, updateBuilder); err != nil {
		return errors.Wrap(err, "unable_to_update_import_capacity")
	}
	return nil
}

// GetImportCapacity returns the singleton capacity row, for diagnostics and tests.
func (s *Store) GetImportCapacity() (*model.ImportCapacity, error) {
	var capacity model.ImportCapacity
	builder := s.getQueryBuilder().
		Select("Id", "ReservedStagedBytes", "ReservedRetainedBytes", "UpdateAt").
		From("DOCS_ImportCapacity").
		Where(sq.Eq{"Id": 1})
	if err := s.getBuilder(s.db, &capacity, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_capacity")
	}
	return &capacity, nil
}

// GetImportJob returns the job with the given ID, or ErrNotFound.
func (s *Store) GetImportJob(jobID string) (*model.ImportJob, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "id", Value: jobID}
	}
	var job model.ImportJob
	if err := s.getBuilder(s.db, &job, s.importJobSelectQuery().Where(sq.Eq{"Id": jobID})); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ImportJob", ID: jobID}
		}
		return nil, errors.Wrap(err, "unable_to_get_import_job")
	}
	return &job, nil
}

// GetImportJobsForActor returns one page of the actor's own jobs, newest first. V1 job visibility is
// actor-only, so there is deliberately no unfiltered variant. teamID, when non-empty, restricts the
// result to that team. limit is expected to be perPage+1 so the caller derives has-more from a probe
// row, matching every other paginated read in this repository.
func (s *Store) GetImportJobsForActor(actorID, teamID string, offset, limit int) ([]*model.ImportJob, error) {
	if actorID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "actorID", Value: actorID}
	}
	if err := requirePositiveLimit("ImportJob", limit); err != nil {
		return nil, err
	}
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"ActorId": actorID}).
		OrderBy("CreateAt DESC", "Id DESC")
	if teamID != "" {
		builder = builder.Where(sq.Eq{"TeamId": teamID})
	}
	builder = applyLimitOffset(builder, offset, limit)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_jobs_for_actor")
	}
	return jobs, nil
}

// GetImportIssues returns one page of a job's issues ordered by (Stage, Ordinal). stage and severity
// are optional filters. limit is expected to be perPage+1 for has-more probing.
func (s *Store) GetImportIssues(jobID, stage, severity string, offset, limit int) ([]*model.ImportIssueRecord, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "jobID", Value: jobID}
	}
	if err := requirePositiveLimit("ImportIssue", limit); err != nil {
		return nil, err
	}
	builder := s.getQueryBuilder().
		Select(importIssueColumns...).
		From("DOCS_ImportIssue").
		Where(sq.Eq{"JobId": jobID}).
		OrderBy("Stage ASC", "Ordinal ASC")
	if stage != "" {
		builder = builder.Where(sq.Eq{"Stage": stage})
	}
	if severity != "" {
		builder = builder.Where(sq.Eq{"Severity": severity})
	}
	builder = applyLimitOffset(builder, offset, limit)

	issues := []*model.ImportIssueRecord{}
	if err := s.selectBuilder(s.db, &issues, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_issues")
	}
	return issues, nil
}

// CountImportStagedPages returns how many staged pages a job has, without loading any bodies.
func (s *Store) CountImportStagedPages(jobID string) (int, error) {
	if jobID == "" {
		return 0, &ErrInvalidInput{Entity: "ImportStagedPage", Field: "jobID", Value: jobID}
	}
	var count int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": jobID})
	if err := s.getBuilder(s.db, &count, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_count_import_staged_pages")
	}
	return count, nil
}

// GetImportManifestUsers returns a job's durable manifest user mappings in manifest order. Author
// resolution reads these rather than any in-memory manifest, so it survives a process restart.
func (s *Store) GetImportManifestUsers(jobID string) ([]*model.ImportManifestUser, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportManifestUser", Field: "jobID", Value: jobID}
	}
	builder := s.getQueryBuilder().
		Select(importManifestUserColumns...).
		From("DOCS_ImportManifestUser").
		Where(sq.Eq{"JobId": jobID}).
		OrderBy("Ordinal ASC")

	users := []*model.ImportManifestUser{}
	if err := s.selectBuilder(s.db, &users, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_manifest_users")
	}
	return users, nil
}

// GetImportSourcesForSpace returns every ImportSource belonging to the given target Docs Space,
// oldest first. These are candidate suggestions only: the user always selects a source explicitly,
// so this deliberately performs no scoring or auto-selection of its own.
func (s *Store) GetImportSourcesForSpace(spaceID string) ([]*model.ImportSource, error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportSource", Field: "spaceID", Value: spaceID}
	}
	builder := s.getQueryBuilder().
		Select(importSourceColumns...).
		From("DOCS_ImportSource").
		Where(sq.Eq{"SpaceId": spaceID}).
		OrderBy("CreateAt ASC", "Id ASC")

	sources := []*model.ImportSource{}
	if err := s.selectBuilder(s.db, &sources, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_sources_for_space")
	}
	return sources, nil
}

// CountImportSourceMappedPages returns, per ImportSource ID, how many page mappings it owns. Used to
// show a candidate's size. An ID with no mappings is absent from the result rather than zero-valued.
func (s *Store) CountImportSourceMappedPages(sourceIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if len(sourceIDs) == 0 {
		return counts, nil
	}
	builder := s.getQueryBuilder().
		Select("ImportSourceId", "COUNT(*) AS MappedCount").
		From("DOCS_ImportEntity").
		Where(sq.Eq{"ImportSourceId": sourceIDs, "EntityType": model.ImportEntityTypePage}).
		GroupBy("ImportSourceId")

	var rows []struct {
		ImportSourceId string
		MappedCount    int
	}
	if err := s.selectBuilder(s.db, &rows, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_count_import_source_mapped_pages")
	}
	for _, r := range rows {
		counts[r.ImportSourceId] = r.MappedCount
	}
	return counts, nil
}

// compile-time assertion that the batching writer satisfies the injected sink interface.
var _ ImportStagingWriter = (*importStagingWriter)(nil)

// --- cancellation, expiry, and capacity release ---

// cancelableImportStates are the pre-execution states a job may be canceled from today. They are the
// only states reachable in this milestone, and none of them can have written a page, so cancellation
// reduces to marking the job canceled and releasing its staged reservation.
//
// Once execution states exist, cancellation instead routes through terminalizing so the terminalizer
// can record a durable not-attempted outcome for every staged page before the job becomes terminal;
// this method deliberately refuses those states rather than short-circuiting that guarantee.
var cancelableImportStates = []string{
	string(model.ImportStateAwaitingSource),
	string(model.ImportStateQueuedPreflight),
	string(model.ImportStateAwaitingConfirmation),
}

// CancelImportJob transitions a pre-execution job to canceled, deletes its staged page bodies, and
// releases the staged bytes it reserved — all in one transaction, so capacity is never lost to a
// half-applied cancel. It returns ErrConflict when the job is not in a cancelable state (for example
// because the worker already advanced it), which the caller surfaces as a 409.
func (s *Store) CancelImportJob(jobID, actorID, errorCode string) (_ *model.ImportJob, err error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "id", Value: jobID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// Lock the job row first, matching the worker's lock order, so a concurrent state change either
	// precedes this cancel or observes it.
	var job model.ImportJob
	lockBuilder := s.importJobSelectQuery().Where(sq.Eq{"Id": jobID}).Suffix("FOR UPDATE")
	if err = s.getBuilder(tx, &job, lockBuilder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ImportJob", ID: jobID}
		}
		return nil, errors.Wrap(err, "unable_to_get_import_job")
	}
	if actorID != "" && job.ActorId != actorID {
		// Report as absent rather than forbidden, matching read visibility.
		return nil, &ErrNotFound{EntityName: "ImportJob", ID: jobID}
	}
	if !slices.Contains(cancelableImportStates, string(job.State)) {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}

	now := mmmodel.GetMillis()
	if err = s.releaseStagedBytes(tx, &job, now); err != nil {
		return nil, err
	}

	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateCanceled)).
		Set("TerminalIntent", string(model.ImportIntentCanceled)).
		Set("CancelRequestedAt", now).
		Set("FinishedAt", now).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Set("RetainUntil", now+importTerminalRetentionMillis).
		Where(sq.Eq{"Id": jobID, "State": string(job.State)})
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return nil, errors.Wrap(err, "unable_to_cancel_import_job")
	}
	if err = checkRowsAffected(result, "ImportJob", jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	job.State = model.ImportStateCanceled
	job.TerminalIntent = model.ImportIntentCanceled
	job.FinishedAt = now
	job.StagedBytes = 0
	return &job, nil
}

// importTerminalRetentionMillis is how long a terminal job's summaries, results, and issues are kept
// so its report stays downloadable. Distinct from the seven-day review window an unconfirmed job gets.
const importTerminalRetentionMillis = int64(90 * 24 * 60 * 60 * 1000)

// releaseStagedBytes deletes a job's body-bearing staged rows and decrements the matching global
// reservation, leaving durable results, issues, and manifest users intact so the report survives.
// Deleting the rows and releasing the reservation in one transaction is what keeps the accounting
// honest: a crash between them would otherwise leak capacity permanently. Must be called inside tx.
func (s *Store) releaseStagedBytes(tx sqlx.ExtContext, job *model.ImportJob, now int64) error {
	deleteBuilder := s.getQueryBuilder().
		Delete("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": job.Id})
	if _, err := s.execBuilder(tx, deleteBuilder); err != nil {
		return errors.Wrap(err, "unable_to_delete_import_staged_pages")
	}
	if job.StagedBytes == 0 {
		return nil
	}
	// GREATEST guards the invariant that the reservation can never go negative, even if an earlier
	// release was somehow double-applied.
	capacityBuilder := s.getQueryBuilder().
		Update("DOCS_ImportCapacity").
		Set("ReservedStagedBytes", sq.Expr("GREATEST(ReservedStagedBytes - ?, 0)", job.StagedBytes)).
		Set("UpdateAt", now).
		Where(sq.Eq{"Id": 1})
	if _, err := s.execBuilder(tx, capacityBuilder); err != nil {
		return errors.Wrap(err, "unable_to_release_import_staged_capacity")
	}
	zeroBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("StagedBytes", 0).
		Where(sq.Eq{"Id": job.Id})
	if _, err := s.execBuilder(tx, zeroBuilder); err != nil {
		return errors.Wrap(err, "unable_to_zero_import_staged_bytes")
	}
	return nil
}

// ImportCleanupCounts reports what one maintenance sweep did, for the operator log line.
type ImportCleanupCounts struct {
	ExpiredJobs         int
	PurgedStagedJobs    int
	DeletedJobs         int
	ReleasedStagedBytes int64
}

// importCleanupBatch bounds how many jobs one sweep touches per category, so a large backlog is
// worked down over successive runs instead of in one long transaction.
const importCleanupBatch = 100

// ExpireStalledImportJobs cancels pre-execution jobs that are past their deadline, releasing their
// staged capacity. Without this an abandoned upload would hold its share of the per-user and global
// admission budget forever, eventually locking the user out of importing.
//
// It covers every cancelable pre-execution state, not just the two that wait on a human: a job queued
// for preflight is equally abandoned if nothing has advanced it within the retention window (which is
// the normal case while no worker exists), and a healthy worker moves such a job within seconds, so
// this can never race real progress. Execution states are deliberately excluded — those must go
// through terminalization so every staged page gets a durable outcome first.
func (s *Store) ExpireStalledImportJobs(now int64, errorCode string) (int, int64, error) {
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"State": cancelableImportStates}).
		Where(sq.LtOrEq{"RetainUntil": now}).
		OrderBy("RetainUntil ASC", "Id ASC")
	builder = applyLimitOffset(builder, 0, importCleanupBatch)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return 0, 0, errors.Wrap(err, "unable_to_list_expired_import_jobs")
	}

	expired := 0
	var released int64
	for _, job := range jobs {
		// Cancel each job in its own transaction so one contended row cannot stall the whole sweep.
		// An actorID of "" means "system", bypassing the ownership check.
		canceled, err := s.CancelImportJob(job.Id, "", errorCode)
		if err != nil {
			if IsErrConflict(err) || IsErrNotFound(err) {
				// The job advanced or vanished between the scan and the cancel; leave it to the next run.
				continue
			}
			return expired, released, err
		}
		expired++
		released += job.StagedBytes
		_ = canceled
	}
	return expired, released, nil
}

// PurgeTerminalImportStagedBodies deletes staged page bodies for terminal jobs older than the body
// retention window, releasing their staged capacity while keeping results, issues, manifest users, and
// summaries so the report is still downloadable.
func (s *Store) PurgeTerminalImportStagedBodies(olderThan, now int64) (int, int64, error) {
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"State": []string{
			string(model.ImportStateCompleted), string(model.ImportStateCompletedWithIssues),
			string(model.ImportStateFailed), string(model.ImportStateCanceled),
		}}).
		Where(sq.Gt{"StagedBytes": 0}).
		Where(sq.LtOrEq{"FinishedAt": olderThan}).
		Where(sq.Gt{"FinishedAt": 0}).
		OrderBy("FinishedAt ASC", "Id ASC")
	builder = applyLimitOffset(builder, 0, importCleanupBatch)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return 0, 0, errors.Wrap(err, "unable_to_list_purgeable_import_jobs")
	}

	purged := 0
	var released int64
	for _, job := range jobs {
		if err := s.purgeOneJobStagedBodies(job, now); err != nil {
			return purged, released, err
		}
		purged++
		released += job.StagedBytes
	}
	return purged, released, nil
}

// purgeOneJobStagedBodies runs one job's staged-body deletion and capacity release atomically.
func (s *Store) purgeOneJobStagedBodies(job *model.ImportJob, now int64) (err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	var locked model.ImportJob
	lockBuilder := s.importJobSelectQuery().Where(sq.Eq{"Id": job.Id}).Suffix("FOR UPDATE")
	if err = s.getBuilder(tx, &locked, lockBuilder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // deleted underneath us; nothing to release
		}
		return errors.Wrap(err, "unable_to_get_import_job")
	}
	if locked.StagedBytes == 0 {
		return nil // another sweep already released it
	}
	if err = s.releaseStagedBytes(tx, &locked, now); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteExpiredImportJobs removes terminal jobs past their 90-day retention, releasing the retained
// reservation each one held. Cascades take their staged pages, manifest users, channel attempts,
// results, and issues; ImportSources and their page mappings are deliberately never touched, because
// those are the durable identity a later reimport depends on.
func (s *Store) DeleteExpiredImportJobs(now int64) (int, error) {
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"State": []string{
			string(model.ImportStateCompleted), string(model.ImportStateCompletedWithIssues),
			string(model.ImportStateFailed), string(model.ImportStateCanceled),
		}}).
		Where(sq.LtOrEq{"RetainUntil": now}).
		OrderBy("RetainUntil ASC", "Id ASC")
	builder = applyLimitOffset(builder, 0, importCleanupBatch)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_list_deletable_import_jobs")
	}

	deleted := 0
	for _, job := range jobs {
		if err := s.deleteOneImportJob(job, now); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// deleteOneImportJob deletes one job and releases both of its reservations atomically.
func (s *Store) deleteOneImportJob(job *model.ImportJob, now int64) (err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	var locked model.ImportJob
	lockBuilder := s.importJobSelectQuery().Where(sq.Eq{"Id": job.Id}).Suffix("FOR UPDATE")
	if err = s.getBuilder(tx, &locked, lockBuilder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return errors.Wrap(err, "unable_to_get_import_job")
	}

	capacityBuilder := s.getQueryBuilder().
		Update("DOCS_ImportCapacity").
		Set("ReservedStagedBytes", sq.Expr("GREATEST(ReservedStagedBytes - ?, 0)", locked.StagedBytes)).
		Set("ReservedRetainedBytes", sq.Expr("GREATEST(ReservedRetainedBytes - ?, 0)", locked.RetainedReservedBytes)).
		Set("UpdateAt", now).
		Where(sq.Eq{"Id": 1})
	if _, err = s.execBuilder(tx, capacityBuilder); err != nil {
		return errors.Wrap(err, "unable_to_release_import_capacity")
	}

	deleteBuilder := s.getQueryBuilder().Delete("DOCS_ImportJob").Where(sq.Eq{"Id": locked.Id})
	if _, err = s.execBuilder(tx, deleteBuilder); err != nil {
		return errors.Wrap(err, "unable_to_delete_import_job")
	}
	return tx.Commit()
}
