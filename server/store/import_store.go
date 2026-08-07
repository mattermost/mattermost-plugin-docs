// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"database/sql/driver"
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
	"ProgressCurrent", "ProgressTotal", "StagedBytes", "RetainedBytes", "RetainedIssueBytes",
	"PreflightRetainedBytes", "PreflightRetainedIssueBytes", "RetainedReservedBytes",
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

// importMandatoryResultsPerEntity is how many durable result rows one page or mapping is guaranteed to
// need: a preflight outcome and an execution outcome. These are unconditional — a terminal job must
// carry an outcome for every entity even when it never ran — so they are reserved at the worst-case row
// size the model admits (model.ImportRetainedResultRowMaxBytes), never at an average.
const importMandatoryResultsPerEntity = 2

// retainedIssueRowBytes returns the measured retained cost of one issue row. Measuring beats charging a
// flat figure: issue text spans three orders of magnitude, so a flat charge would either exhaust the
// budget on short rows or silently overrun it on long ones.
func retainedIssueRowBytes(i *model.ImportIssueRecord, detailsBytes int) int64 {
	return int64(len(i.Code) + len(i.EntityType) + len(i.ExternalId) + len(i.LocalId) +
		len(i.Title) + len(i.Message) + len(i.Remediation) + detailsBytes + importRetainedRowFixedBytes)
}

// retainedManifestUserRowBytes returns the measured retained cost of one manifest-user row.
func retainedManifestUserRowBytes(u *model.ImportManifestUser) int64 {
	return int64(len(u.AccountId) + len(u.ConfluenceUsername) + len(u.MattermostUsername) +
		importRetainedRowFixedBytes)
}

// retainedResultRowBytes returns the measured retained cost of one result row.
func retainedResultRowBytes(r *model.ImportResultRecord, detailsBytes int) int64 {
	return int64(len(r.EntityType) + len(r.ExternalId) + len(r.LocalId) + len(r.Title) +
		len(r.PlannedAction) + len(r.ActualAction) + len(r.Outcome) + detailsBytes +
		importRetainedRowFixedBytes)
}

// importRetainedRowFixedBytes is the per-row fixed cost added to every measured retained row: page
// header share, job id, ordinal, and timestamps.
const importRetainedRowFixedBytes = 128

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
	// staged-body cleanup. The mandatory part — one outcome per entity, at the worst-case row size the
	// model admits — is reserved up front, so an admitted job always has room to record what happened
	// to every page it staged. The discretionary part (explanatory issues) is a flat allowance the
	// report writers must charge against; see model.ImportJob.IssueBudgetRemaining.
	//
	// Terminal jobs are trued up to their measured usage (finalizeRetainedReservation), so a reservation
	// this large is only ever held by jobs that could still write.
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

	// Measured sizes of the durable rows this upload wrote, split by budget pool: mandatory rows
	// (manifest users) against the job's own total, and issue rows against the flat issue allowance.
	// Unlike stagedBytes neither is released by cleanup, so the reservation the job carries for the rest
	// of its life is built on top of both.
	retainedMandatoryBytes int64
	retainedIssueBytes     int64

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
	w.retainedMandatoryBytes += retainedManifestUserRowBytes(u)
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
	detailsBytes, err := jsonByteLen(jsonbMap(i.Details))
	if err != nil {
		return err
	}
	w.issueBatch = w.issueBatch.Values(
		w.jobID, string(i.Stage), i.Ordinal, string(i.Severity), i.Code,
		i.EntityType, i.ExternalId, i.LocalId, i.Title, i.Message, i.Remediation, jsonbMap(i.Details),
	)
	w.issueRows++
	w.issueCount++
	w.retainedIssueBytes += retainedIssueRowBytes(i, detailsBytes)
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

