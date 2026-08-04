// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

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

// importStagedRowOverheadBytes is the per-staged-page fixed cost charged against the staged-byte
// budget on top of the page's variable text, covering ordinals, hashes, identifiers, and row overhead.
const importStagedRowOverheadBytes = 512

// importRetainedRowBytes is the conservative per-row budget reserved for the durable report rows a
// job will eventually need (preflight result, execution result, and their issues).
const importRetainedRowBytes = 1024

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

	// Reserve a conservative retained budget covering the preflight and terminal report rows this job
	// will need, so capacity exhaustion can never strand it without room for its mandatory outcome.
	job.StagedBytes = w.stagedBytes
	job.RetainedReservedBytes = int64(w.pageCount+model.ImportMaxMappingsPerSource) * importRetainedRowBytes
	job.ProgressTotal = int64(w.pageCount)

	if err = s.admitImportCapacity(tx, job, limits); err != nil {
		return nil, nil, err
	}

	// Persist the final counts now that streaming has settled them.
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("ProgressTotal", job.ProgressTotal).
		Set("StagedBytes", job.StagedBytes).
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
