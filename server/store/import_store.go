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
	"SourceSelectionMode", "SelectedImportSourceId", "SelectedSourceDisplayName",
	"State", "Phase", "ProgressCurrent", "ProgressTotal",
	"BundleSha256", "BundleSummary", "PreflightSummary", "PreflightRevision", "Confirmation", "FinalSummary",
	"ErrorCode", "ErrorMessage", "CancelRequestedAt",
	"ClaimToken", "ClaimedBy", "LeaseExpiresAt", "HeartbeatAt",
	"CreateAt", "UpdateAt", "ConfirmedAt", "StartedAt", "FinishedAt", "RetainUntil",
}

// importStagedPageColumns are the DOCS_ImportStagedPage columns, in insert order.
var importStagedPageColumns = []string{
	"JobId", "Ordinal", "ExternalId", "ParentExternalId", "SourceOrdinal",
	"Title", "CanonicalBody", "SearchText", "SourceUserProposal", "SourceAuthorAccountId",
	"SourceCreateAt", "SourceUpdateAt", "SourceProps",
	"IncomingSourceHash", "PreflightCurrentHash", "PreflightMappingHash", "PreflightMappingUpdateAt",
	"PlannedAction", "PlannedPageId", "ResolvedUserId", "AuthorFallbackReason",
}

// importIssueColumns are the DOCS_ImportIssue columns, in insert order.
var importIssueColumns = []string{
	"JobId", "Stage", "Ordinal", "Severity", "Code",
	"EntityType", "ExternalId", "LocalId", "Title", "Message", "Remediation", "Details",
}

// importSourceColumns are the DOCS_ImportSource columns. OrganizationId is nullable in the schema
// (two Confluence instances may legitimately have no organization id), so reads coalesce it to ”
// rather than scanning NULL into model.ImportSource's string field.
var importSourceColumns = []string{
	"Id", "SpaceId", "SourceType", "DisplayName",
	"COALESCE(OrganizationId, '') AS OrganizationId",
	"ExternalSpaceKey", "ExternalSpaceName", "CreatedBy",
	"CreateAt", "UpdateAt", "LastImportAt", "LastSuccessfulJobId", "ActiveJobId", "Props",
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

// importIssueBatchRows bounds one multi-row issue insert. Issue rows are small, so only the bind
// parameter limit matters.
const importIssueBatchRows = 200

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

// CreateImportJobWithStaging inserts an import job together with all of its normalized staged pages
// and inspection issues in one transaction, so a job never becomes visible without the staged input
// the worker needs. This is the single write performed by the synchronous upload/inspection request:
// if it fails, no resumable job is promised and the user uploads again.
func (s *Store) CreateImportJobWithStaging(job *model.ImportJob, pages []*model.ImportStagedPage, issues []*model.ImportIssueRecord) (_ *model.ImportJob, err error) {
	if job == nil {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "job", Value: nil}
	}
	if validErr := job.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	for _, issue := range issues {
		if issue == nil {
			return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "issue", Value: nil}
		}
		if validErr := issue.IsValid(); validErr != nil {
			return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
		}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	jobBuilder := s.getQueryBuilder().
		Insert("DOCS_ImportJob").
		Columns(importJobColumns...).
		Values(
			job.Id, job.ActorId, job.TeamId,
			string(job.TargetKind), job.TargetSpaceId, job.TargetSpaceExisted, job.ConfirmedSpaceTitle, job.ConfirmedSpaceDescription, job.ProvisionedChannelId,
			string(job.SourceSelectionMode), job.SelectedImportSourceId, job.SelectedSourceDisplayName,
			string(job.State), string(job.Phase), job.ProgressCurrent, job.ProgressTotal,
			job.BundleSha256, jsonbMap(job.BundleSummary), jsonbMap(job.PreflightSummary), job.PreflightRevision, job.Confirmation, jsonbMap(job.FinalSummary),
			job.ErrorCode, job.ErrorMessage, job.CancelRequestedAt,
			job.ClaimToken, job.ClaimedBy, job.LeaseExpiresAt, job.HeartbeatAt,
			job.CreateAt, job.UpdateAt, job.ConfirmedAt, job.StartedAt, job.FinishedAt, job.RetainUntil,
		)
	if _, execErr := s.execBuilder(tx, jobBuilder); execErr != nil {
		if isUniqueViolation(execErr) {
			return nil, &ErrConflict{Resource: "ImportJob id=" + job.Id}
		}
		return nil, errors.Wrap(execErr, "unable_to_save_import_job")
	}

	if stageErr := s.insertStagedPages(tx, job.Id, pages); stageErr != nil {
		return nil, stageErr
	}
	if issueErr := s.insertImportIssues(tx, issues); issueErr != nil {
		return nil, issueErr
	}

	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return job, nil
}