// summaryByteLen returns the serialized length of a jsonb summary column, for retained accounting. The
// summaries are durable report content that outlives staged-body cleanup, so leaving them uncounted
// would understate what a job permanently occupies.
func summaryByteLen(v driver.Valuer) (int64, error) {
	raw, err := v.Value()
	if err != nil {
		return 0, errors.Wrap(err, "unable_to_size_import_summary")
	}
	switch t := raw.(type) {
	case string:
		return int64(len(t)), nil
	case []byte:
		return int64(len(t)), nil
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

	// Reserve the retained budget. It has three parts, and only the first is already spent:
	//
	//  1. what this upload actually retained — measured manifest-user and inspection-issue rows plus
	//     the serialized bundle summary, not a row count times a flat figure;
	//  2. the mandatory per-entity outcomes still to come, at the worst-case row size the model
	//     admits, for every staged page and for the stale entries a full mapping set could contribute;
	//  3. one flat discretionary allowance for the issues that will explain those outcomes.
	//
	// Parts 2 and 3 are what make the "an admitted job always has room for its terminal outcome"
	// guarantee real: part 2 can never be short, and part 3 is enforced by IssueBudgetRemaining rather
	// than assumed.
	//
	// The issue allowance is reserved whole rather than as "what inspection has already spent", so the
	// pools stay separable: a job's issue rows are bounded by the allowance and its mandatory outcomes by
	// the rest, and neither can eat the other. Taking the larger of the allowance and actual inspection
	// usage keeps the reservation at or above real usage even for a bundle whose findings alone exceed
	// the allowance — such a job simply has no room left for preflight or execution issues, which
	// IssueBudgetRemaining reports as zero rather than silently overrunning.
	summaryBytes, err := summaryByteLen(job.BundleSummary)
	if err != nil {
		return nil, nil, err
	}
	job.StagedBytes = w.stagedBytes
	job.RetainedIssueBytes = w.retainedIssueBytes
	job.RetainedBytes = w.retainedMandatoryBytes + w.retainedIssueBytes + summaryBytes
	job.RetainedReservedBytes = w.retainedMandatoryBytes + summaryBytes +
		max(int64(model.ImportRetainedIssueBudgetBytes), w.retainedIssueBytes) +
		int64(w.pageCount+model.ImportMaxMappingsPerSource)*
			importMandatoryResultsPerEntity*model.ImportRetainedResultRowMaxBytes
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
		Set("RetainedIssueBytes", job.RetainedIssueBytes).
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
			job.ProgressCurrent, job.ProgressTotal, job.StagedBytes, job.RetainedBytes, job.RetainedIssueBytes,
			job.PreflightRetainedBytes, job.PreflightRetainedIssueBytes, job.RetainedReservedBytes,
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

// cancelableImportStates are the pre-execution states a job may be canceled from today. None of them can
// have written a page, so cancellation never has to undo work — but it does still have to record what the
// bundle contained before it throws the staged rows away.
//
// queued_import is included even though the user has already confirmed: until execution exists a confirmed
// job simply waits, and a state that can neither advance nor be canceled would hold its staged bytes and
// its slot until an operator intervened. Cancelling one is safe precisely because nothing has been written
// yet.
//
// Once execution states exist, cancellation from importing instead routes through terminalizing so the
// terminalizer can reconcile partially applied pages; this method deliberately refuses that state rather
// than short-circuiting the reconciliation.
var cancelableImportStates = []string{
	string(model.ImportStateAwaitingSource),
	string(model.ImportStateQueuedPreflight),
	string(model.ImportStateAwaitingConfirmation),
	string(model.ImportStateQueuedImport),
}

// CancelImportJob transitions a pre-execution job to canceled: it records a durable not-attempted
// outcome for every staged page, writes the final summary, deletes the staged page bodies, and trues up
// both reservations — all in one transaction, so capacity is never lost to a half-applied cancel and a
// canceled job's report is never left unable to say which pages it held. It returns ErrConflict when the
// job is not in a cancelable state (for example because the worker already advanced it), which the
// caller surfaces as a 409.
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
	if err = s.finishImportJob(tx, &job, model.ImportIntentCanceled, errorCode, now); err != nil {
		return nil, err
	}
	job.CancelRequestedAt = now
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return &job, nil
}

// TerminalizeImportJob completes a job sitting in terminalizing, honouring the intent recorded when it
// entered that state.
//
// Terminalization is worker work rather than part of the transition that decided the outcome, because the
// durable report has to be written before the job is terminal: a crash mid-way must resume, not leave a
// terminal job with an empty report. It is idempotent — outcomes are inserted only for entities that have
// none — so a restart re-runs it safely.
func (s *Store) TerminalizeImportJob(jobID string) (_ *model.ImportJob, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStateTerminalizing {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if job.TerminalIntent == model.ImportIntentNone {
		// The state machine sets the intent in the same transaction that enters terminalizing, so an
		// intentless job here is a corrupted row rather than a race. Failing loudly beats inventing one.
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "TerminalIntent", Value: ""}
	}

	now := mmmodel.GetMillis()
	if err = s.finishImportJob(tx, job, job.TerminalIntent, job.ErrorCode, now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return job, nil
}

// finishImportJob is the one path from "this job is over" to a terminal state, shared by cancellation and
// worker terminalization. It records a durable outcome for every entity, writes the final summary, releases
// the staged reservation, and trues up the retained one — all in the caller's transaction, so capacity is
// never lost to a half-applied finish and a terminal job's report can always say which pages it held.
//
// The job value is updated in place to mirror every column written, so the caller returns what a
// subsequent read would return rather than a half-stale copy. UpdateAt reproduces monotonicBump's
// GREATEST(UpdateAt+1, now) exactly, which is why it needs no re-read.
func (s *Store) finishImportJob(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	intent model.ImportTerminalIntent,
	errorCode string,
	now int64,
) error {
	previousState := job.State

	// Record the durable outcomes first: releaseStagedBytes deletes the staged rows, and with them the only
	// record of which pages the bundle contained. Every staged page must have a terminal outcome before the
	// job becomes terminal, so this cannot be deferred to a later sweep.
	outcome := model.ImportOutcomeNotAttemptedCancel
	if intent == model.ImportIntentFailed {
		outcome = model.ImportOutcomeNotAttemptedFailure
	}
	outcomes, err := s.recordNotAttemptedOutcomes(tx, job, outcome, errorCode, now)
	if err != nil {
		return err
	}
	finalSummary := model.ImportFinalSummary{
		Manifest: job.BundleSummary.Counts,
		Actions: model.ImportActionCounts{
			NotAttempted: outcomes.pages + outcomes.stale,
			Stale:        outcomes.stale,
		},
	}
	if total := outcomes.pages + outcomes.stale; total > 0 {
		finalSummary.Outcomes = map[string]int{string(outcome): total}
	}
	// Charge the summary before the reservation is trued up: it is durable JSONB bounded at
	// ImportSummaryMaxBytes, so leaving it uncounted would understate what the job permanently holds by
	// exactly the amount the accounting is meant to track.
	finalSummaryBytes, err := summaryByteLen(finalSummary)
	if err != nil {
		return err
	}
	job.RetainedBytes += finalSummaryBytes

	if err = s.releaseStagedBytes(tx, job, now); err != nil {
		return err
	}
	// Trueing up the retained reservation is what keeps finishing a job from trading a fast capacity leak
	// for a slow one: the job keeps only what it actually retained, so it no longer holds a whole
	// execution's worth of budget for the ninety-day retention window.
	if err = s.finalizeRetainedReservation(tx, job, now); err != nil {
		return err
	}

	finalState, err := s.terminalStateFor(tx, job.Id, intent)
	if err != nil {
		return err
	}
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(finalState)).
		Set("TerminalIntent", string(intent)).
		Set("ErrorCode", errorCode).
		Set("FinalSummary", finalSummary).
		Set("FinishedAt", now).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Set("RetainUntil", now+importTerminalRetentionMillis).
		Where(sq.Eq{"Id": job.Id, "State": string(previousState)})
	if intent == model.ImportIntentCanceled {
		updateBuilder = updateBuilder.Set("CancelRequestedAt", now)
	}
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return errors.Wrap(err, "unable_to_finish_import_job")
	}
	if err = checkRowsAffected(result, "ImportJob", job.Id); err != nil {
		return err
	}

	job.State = finalState
	job.TerminalIntent = intent
	job.ErrorCode = errorCode
	job.FinalSummary = finalSummary
	job.FinishedAt = now
	job.UpdateAt = max(job.UpdateAt+1, now)
	job.RetainUntil = now + importTerminalRetentionMillis
	job.StagedBytes = 0
	return nil
}

// terminalStateFor maps a terminal intent onto the state the job lands in. A completed job that recorded
// any error-severity finding lands in completed_with_issues rather than completed, so "it worked" never
// hides a page that did not. Must be called inside tx.
func (s *Store) terminalStateFor(tx sqlx.ExtContext, jobID string, intent model.ImportTerminalIntent) (model.ImportJobState, error) {
	switch intent {
	case model.ImportIntentCanceled:
		return model.ImportStateCanceled, nil
	case model.ImportIntentFailed:
		return model.ImportStateFailed, nil
	}
	var errorIssues int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportIssue").
		Where(sq.Eq{"JobId": jobID, "Severity": string(model.ImportSeverityError)})
	if err := s.getBuilder(tx, &errorIssues, builder); err != nil {
		return "", errors.Wrap(err, "unable_to_count_import_error_issues")
	}
	if errorIssues > 0 {
		return model.ImportStateCompletedWithIssues, nil
	}
	return model.ImportStateCompleted, nil
}

// importResultColumns are the DOCS_ImportResult columns, in insert order.
var importResultColumns = []string{
	"JobId", "Stage", "Ordinal", "EntityType", "ExternalId", "LocalId", "Title",
	"PlannedAction", "ActualAction", "Outcome", "Details", "CreateAt", "UpdateAt",
}