// insertStagedPages writes the staged pages in size-bounded batches (see the batch constants).
// Must be called inside tx.
func (s *Store) insertStagedPages(tx sqlx.ExtContext, jobID string, pages []*model.ImportStagedPage) error {
	batch := s.getQueryBuilder().Insert("DOCS_ImportStagedPage").Columns(importStagedPageColumns...)
	rows, bytesInBatch := 0, 0

	flush := func() error {
		if rows == 0 {
			return nil
		}
		if _, err := s.execBuilder(tx, batch); err != nil {
			if isUniqueViolation(err) {
				return &ErrConflict{Resource: "ImportStagedPage job_id=" + jobID}
			}
			return errors.Wrap(err, "unable_to_save_import_staged_pages")
		}
		batch = s.getQueryBuilder().Insert("DOCS_ImportStagedPage").Columns(importStagedPageColumns...)
		rows, bytesInBatch = 0, 0
		return nil
	}

	for _, p := range pages {
		if p == nil {
			return &ErrInvalidInput{Entity: "ImportStagedPage", Field: "page", Value: nil}
		}
		batch = batch.Values(
			jobID, p.Ordinal, p.ExternalId, p.ParentExternalId, p.SourceOrdinal,
			p.Title, p.CanonicalBody, p.SearchText, p.SourceUserProposal, p.SourceAuthorAccountId,
			p.SourceCreateAt, p.SourceUpdateAt, jsonbMap(p.SourceProps),
			p.IncomingSourceHash, p.PreflightCurrentHash, p.PreflightMappingHash, p.PreflightMappingUpdateAt,
			string(p.PlannedAction), p.PlannedPageId, p.ResolvedUserId, p.AuthorFallbackReason,
		)
		rows++
		bytesInBatch += len(p.CanonicalBody) + len(p.SearchText)
		if rows >= importStagedPageBatchRows || bytesInBatch >= importStagedPageBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// insertImportIssues writes issue rows in bind-parameter-bounded batches. Must be called inside tx.
func (s *Store) insertImportIssues(tx sqlx.ExtContext, issues []*model.ImportIssueRecord) error {
	batch := s.getQueryBuilder().Insert("DOCS_ImportIssue").Columns(importIssueColumns...)
	rows := 0

	flush := func() error {
		if rows == 0 {
			return nil
		}
		if _, err := s.execBuilder(tx, batch); err != nil {
			if isUniqueViolation(err) {
				return &ErrConflict{Resource: "ImportIssue duplicate (job_id, stage, ordinal)"}
			}
			return errors.Wrap(err, "unable_to_save_import_issues")
		}
		batch = s.getQueryBuilder().Insert("DOCS_ImportIssue").Columns(importIssueColumns...)
		rows = 0
		return nil
	}

	for _, i := range issues {
		batch = batch.Values(
			i.JobId, i.Stage, i.Ordinal, i.Severity, i.Code,
			i.EntityType, i.ExternalId, i.LocalId, i.Title, i.Message, i.Remediation, jsonbMap(i.Details),
		)
		rows++
		if rows >= importIssueBatchRows {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
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
// result to that team. limit must be > 0.
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
// are optional filters. limit must be > 0.
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

// CountImportStagedPages returns how many staged pages a job has. Used to report the staged total
// without loading any bodies.
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