// importEntityIdentity is the subset of an entity needed to record its terminal outcome. Only these
// columns are read so terminalizing a five-thousand-page job never pulls its bodies into memory.
type importEntityIdentity struct {
	Ordinal       int
	ExternalId    string
	Title         string
	PlannedAction string
}

// importOutcomeCounts reports how many terminal outcomes were recorded, by the source that supplied the
// entity's identity.
type importOutcomeCounts struct {
	// pages are outcomes derived from staged pages: the bundle's own content.
	pages int
	// stale are outcomes derived from preflight results with no staged page, i.e. mappings preflight
	// classified as no longer present in the bundle.
	stale int
}

// recordNotAttemptedOutcomes writes one execution-stage not-attempted result for every entity a job being
// canceled has durable input for, and charges their measured size against the job's retained total. Must
// be called inside tx, and before the staged rows are deleted.
//
// Two sources are covered, because a job's entities are not only its staged pages. Anything preflight
// already classified — notably the stale mappings whose ordinals start at model.ImportStaleOrdinalBase —
// has a preflight result but no staged page, and would otherwise reach a terminal state with a plan and
// no outcome. Preflight rows are the authority for those: they are what preflight actually decided,
// rather than a guess reconstructed from the mapping table.
func (s *Store) recordNotAttemptedOutcomes(tx sqlx.ExtContext, job *model.ImportJob, outcome model.ImportOutcome, errorCode string, now int64) (importOutcomeCounts, error) {
	details := mmmodel.StringInterface{"finished_from_state": string(job.State)}
	if errorCode != "" {
		details["reason"] = errorCode
	}
	detailsBytes, err := jsonByteLen(details)
	if err != nil {
		return importOutcomeCounts{}, err
	}

	var counts importOutcomeCounts
	// Staged pages first: their ordinals are the execution stage's own key range. The anti-join is what
	// makes terminalization idempotent — a page that already has an execution result keeps it, so a restart
	// mid-terminalization resumes instead of colliding with its own earlier rows, and a partially executed
	// job never has a real outcome overwritten by "not attempted".
	stagedPages := s.getQueryBuilder().
		Select("Ordinal", "ExternalId", "Title", "PlannedAction").
		From("DOCS_ImportStagedPage").
		Where(`NOT EXISTS (
			SELECT 1 FROM DOCS_ImportResult x
			WHERE x.JobId = DOCS_ImportStagedPage.JobId AND x.Stage = 'execution'
			  AND x.Ordinal = DOCS_ImportStagedPage.Ordinal
		)`)
	counts.pages, err = s.recordOutcomesFrom(tx, job, stagedPages, outcome, details, detailsBytes, now)
	if err != nil {
		return counts, err
	}

	// Then anything preflight classified that has no staged page of its own. The anti-join is what makes
	// this safe to run unconditionally: a page that produced both rows is counted once, above.
	preflightOnly := s.getQueryBuilder().
		Select("r.Ordinal", "r.ExternalId", "r.Title", "r.PlannedAction").
		From("DOCS_ImportResult r").
		Where(sq.Eq{"r.Stage": string(model.ImportStagePreflight)}).
		Where(`NOT EXISTS (
			SELECT 1 FROM DOCS_ImportStagedPage p WHERE p.JobId = r.JobId AND p.Ordinal = r.Ordinal
		)`).
		Where(`NOT EXISTS (
			SELECT 1 FROM DOCS_ImportResult x
			WHERE x.JobId = r.JobId AND x.Stage = 'execution' AND x.Ordinal = r.Ordinal
		)`)
	counts.stale, err = s.recordOutcomesFrom(tx, job, preflightOnly, outcome, details, detailsBytes, now)
	return counts, err
}

// recordOutcomesFrom streams entity identities from one source query and writes a not-attempted execution
// result for each. source must select (Ordinal, ExternalId, Title, PlannedAction) and is filtered by job
// and paged on Ordinal here, so neither the read nor the write ever materializes the whole entity set.
func (s *Store) recordOutcomesFrom(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	source sq.SelectBuilder,
	outcome model.ImportOutcome,
	details mmmodel.StringInterface,
	detailsBytes int,
	now int64,
) (int, error) {
	written := 0
	lastOrdinal := -1
	for {
		var entities []importEntityIdentity
		builder := source.Where(sq.Eq{"JobId": job.Id}).Where(sq.Gt{"Ordinal": lastOrdinal}).OrderBy("Ordinal ASC")
		builder = applyLimitOffset(builder, 0, importRowBatchRows)
		if err := s.selectBuilder(tx, &entities, builder); err != nil {
			return written, errors.Wrap(err, "unable_to_list_import_entity_identities")
		}
		if len(entities) == 0 {
			return written, nil
		}

		batch := s.getQueryBuilder().Insert("DOCS_ImportResult").Columns(importResultColumns...)
		for _, e := range entities {
			record := &model.ImportResultRecord{
				JobId:         job.Id,
				Stage:         model.ImportStageExecution,
				Ordinal:       e.Ordinal,
				EntityType:    model.ImportEntityTypePage,
				ExternalId:    e.ExternalId,
				Title:         e.Title,
				PlannedAction: model.ImportAction(e.PlannedAction),
				ActualAction:  model.ImportActionNotAttempted,
				Outcome:       outcome,
				Details:       details,
				CreateAt:      now,
				UpdateAt:      now,
			}
			if validErr := record.IsValid(); validErr != nil {
				return written, &ErrInvalidInput{Entity: "ImportResult", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
			}
			batch = batch.Values(
				record.JobId, string(record.Stage), record.Ordinal, record.EntityType,
				record.ExternalId, record.LocalId, record.Title,
				string(record.PlannedAction), string(record.ActualAction), string(record.Outcome),
				jsonbMap(record.Details), record.CreateAt, record.UpdateAt,
			)
			job.RetainedBytes += retainedResultRowBytes(record, detailsBytes)
			lastOrdinal = e.Ordinal
			written++
		}
		if _, err := s.execBuilder(tx, batch); err != nil {
			if isUniqueViolation(err) {
				return written, &ErrConflict{Resource: "ImportResult job_id=" + job.Id}
			}
			return written, errors.Wrap(err, "unable_to_save_import_results")
		}
	}
}

// finalizeRetainedReservation replaces a job's retained reservation with what it actually retained and
// moves the difference on the shared capacity row. Once a job is terminal nothing more will be written
// for it, so continuing to hold a reservation sized for a full execution would keep the per-user and
// global retained budgets occupied for the whole retention window — the exact lockout admission exists
// to prevent, arriving by a slower route.
//
// The delta is signed on purpose: if measured usage somehow exceeded the reservation, the accounting
// grows to match rather than quietly under-reporting what the database is holding. Must be called inside
// tx, and after every retained row for the job has been written and charged.
func (s *Store) finalizeRetainedReservation(tx sqlx.ExtContext, job *model.ImportJob, now int64) error {
	delta := job.RetainedReservedBytes - job.RetainedBytes
	if delta == 0 {
		return nil
	}
	capacityBuilder := s.getQueryBuilder().
		Update("DOCS_ImportCapacity").
		Set("ReservedRetainedBytes", sq.Expr("GREATEST(ReservedRetainedBytes - ?, 0)", delta)).
		Set("UpdateAt", now).
		Where(sq.Eq{"Id": 1})
	if _, err := s.execBuilder(tx, capacityBuilder); err != nil {
		return errors.Wrap(err, "unable_to_release_import_retained_capacity")
	}
	jobBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("RetainedBytes", job.RetainedBytes).
		Set("RetainedReservedBytes", job.RetainedBytes).
		Where(sq.Eq{"Id": job.Id})
	if _, err := s.execBuilder(tx, jobBuilder); err != nil {
		return errors.Wrap(err, "unable_to_finalize_import_retained_bytes")
	}
	job.RetainedReservedBytes = job.RetainedBytes
	return nil
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
	ExpiredJobs      int
	PurgedStagedJobs int
	DeletedJobs      int
	// KeptForCompensationJobs counts jobs past retention that were deliberately not deleted because a
	// channel attempt still awaits compensation. Reported rather than skipped silently: a job that never
	// leaves this count means compensation is stuck and an orphaned channel is waiting on it.
	KeptForCompensationJobs int
	// ReleasedStagedBytes and ReleasedRetainedBytes are reported separately because they come back on
	// different schedules: staged bytes are freed as soon as a job stops needing its input, while
	// retained bytes only shrink to measured usage when the job goes terminal.
	ReleasedStagedBytes   int64
	ReleasedRetainedBytes int64
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
func (s *Store) ExpireStalledImportJobs(now int64, errorCode string) (int, int64, int64, error) {
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"State": cancelableImportStates}).
		Where(sq.LtOrEq{"RetainUntil": now}).
		OrderBy("RetainUntil ASC", "Id ASC")
	builder = applyLimitOffset(builder, 0, importCleanupBatch)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return 0, 0, 0, errors.Wrap(err, "unable_to_list_expired_import_jobs")
	}

	expired := 0
	var releasedStaged, releasedRetained int64
	for _, job := range jobs {
		// Cancel each job in its own transaction so one contended row cannot stall the whole sweep.
		// An actorID of "" means "system", bypassing the ownership check.
		canceled, err := s.CancelImportJob(job.Id, "", errorCode)
		if err != nil {
			if IsErrConflict(err) || IsErrNotFound(err) {
				// The job advanced or vanished between the scan and the cancel; leave it to the next run.
				continue
			}
			return expired, releasedStaged, releasedRetained, err
		}
		expired++
		releasedStaged += job.StagedBytes
		releasedRetained += job.RetainedReservedBytes - canceled.RetainedReservedBytes
	}
	return expired, releasedStaged, releasedRetained, nil
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
//
// It returns how many jobs it deleted and how many it deliberately left in place. A job holding a channel
// attempt in pending_compensation is skipped: that row is the record of a Mattermost channel this import
// created and must still clean up, and DOCS_ImportChannelAttempt cascades on job delete, so removing the
// job would destroy the only pointer to an orphaned channel. Such a job stays until compensation
// resolves the attempt, which is also why the second return value is reported rather than swallowed.
func (s *Store) DeleteExpiredImportJobs(now int64) (int, int, error) {
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
		return 0, 0, errors.Wrap(err, "unable_to_list_deletable_import_jobs")
	}

	deleted, skipped := 0, 0
	for _, job := range jobs {
		removed, err := s.deleteOneImportJob(job, now)
		if err != nil {
			return deleted, skipped, err
		}
		if removed {
			deleted++
		} else {
			skipped++
		}
	}
	return deleted, skipped, nil
}

// hasPendingCompensation reports whether a job still owns a channel attempt awaiting compensation. Must
// be called inside tx, with the job row already locked, so the answer cannot change under the delete.
func (s *Store) hasPendingCompensation(tx sqlx.ExtContext, jobID string) (bool, error) {
	var pending int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportChannelAttempt").
		Where(sq.Eq{"JobId": jobID, "State": string(model.ImportChannelPendingCompensation)})
	if err := s.getBuilder(tx, &pending, builder); err != nil {
		return false, errors.Wrap(err, "unable_to_count_import_channel_attempts")
	}
	return pending > 0, nil
}

// deleteOneImportJob deletes one job and releases both of its reservations atomically. It reports false
// when the job was deliberately kept because compensation still needs its channel attempt.
func (s *Store) deleteOneImportJob(job *model.ImportJob, now int64) (_ bool, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return false, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	var locked model.ImportJob
	lockBuilder := s.importJobSelectQuery().Where(sq.Eq{"Id": job.Id}).Suffix("FOR UPDATE")
	if err = s.getBuilder(tx, &locked, lockBuilder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, errors.Wrap(err, "unable_to_get_import_job")
	}

	pending, err := s.hasPendingCompensation(tx, locked.Id)
	if err != nil {
		return false, err
	}
	if pending {
		// Keep the job (and therefore its attempt row) until compensation finishes. Its reservation stays
		// held too, which is correct: the rows are still there.
		return false, tx.Commit()
	}

	capacityBuilder := s.getQueryBuilder().
		Update("DOCS_ImportCapacity").
		Set("ReservedStagedBytes", sq.Expr("GREATEST(ReservedStagedBytes - ?, 0)", locked.StagedBytes)).
		Set("ReservedRetainedBytes", sq.Expr("GREATEST(ReservedRetainedBytes - ?, 0)", locked.RetainedReservedBytes)).
		Set("UpdateAt", now).
		Where(sq.Eq{"Id": 1})
	if _, err = s.execBuilder(tx, capacityBuilder); err != nil {
		return false, errors.Wrap(err, "unable_to_release_import_capacity")
	}

	deleteBuilder := s.getQueryBuilder().Delete("DOCS_ImportJob").Where(sq.Eq{"Id": locked.Id})
	if _, err = s.execBuilder(tx, deleteBuilder); err != nil {
		return false, errors.Wrap(err, "unable_to_delete_import_job")
	}
	return true, tx.Commit()
}
