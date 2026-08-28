// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

	"github.com/jmoiron/sqlx"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// importChannelAttemptColumns are the DOCS_ImportChannelAttempt columns, in insert order.
var importChannelAttemptColumns = []string{
	"JobId", "AttemptId", "ChannelName", "ChannelId", "State", "ErrorCode", "CreateAt", "UpdateAt",
}

// --- execution entry ---

// BeginImportExecution moves a confirmed job into importing, or adopts one that is already there.
//
// The mapping-revision recheck happens only on the queued_import -> importing transition. Once a job is
// importing it is itself changing this source's mappings, so recomparing would make the job invalidate its
// own preflight halfway through; from that point the immutable execution checkpoints, not the revision, are
// what keep a restart deterministic.
//
// Adopting an already-importing job is what makes restart recovery a plain re-entry rather than a special
// path: the worker calls this on every pass and the job resumes from its committed checkpoints.
func (s *Store) BeginImportExecution(jobID string) (_ *model.ImportJob, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return nil, err
	}
	if job.TerminalIntent != model.ImportIntentNone {
		// A cancellation or failure has already been recorded, so terminalization owns this job now.
		return nil, &ErrConflict{Resource: "ImportJob terminal_intent=" + string(job.TerminalIntent)}
	}

	staged, err := s.countImportStagedPages(tx, jobID)
	if err != nil {
		return nil, err
	}
	applied, err := s.countImportExecutionResults(tx, jobID)
	if err != nil {
		return nil, err
	}

	if job.State == model.ImportStateImporting {
		// Resume: progress is reconstructed from committed checkpoints rather than remembered, so a restart
		// never double-counts and never reports work it has lost track of.
		if err = s.setImportExecutionProgress(tx, job, applied, staged); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "commit_transaction")
		}
		return job, nil
	}
	if job.State != model.ImportStateQueuedImport {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}

	current, err := s.readImportSourceRevision(tx, job, true)
	if err != nil {
		return nil, err
	}
	if current != job.PreflightMappingRevision {
		// Another job changed these mappings between confirmation and now. The plan the user approved no
		// longer describes the source, so it is discarded and recomputed rather than applied.
		if err = s.resetImportToPreflight(tx, job, mmmodel.GetMillis()); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "commit_transaction")
		}
		return nil, &ErrPreflightStale{JobID: jobID}
	}

	startedAt := mmmodel.GetMillis()
	phase := model.ImportPhaseWritingPages
	if !job.TargetSpaceExisted {
		phase = model.ImportPhaseProvisioning
	}
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateImporting)).
		Set("Phase", string(phase)).
		Set("StartedAt", startedAt).
		Set("ProgressCurrent", applied).
		Set("ProgressTotal", staged).
		Set("UpdateAt", monotonicBump("UpdateAt", startedAt)).
		Where(sq.Eq{"Id": jobID, "State": string(model.ImportStateQueuedImport)})
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return nil, errors.Wrap(err, "unable_to_begin_import_execution")
	}
	if err = checkRowsAffected(result, "ImportJob", jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	job.State = model.ImportStateImporting
	job.Phase = phase
	job.StartedAt = startedAt
	job.ProgressCurrent = applied
	job.ProgressTotal = staged
	job.UpdateAt = max(job.UpdateAt+1, startedAt)
	return job, nil
}

// setImportExecutionProgress writes the reconstructed progress counters. Must be called inside tx.
func (s *Store) setImportExecutionProgress(tx sqlx.ExtContext, job *model.ImportJob, current, total int64) error {
	if job.ProgressCurrent == current && job.ProgressTotal == total {
		return nil
	}
	at := mmmodel.GetMillis()
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("ProgressCurrent", current).
		Set("ProgressTotal", total).
		Set("UpdateAt", monotonicBump("UpdateAt", at)).
		Where(sq.Eq{"Id": job.Id})
	if _, err := s.execBuilder(tx, builder); err != nil {
		return errors.Wrap(err, "unable_to_set_import_progress")
	}
	job.ProgressCurrent = current
	job.ProgressTotal = total
	job.UpdateAt = max(job.UpdateAt+1, at)
	return nil
}

// SetImportExecutionProgress publishes the committed-checkpoint count as the job's progress.
//
// Progress is *set* from the count of durable execution results rather than incremented, so a replayed page
// cannot advance it twice and a restart cannot lose it. It is deliberately written outside the page
// transactions: a progress write is a cosmetic update, and folding it into the page transaction would
// lengthen the one transaction that holds the target Space lock.
func (s *Store) SetImportExecutionProgress(jobID string) (int64, error) {
	applied, err := s.countImportExecutionResults(s.db, jobID)
	if err != nil {
		return 0, err
	}
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("ProgressCurrent", applied).
		Set("UpdateAt", monotonicBump("UpdateAt", mmmodel.GetMillis())).
		Where(sq.Eq{"Id": jobID, "State": string(model.ImportStateImporting)})
	if _, err := s.execBuilder(s.db, builder); err != nil {
		return applied, errors.Wrap(err, "unable_to_set_import_progress")
	}
	return applied, nil
}

// countImportStagedPages counts a job's staged pages. Must be called with a live handle or inside tx.
func (s *Store) countImportStagedPages(e sqlx.ExtContext, jobID string) (int64, error) {
	var count int64
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": jobID})
	if err := s.getBuilder(e, &count, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_count_import_staged_pages")
	}
	return count, nil
}

// countImportExecutionResults counts a job's committed execution checkpoints.
func (s *Store) countImportExecutionResults(e sqlx.ExtContext, jobID string) (int64, error) {
	var count int64
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": jobID, "Stage": string(model.ImportStageExecution)})
	if err := s.getBuilder(e, &count, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_count_import_execution_results")
	}
	return count, nil
}

// GetImportStagedOrdinals returns the next batch of a job's staged page ordinals above afterOrdinal.
//
// Execution walks ordinals rather than whole staged rows because each page transaction re-reads its own row
// under the job lock: reading bodies here would hold a batch of them resident for no benefit, and a row read
// outside the lock could be stale by the time it was applied.
func (s *Store) GetImportStagedOrdinals(jobID string, afterOrdinal, limit int) ([]int, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportStagedPage", Field: "jobID", Value: jobID}
	}
	if err := requirePositiveLimit("ImportStagedPage", limit); err != nil {
		return nil, err
	}
	builder := s.getQueryBuilder().
		Select("Ordinal").
		From("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": jobID}).
		Where(sq.Gt{"Ordinal": afterOrdinal}).
		OrderBy("Ordinal ASC")
	builder = applyLimitOffset(builder, 0, limit)

	ordinals := []int{}
	if err := s.selectBuilder(s.db, &ordinals, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_staged_ordinals")
	}
	return ordinals, nil
}

// ImportStagedPageSummary is the identity and author fields the execution loop needs before it calls
// ApplyImportedPage. It deliberately excludes the body, SearchText, and props: those are read inside the page
// transaction, where they are guaranteed current, rather than carried across an unlocked gap.
type ImportStagedPageSummary struct {
	Ordinal               int
	ExternalId            string
	SourceUserProposal    string
	SourceAuthorAccountId string
	ResolvedUserId        string
	AuthorFallbackReason  string
	PlannedAction         model.ImportAction
}

// GetImportStagedPageSummary reads one staged page's identity and reviewed author.
func (s *Store) GetImportStagedPageSummary(jobID string, ordinal int) (*ImportStagedPageSummary, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportStagedPage", Field: "jobID", Value: jobID}
	}
	var summary ImportStagedPageSummary
	builder := s.getQueryBuilder().
		Select("Ordinal", "ExternalId", "SourceUserProposal", "SourceAuthorAccountId",
			"ResolvedUserId", "AuthorFallbackReason", "PlannedAction").
		From("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": jobID, "Ordinal": ordinal})
	if err := s.getBuilder(s.db, &summary, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ImportStagedPage", ID: jobID}
		}
		return nil, errors.Wrap(err, "unable_to_get_import_staged_page_summary")
	}
	return &summary, nil
}

// --- crash-aware channel provisioning ---

// BeginImportChannelAttempt records a durable identity for one external channel-create attempt and commits
// it before the call is made.
//
// The row exists so a channel created by a call whose result was lost can still be found and compensated.
// The name is random rather than derived from the job id for the same reason: a deterministic name would
// make every retry after a lost result collide with the orphan forever on (TeamId, Name), permanently
// wedging the job instead of letting it try again.
func (s *Store) BeginImportChannelAttempt(jobID, attemptID, channelName string) (err error) {
	if !mmmodel.IsValidId(attemptID) || channelName == "" {
		return &ErrInvalidInput{Entity: "ImportChannelAttempt", Field: "attemptId", Value: attemptID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return err
	}
	if job.State != model.ImportStateImporting || job.TerminalIntent != model.ImportIntentNone {
		return &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if job.ProvisionedChannelId != "" {
		// A channel is already selected; starting another creation attempt would knowingly orphan one.
		return &ErrConflict{Resource: "ImportJob provisioned_channel"}
	}

	at := mmmodel.GetMillis()
	attempt := &model.ImportChannelAttempt{
		JobId:       jobID,
		AttemptId:   attemptID,
		ChannelName: channelName,
		State:       model.ImportChannelCreating,
		CreateAt:    at,
		UpdateAt:    at,
	}
	insertBuilder := s.getQueryBuilder().
		Insert("DOCS_ImportChannelAttempt").
		Columns(importChannelAttemptColumns...).
		Values(attempt.JobId, attempt.AttemptId, attempt.ChannelName, attempt.ChannelId,
			string(attempt.State), attempt.ErrorCode, attempt.CreateAt, attempt.UpdateAt)
	if _, err = s.execBuilder(tx, insertBuilder); err != nil {
		if isUniqueViolation(err) {
			return &ErrConflict{Resource: "ImportChannelAttempt attempt_id=" + attemptID}
		}
		return errors.Wrap(err, "unable_to_save_import_channel_attempt")
	}
	return tx.Commit()
}

// RecordImportChannelAttemptID persists the channel id a create call returned and reports whether it became
// the job's selected channel.
//
// Persisting the id is the first thing done with it, before membership or any Space row, because until it is
// recorded the channel exists and nothing knows about it. When a different id is already selected — which
// means an earlier attempt's result arrived after all — the extra channel is marked for compensation rather
// than discarded silently.
func (s *Store) RecordImportChannelAttemptID(jobID, attemptID, channelID string) (_ bool, err error) {
	if !mmmodel.IsValidId(channelID) {
		return false, &ErrInvalidInput{Entity: "ImportChannelAttempt", Field: "channelId", Value: channelID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return false, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return false, err
	}

	selected := job.ProvisionedChannelId == "" || job.ProvisionedChannelId == channelID
	state := model.ImportChannelProvisioned
	if !selected {
		state = model.ImportChannelPendingCompensation
	}
	at := mmmodel.GetMillis()
	attemptBuilder := s.getQueryBuilder().
		Update("DOCS_ImportChannelAttempt").
		Set("ChannelId", channelID).
		Set("State", string(state)).
		Set("UpdateAt", at).
		Where(sq.Eq{"JobId": jobID, "AttemptId": attemptID})
	result, err := s.execBuilder(tx, attemptBuilder)
	if err != nil {
		return false, errors.Wrap(err, "unable_to_record_import_channel_attempt")
	}
	if err = checkRowsAffected(result, "ImportChannelAttempt", attemptID); err != nil {
		return false, err
	}

	if selected && job.ProvisionedChannelId == "" {
		jobBuilder := s.getQueryBuilder().
			Update("DOCS_ImportJob").
			Set("ProvisionedChannelId", channelID).
			Set("UpdateAt", monotonicBump("UpdateAt", at)).
			Where(sq.Eq{"Id": jobID, "ProvisionedChannelId": ""})
		if _, err = s.execBuilder(tx, jobBuilder); err != nil {
			return false, errors.Wrap(err, "unable_to_select_import_provisioned_channel")
		}
		job.ProvisionedChannelId = channelID
	}
	if err = tx.Commit(); err != nil {
		return false, errors.Wrap(err, "commit_transaction")
	}
	return selected, nil
}

// SetImportChannelAttemptState records an attempt's new state, for example after a failed create or a
// completed compensation.
func (s *Store) SetImportChannelAttemptState(
	jobID, attemptID string,
	state model.ImportChannelAttemptState,
	errorCode string,
) error {
	if !state.IsValid() {
		return &ErrInvalidInput{Entity: "ImportChannelAttempt", Field: "state", Value: string(state)}
	}
	builder := s.getQueryBuilder().
		Update("DOCS_ImportChannelAttempt").
		Set("State", string(state)).
		Set("ErrorCode", errorCode).
		Set("UpdateAt", mmmodel.GetMillis()).
		Where(sq.Eq{"JobId": jobID, "AttemptId": attemptID})
	result, err := s.execBuilder(s.db, builder)
	if err != nil {
		return errors.Wrap(err, "unable_to_set_import_channel_attempt_state")
	}
	return checkRowsAffected(result, "ImportChannelAttempt", attemptID)
}

// GetImportChannelAttempts returns a job's channel attempts in creation order.
func (s *Store) GetImportChannelAttempts(jobID string) ([]*model.ImportChannelAttempt, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportChannelAttempt", Field: "jobID", Value: jobID}
	}
	builder := s.getQueryBuilder().
		Select(importChannelAttemptColumns...).
		From("DOCS_ImportChannelAttempt").
		Where(sq.Eq{"JobId": jobID}).
		OrderBy("CreateAt ASC", "AttemptId ASC")

	attempts := []*model.ImportChannelAttempt{}
	if err := s.selectBuilder(s.db, &attempts, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_channel_attempts")
	}
	return attempts, nil
}

// ImportSpaceAttachment is the metadata a new target Space is created with, taken from the confirmation
// rather than from the bundle: the user reviewed and edited these values before approving.
type ImportSpaceAttachment struct {
	JobID       string
	AttemptID   string
	ChannelID   string
	Title       string
	Description string
	DisplayName string
}

// AttachImportSpace creates the target Space row and the job's ImportSource together, in one transaction
// with the job-state guard.
//
// Doing all three at once is what makes the Space row the point of no return: before it commits, a failure
// can still compensate the backing channel, and after it commits the import owns real user-visible content
// that must be preserved and reported instead. Every insert tolerates already existing so a restart between
// the external channel call and this transaction resumes rather than conflicting.
func (s *Store) AttachImportSpace(in ImportSpaceAttachment) (_ *model.ImportJob, err error) {
	if !mmmodel.IsValidId(in.ChannelID) {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "channelId", Value: in.ChannelID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, in.JobID, "")
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStateImporting || job.TerminalIntent != model.ImportIntentNone {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}

	space := &model.Space{
		Id:        job.TargetSpaceId,
		ChannelId: in.ChannelID,
		TeamId:    job.TeamId,
		CreatorId: job.ActorId,
		Title:     in.Title,
		// The confirmed description; Space.PreSave sanitizes it, as it does on the interactive path.
		Description: in.Description,
	}
	space.PreSave()
	if validErr := space.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "Space", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	spaceBuilder := s.getQueryBuilder().
		Insert("DOCS_Space").
		Columns(spaceSelectColumns...).
		Values(space.Id, space.ChannelId, space.TeamId, space.CreatorId, space.Title, space.Description,
			space.Icon, space.GetProps(), space.CreateAt, space.UpdateAt, space.DeleteAt, space.SortOrder).
		Suffix("ON CONFLICT (Id) DO NOTHING")
	if _, err = s.execBuilder(tx, spaceBuilder); err != nil {
		if isUniqueViolation(err) {
			// A different Space already backs this channel, which no retry can resolve.
			return nil, &ErrConflict{Resource: "Space channel_id=" + in.ChannelID}
		}
		return nil, errors.Wrap(err, "unable_to_save_import_space")
	}

	if in.AttemptID != "" {
		attemptBuilder := s.getQueryBuilder().
			Update("DOCS_ImportChannelAttempt").
			Set("State", string(model.ImportChannelAttached)).
			Set("UpdateAt", mmmodel.GetMillis()).
			Where(sq.Eq{"JobId": in.JobID, "AttemptId": in.AttemptID})
		if _, err = s.execBuilder(tx, attemptBuilder); err != nil {
			return nil, errors.Wrap(err, "unable_to_attach_import_channel_attempt")
		}
	}

	if err = s.ensureImportSource(tx, job, in.DisplayName); err != nil {
		return nil, err
	}
	if err = s.setImportPhase(tx, job, model.ImportPhaseWritingPages); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return job, nil
}

// EnsureImportSourceForTarget creates the job's ImportSource inside an existing target Space, or adopts the
// one already there, and advances the job to writing pages.
//
// An id already attached to a *different* Space is refused rather than reused: mappings are scoped by source
// id, so adopting one across Spaces would let this job write into another Space's page history.
func (s *Store) EnsureImportSourceForTarget(jobID, displayName string) (_ *model.ImportJob, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStateImporting || job.TerminalIntent != model.ImportIntentNone {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if _, err = s.lockLiveSpaceChannel(tx, job.TargetSpaceId); err != nil {
		return nil, err
	}
	if err = s.ensureImportSource(tx, job, displayName); err != nil {
		return nil, err
	}
	if err = s.setImportPhase(tx, job, model.ImportPhaseWritingPages); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return job, nil
}

// ensureImportSource inserts the job's selected ImportSource if it does not exist yet, and verifies an
// existing row belongs to this job's target Space. Must be called inside tx.
func (s *Store) ensureImportSource(tx sqlx.ExtContext, job *model.ImportJob, displayName string) error {
	if !mmmodel.IsValidId(job.SelectedImportSourceId) {
		return &ErrInvalidInput{Entity: "ImportSource", Field: "id", Value: job.SelectedImportSourceId}
	}

	var existing model.ImportSource
	readBuilder := s.getQueryBuilder().
		Select(importSourceColumns...).
		From("DOCS_ImportSource").
		Where(sq.Eq{"Id": job.SelectedImportSourceId}).
		Suffix("FOR UPDATE")
	err := s.getBuilder(tx, &existing, readBuilder)
	switch {
	case err == nil:
		if existing.SpaceId != job.TargetSpaceId {
			return &ErrConflict{Resource: "ImportSource space_id=" + existing.SpaceId}
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return errors.Wrap(err, "unable_to_read_import_source")
	}

	if displayName == "" {
		displayName = job.SelectedSourceDisplayName
	}
	if displayName == "" {
		displayName = job.BundleSummary.Source.SpaceName
	}
	at := mmmodel.GetMillis()
	source := &model.ImportSource{
		Id:                job.SelectedImportSourceId,
		SpaceId:           job.TargetSpaceId,
		SourceType:        model.ImportSourceTypeConfluence,
		DisplayName:       displayName,
		OrganizationId:    job.BundleSummary.Source.OrganizationId,
		ExternalSpaceKey:  job.BundleSummary.Source.SpaceKey,
		ExternalSpaceName: job.BundleSummary.Source.SpaceName,
		CreatedBy:         job.ActorId,
		CreateAt:          at,
		UpdateAt:          at,
		// MappingRevision starts at zero and is incremented once, by terminalization, if this job commits a
		// mapping-affecting decision.
		MappingRevision: 0,
		Props:           mmmodel.StringInterface{},
	}
	if validErr := source.IsValid(); validErr != nil {
		return &ErrInvalidInput{Entity: "ImportSource", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	insertBuilder := s.getQueryBuilder().
		Insert("DOCS_ImportSource").
		Columns(importSourceColumns...).
		Values(source.Id, source.SpaceId, source.SourceType, source.DisplayName, source.OrganizationId,
			source.ExternalSpaceKey, source.ExternalSpaceName, source.CreatedBy,
			source.CreateAt, source.UpdateAt, source.LastImportAt, source.LastSuccessfulJobId,
			source.MappingRevision, jsonbMap(source.Props)).
		Suffix("ON CONFLICT (Id) DO NOTHING")
	if _, err = s.execBuilder(tx, insertBuilder); err != nil {
		return errors.Wrap(err, "unable_to_save_import_source")
	}
	return nil
}

// --- per-page execution ---

// ImportPageExecution is one page's execution request: the ordinal identifying the staged page, plus the
// author the app revalidated immediately beforehand. Everything else is read under this call's own locks.
type ImportPageExecution struct {
	JobID   string
	Ordinal int
	// ResolvedUserID and AuthorFallbackReason are the author as revalidated just before this call. A create
	// attributes the page to them; an update never changes existing ownership.
	ResolvedUserID       string
	AuthorFallbackReason string
	// AuthorProposal is the effective username proposal the source content hash was computed from. It is
	// recorded in the page's props so the applied-content hash mirrors the same identity the source hash did.
	AuthorProposal string
	// Now is the monotonic execution timestamp written to local UpdateAt/EditAt.
	Now int64
	// ApprovedOverwrite reports that this page's external id is in the job's persisted confirmation.
	ApprovedOverwrite bool
}

// ImportPageOutcome is what one page transaction decided, for the caller's counters and log line.
type ImportPageOutcome struct {
	Ordinal       int
	ExternalID    string
	LocalID       string
	Title         string
	PlannedAction model.ImportAction
	ActualAction  model.ImportAction
	Outcome       model.ImportOutcome
	// TreeChanged is true when this transaction created or modified a page, which is what obliges the job to
	// publish a tree invalidation even if it later fails.
	TreeChanged bool
	// Replayed is true when an immutable checkpoint already existed, so nothing was reapplied.
	Replayed bool
}

// ApplyImportedPage applies one staged page and records its immutable execution outcome, in a single
// transaction.
//
// One transaction per page, rather than one per job, is what makes execution restartable at page
// granularity: whatever committed stays committed, and the result row is the checkpoint that says so. The
// row is inserted in the same transaction as the page write and the mapping write, so there is no state in
// which a page exists without an outcome or an outcome without its page.
//
// Every decision is recomputed here under locks rather than taken from the plan. The plan records what the
// user approved; only this transaction can say what is still true of the target.
func (s *Store) ApplyImportedPage(in ImportPageExecution) (_ *ImportPageOutcome, err error) {
	if in.JobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "id", Value: in.JobID}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	// The job-row lock is held through commit: cancellation and terminalization take the same lock, so a
	// cancel arriving mid-page either precedes this page entirely or waits and wins before the next one.
	job, err := s.lockImportJobForActor(tx, in.JobID, "")
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStateImporting {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if job.TerminalIntent != model.ImportIntentNone {
		return nil, &ErrConflict{Resource: "ImportJob terminal_intent=" + string(job.TerminalIntent)}
	}

	// The immutable checkpoint short-circuits everything: a page that already has an outcome is never
	// reclassified, so a replay after a crash cannot turn a committed create into an update, or overwrite a
	// user's edits a second time.
	existing, err := s.getImportExecutionResult(tx, in.JobID, in.Ordinal)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err = tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "commit_transaction")
		}
		return &ImportPageOutcome{
			Ordinal:       existing.Ordinal,
			ExternalID:    existing.ExternalId,
			LocalID:       existing.LocalId,
			Title:         existing.Title,
			PlannedAction: existing.PlannedAction,
			ActualAction:  existing.ActualAction,
			Outcome:       existing.Outcome,
			Replayed:      true,
		}, nil
	}

	staged, err := s.getImportStagedPage(tx, in.JobID, in.Ordinal)
	if err != nil {
		return nil, err
	}
	channelID, err := s.lockLiveSpaceChannel(tx, job.TargetSpaceId)
	if err != nil {
		return nil, err
	}
	source, err := s.lockImportSourceForTarget(tx, job)
	if err != nil {
		return nil, err
	}

	applied, err := s.decideAndApplyImportedPage(tx, job, source, channelID, staged, in)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return applied, nil
}

// importPageWrite is the accumulated decision for one page, before it is persisted.
type importPageWrite struct {
	action  model.ImportAction
	outcome model.ImportOutcome
	localID string
	issues  []string
	details mmmodel.StringInterface
	// appliedHash is the content hash of what was written, stored as the new mapping baseline. It is empty
	// when nothing was written, which is what keeps a skipped page's baseline from being replaced.
	appliedHash     string
	appliedParentID string
	// wrote reports that the page row was created or modified.
	wrote bool
	// mappingInputsChanged reports that a field a later preflight classifies against has moved.
	mappingInputsChanged bool
}

// decideAndApplyImportedPage reclassifies one locked page, applies the decision, and writes the mapping,
// result, and issue rows. Must be called inside tx with the job row already locked.
func (s *Store) decideAndApplyImportedPage(
	tx *sqlx.Tx,
	job *model.ImportJob,
	source *model.ImportSource,
	channelID string,
	staged *model.ImportStagedPage,
	in ImportPageExecution,
) (*ImportPageOutcome, error) {
	mapping, mappedPage, err := s.lockImportMapping(tx, source.Id, staged.ExternalId)
	if err != nil {
		return nil, err
	}

	// A create's parent must resolve to a live page in this Space *now*. An existing page keeps its current
	// parent in V1, so its own parent is never in question and is deliberately not locked.
	parentAvailable, parentLocalID := true, ""
	parentMissing := false
	if mapping == nil && staged.ParentExternalId != "" {
		parentAvailable, parentLocalID, err = s.resolveImportParent(tx, job, source.Id, staged.ParentExternalId)
		if err != nil {
			return nil, err
		}
		parentMissing = !parentAvailable
	}

	atCap := false
	if mapping == nil {
		atCap, err = s.importSourceAtMappingCap(tx, source.Id)
		if err != nil {
			return nil, err
		}
	}

	local := importLocalPageState(mappedPage)
	decision := importer.DecideExecution(importer.ExecutionInput{
		ClassifyInput: importer.ClassifyInput{
			IncomingSourceContentHash: staged.IncomingSourceContentHash,
			IncomingParentExternalID:  staged.ParentExternalId,
			IncomingSourceOrdinal:     staged.SourceOrdinal,
			TargetSpaceID:             job.TargetSpaceId,
			Mapping:                   mappingBaselineOf(mapping),
			Local:                     local,
			ParentAvailable:           parentAvailable,
			MappingCapacityExceeded:   atCap,
		},
		ConflictApproved: in.ApprovedOverwrite,
		MappingUpdateAt:  mappingUpdateAtOf(mapping),
		Baselines: importer.PreflightBaselines{
			CurrentContentHash: staged.PreflightCurrentContentHash,
			MappingContentHash: staged.PreflightMappingContentHash,
			MappingUpdateAt:    staged.PreflightMappingUpdateAt,
		},
	})

	// A page the reviewed plan refused stays refused, even when the condition that blocked it has since
	// cleared. Two things go wrong otherwise: the user approved a plan that said this page would be skipped, so
	// creating it exceeds what they agreed to; and a blocked create carries no planned page id, so execution has
	// no id to create it with and would fail the entire job over one page.
	if staged.PlannedAction == model.ImportActionBlocked && decision.Action != model.ImportActionBlocked {
		decision.Action = model.ImportActionBlocked
		decision.Issues = append(decision.Issues, importer.IssueSkippedByReviewedPlan)
	}

	write := importPageWrite{
		action:  decision.Action,
		localID: decision.LocalID,
		issues:  decision.Issues,
		details: mmmodel.StringInterface{},
	}
	if parentMissing {
		// Classify reports an unavailable parent as parent_mapping_missing, which is the *preflight* meaning:
		// the parent is not in the bundle at all. At execution the parent was in the bundle and its own create
		// did not survive, which is a different thing for the reader to act on.
		write.issues = replaceIssueCode(write.issues,
			importer.IssueParentMappingMissing, importer.IssueParentNotAvailableAfterImport)
	}
	if in.ApprovedOverwrite && write.action == model.ImportActionUpdate {
		write.details["overwrite_approved"] = true
	}

	if err = s.applyImportPageDecision(tx, job, source, channelID, staged, in, mapping, mappedPage, parentLocalID, &write); err != nil {
		return nil, err
	}
	if err = s.persistImportPageMapping(tx, job, source, staged, mapping, &write); err != nil {
		return nil, err
	}
	return s.recordImportPageOutcome(tx, job, staged, in, &write)
}

// applyImportPageDecision performs the page write (or deliberately performs none) for one decision. It may
// downgrade a create to blocked: depth and sibling capacity are rechecked here under the locks, because an
// interactive edit can consume the room the review projected.
func (s *Store) applyImportPageDecision(
	tx *sqlx.Tx,
	job *model.ImportJob,
	source *model.ImportSource,
	channelID string,
	staged *model.ImportStagedPage,
	in ImportPageExecution,
	mapping *model.ImportEntity,
	mappedPage *model.Page,
	parentLocalID string,
	write *importPageWrite,
) error {
	switch write.action {
	case model.ImportActionCreate:
		return s.createImportedPage(tx, job, source, channelID, staged, in, parentLocalID, write)
	case model.ImportActionUpdate:
		return s.updateImportedPage(tx, job, source, staged, in, mapping, mappedPage, write)
	default:
		// noop, preserve_local, conflict, and blocked all deliberately leave the page alone. Their mapping
		// bookkeeping still runs, so a page that is present but skipped never reads as stale next time.
		write.outcome = importer.OutcomeForPlannedAction(write.action)
		return nil
	}
}

// createImportedPage inserts a new page using the exact id the preflight published.
func (s *Store) createImportedPage(
	tx *sqlx.Tx,
	job *model.ImportJob,
	source *model.ImportSource,
	channelID string,
	staged *model.ImportStagedPage,
	in ImportPageExecution,
	parentLocalID string,
	write *importPageWrite,
) error {
	if staged.PlannedPageId == "" {
		// Preflight publishes an id for every create and clears it for every block, so a create with no id
		// means the plan and the decision have diverged. Refusing beats inventing an id the report never named.
		return &ErrInvalidInput{Entity: "ImportStagedPage", Field: "PlannedPageId", Value: ""}
	}

	if parentLocalID != "" {
		parentDepth, depthErr := s.pageDepth(tx, parentLocalID)
		if depthErr != nil {
			return depthErr
		}
		if parentDepth+1 > model.MaxPageDepth {
			blockImportPage(write, importer.IssueTargetDepthExceeded)
			return nil
		}
	}

	// nextSortOrder takes the sibling group's advisory lock and enforces the group cap, so appending and the
	// capacity recheck are the same operation rather than two racing ones.
	sortOrder, err := s.nextSortOrder(tx, channelID, parentLocalID)
	if err != nil {
		if IsErrLimitExceeded(err) {
			blockImportPage(write, importer.IssueTargetSiblingCapacityExceeded)
			return nil
		}
		return err
	}

	createAt, createAtValid := importSourceTimestamp(staged.SourceCreateAt, in.Now)
	if !createAtValid {
		write.issues = append(write.issues, importer.IssueSourceCreateAtInvalid)
	}
	if _, updateAtValid := importSourceTimestamp(staged.SourceUpdateAt, in.Now); !updateAtValid {
		write.issues = append(write.issues, importer.IssueSourceUpdateAtInvalid)
	}

	docsImport := importDocsImportProps(job, source, staged, in)
	page := &model.Page{
		Id:        staged.PlannedPageId,
		SpaceId:   job.TargetSpaceId,
		ChannelId: channelID,
		ParentId:  parentLocalID,
		Type:      model.PageTypePage,
		Title:     staged.Title,
		Body:      staged.CanonicalBody,
		// SearchText is the projection the inspector already derived from this exact body, so it is reused
		// rather than recomputed: recomputing risks the two drifting for no benefit.
		SearchText: staged.SearchText,
		// Authorship states who wrote the page in Confluence. It is not a permission grant, so the resolved
		// author needs no membership of the target team or Space.
		UserId:         in.ResolvedUserID,
		LastModifiedBy: in.ResolvedUserID,
		SortOrder:      sortOrder,
		CreateAt:       createAt,
		Props:          mmmodel.StringInterface(importer.MergeDocsImportProps(nil, docsImport)),
	}
	page.PreSave()
	// PreSave stamps wall-clock UpdateAt/EditAt; the import's own monotonic timestamp replaces them so every
	// page in one job shares a coherent edit time.
	page.UpdateAt = in.Now
	page.EditAt = in.Now
	if validErr := page.IsValid(); validErr != nil {
		return &ErrInvalidInput{Entity: "Page", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	insertBuilder := s.getQueryBuilder().
		Insert("DOCS_Page").
		Columns(pageColumnList...).
		Values(pageToSlice(page)...)
	if _, err = s.execBuilder(tx, insertBuilder); err != nil {
		if isUniqueViolation(err) {
			return &ErrConflict{Resource: "Page id=" + page.Id}
		}
		return errors.Wrap(err, "unable_to_insert_imported_page")
	}

	hash, err := importer.HashAppliedContent(importer.AppliedContentHashInput{
		Title:                  page.Title,
		BodyFormat:             importer.BodyFormatCanonicalTipTap,
		Body:                   page.Body,
		DocsImportSourceFields: importer.DocsImportSourceFields(docsImport),
	})
	if err != nil {
		return errors.Wrap(err, "hash applied import content")
	}

	write.localID = page.Id
	write.outcome = model.ImportOutcomeCreated
	write.appliedHash = hash
	write.appliedParentID = parentLocalID
	write.wrote = true
	// A brand-new mapping is by definition an input every later preflight classifies against.
	write.mappingInputsChanged = true
	return nil
}

// updateImportedPage rewrites a mapped page's content, leaving its structure and original ownership alone.
func (s *Store) updateImportedPage(
	tx *sqlx.Tx,
	job *model.ImportJob,
	source *model.ImportSource,
	staged *model.ImportStagedPage,
	in ImportPageExecution,
	mapping *model.ImportEntity,
	mappedPage *model.Page,
	write *importPageWrite,
) error {
	if mapping == nil || mappedPage == nil {
		return &ErrInvalidInput{Entity: "ImportEntity", Field: "mapping", Value: staged.ExternalId}
	}
	if _, updateAtValid := importSourceTimestamp(staged.SourceUpdateAt, in.Now); !updateAtValid {
		write.issues = append(write.issues, importer.IssueSourceUpdateAtInvalid)
	}

	docsImport := importDocsImportProps(job, source, staged, in)
	// Only the importer's own namespace is replaced. Whatever interactive editing or another feature stored
	// on this page survives a reimport untouched.
	props := mmmodel.StringInterface(importer.MergeDocsImportProps(mappedPage.Props, docsImport))
	title := staged.Title

	// The merged props are validated, not just the importer's own contribution. A page already close to the
	// props limit can leave no room for the docs_import namespace, and the direct update below would write an
	// over-limit value that no DB constraint rejects — leaving a page that model validation refuses, which makes
	// it uneditable through the normal API. Refusing to write it and reporting why is the honest outcome.
	if model.ValidatePropsSize("ImportedPage", "id="+mappedPage.Id, props, model.PagePropsMaxBytes) != nil {
		blockImportPage(write, importer.IssuePagePropsTooLarge)
		write.localID = mappedPage.Id
		return nil
	}

	// EditAt is an optimistic-lock token, so it must never move backwards. in.Now is captured before this
	// transaction takes its locks, so a concurrent edit that committed in between can already hold a later
	// timestamp; writing in.Now over it would let a client holding the older token pass a compare-and-swap it
	// should fail.
	writeAt := max(in.Now, mappedPage.UpdateAt+1, mappedPage.EditAt+1)

	updateBuilder := s.getQueryBuilder().
		Update("DOCS_Page").
		Set("Title", title).
		Set("Body", staged.CanonicalBody).
		Set("SearchText", staged.SearchText).
		Set("Props", props).
		// The importing actor is the editor of record; Page.UserId keeps the original author, because a
		// reimport is not a change of authorship.
		Set("LastModifiedBy", job.ActorId).
		Set("UpdateAt", writeAt).
		Set("EditAt", writeAt).
		Where(sq.Eq{"Id": mappedPage.Id}).
		Where(liveNonSnapshotFilter(""))
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return errors.Wrap(err, "unable_to_update_imported_page")
	}
	if err = checkRowsAffected(result, "Page", mappedPage.Id); err != nil {
		return err
	}

	hash, err := importer.HashAppliedContent(importer.AppliedContentHashInput{
		Title:                  title,
		BodyFormat:             importer.BodyFormatCanonicalTipTap,
		Body:                   staged.CanonicalBody,
		DocsImportSourceFields: importer.DocsImportSourceFields(docsImport),
	})
	if err != nil {
		return errors.Wrap(err, "hash applied import content")
	}

	write.localID = mappedPage.Id
	write.outcome = model.ImportOutcomeUpdated
	write.appliedHash = hash
	// The local parent is preserved, so the structural baseline follows where the page actually is rather
	// than where the source would now put it.
	write.appliedParentID = mappedPage.ParentId
	write.wrote = true
	return nil
}

// blockImportPage rewrites a decision as blocked with the given reason. A per-page limit breach is a
// reportable outcome for that page, never a failure of the job, so this is a decision change rather than an
// error: the remaining pages still deserve their chance to be imported.
func blockImportPage(write *importPageWrite, code string) {
	write.action = model.ImportActionBlocked
	write.outcome = model.ImportOutcomeBlocked
	write.localID = ""
	write.issues = append(write.issues, code)
	write.wrote = false
	write.appliedHash = ""
}

// persistImportPageMapping writes the durable mapping for one applied or skipped page.
//
// A mapped page present in the bundle always has its LastSeenJobId refreshed, whatever the content decision:
// presence tracking is what stops a page this job deliberately skipped from being reported as stale by the
// next import. The content baselines, by contrast, are replaced only when content was actually written —
// replacing them for a skipped conflict would erase the record of what was last successfully applied and
// make the conflict silently disappear.
func (s *Store) persistImportPageMapping(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	source *model.ImportSource,
	staged *model.ImportStagedPage,
	mapping *model.ImportEntity,
	write *importPageWrite,
) error {
	at := mmmodel.GetMillis()

	if mapping == nil {
		if !write.wrote || write.localID == "" {
			// Nothing was created and nothing was mapped, so there is no mapping to record. A blocked create
			// deliberately leaves no trace: a mapping would claim a page exists that does not.
			return nil
		}
		entity := &model.ImportEntity{
			ImportSourceId:             source.Id,
			EntityType:                 model.ImportEntityTypePage,
			ExternalId:                 staged.ExternalId,
			LocalId:                    write.localID,
			LastSourceContentHash:      staged.IncomingSourceContentHash,
			LastAppliedContentHash:     write.appliedHash,
			LastAppliedParentId:        write.appliedParentID,
			LastSourceParentExternalId: staged.ParentExternalId,
			LastSourceTitle:            staged.Title,
			LastSourceOrdinal:          staged.SourceOrdinal,
			FirstJobId:                 job.Id,
			LastSeenJobId:              job.Id,
			CreateAt:                   at,
			UpdateAt:                   at,
		}
		insertBuilder := s.getQueryBuilder().
			Insert("DOCS_ImportEntity").
			Columns(importEntityColumns...).
			Values(entity.ImportSourceId, entity.EntityType, entity.ExternalId, entity.LocalId,
				entity.LastSourceContentHash, entity.LastAppliedContentHash, entity.LastAppliedParentId,
				entity.LastSourceParentExternalId, entity.LastSourceTitle, entity.LastSourceOrdinal,
				entity.FirstJobId, entity.LastSeenJobId, entity.CreateAt, entity.UpdateAt)
		if _, err := s.execBuilder(tx, insertBuilder); err != nil {
			if isUniqueViolation(err) {
				return &ErrConflict{Resource: "ImportEntity external_id=" + staged.ExternalId}
			}
			return errors.Wrap(err, "unable_to_save_import_entity")
		}
		return nil
	}

	// Latest-observed source structure is recorded even when it is not applied, so the next import can tell a
	// page that moved once from one that keeps moving. LastSourceTitle is deliberately excluded from the
	// change detection below: it only affects how a stale entry is labelled in a report, and bumping the
	// source revision for a rename would invalidate every other job's reviewed plan for a cosmetic edit.
	changed := mapping.LastSourceParentExternalId != staged.ParentExternalId ||
		mapping.LastSourceOrdinal != staged.SourceOrdinal
	builder := s.getQueryBuilder().
		Update("DOCS_ImportEntity").
		Set("LastSeenJobId", job.Id).
		Set("LastSourceTitle", staged.Title).
		Set("LastSourceOrdinal", staged.SourceOrdinal).
		Set("LastSourceParentExternalId", staged.ParentExternalId).
		Set("UpdateAt", at)
	if write.wrote {
		changed = changed ||
			mapping.LastSourceContentHash != staged.IncomingSourceContentHash ||
			mapping.LastAppliedContentHash != write.appliedHash ||
			mapping.LastAppliedParentId != write.appliedParentID
		builder = builder.
			Set("LastSourceContentHash", staged.IncomingSourceContentHash).
			Set("LastAppliedContentHash", write.appliedHash).
			Set("LastAppliedParentId", write.appliedParentID)
	}
	builder = builder.Where(sq.Eq{
		"ImportSourceId": source.Id,
		"EntityType":     model.ImportEntityTypePage,
		"ExternalId":     staged.ExternalId,
	})
	result, err := s.execBuilder(tx, builder)
	if err != nil {
		return errors.Wrap(err, "unable_to_update_import_entity")
	}
	if err = checkRowsAffected(result, "ImportEntity", staged.ExternalId); err != nil {
		return err
	}
	write.mappingInputsChanged = write.mappingInputsChanged || changed
	return nil
}

// recordImportPageOutcome inserts the immutable execution result and its issue rows, and records on the job
// whether a later preflight's inputs have moved. Must be called inside tx, in the same transaction as the
// page and mapping writes.
func (s *Store) recordImportPageOutcome(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	staged *model.ImportStagedPage,
	in ImportPageExecution,
	write *importPageWrite,
) (*ImportPageOutcome, error) {
	if write.mappingInputsChanged {
		write.details["mapping_inputs_changed"] = true
	}
	if in.AuthorFallbackReason != "" && write.action == model.ImportActionCreate {
		// Recorded on the result rather than only as an issue because the final report's author counts are
		// rebuilt from results: issues are discretionary and may be truncated, outcomes never are.
		write.details["author_fallback"] = true
		write.details["author_fallback_reason"] = in.AuthorFallbackReason
		write.issues = append(write.issues, importer.IssueAuthorFallbackToActor)
	}

	at := mmmodel.GetMillis()
	record := &model.ImportResultRecord{
		JobId:         job.Id,
		Stage:         model.ImportStageExecution,
		Ordinal:       staged.Ordinal,
		EntityType:    model.ImportEntityTypePage,
		ExternalId:    staged.ExternalId,
		LocalId:       write.localID,
		Title:         staged.Title,
		PlannedAction: staged.PlannedAction,
		ActualAction:  write.action,
		Outcome:       write.outcome,
		Details:       write.details,
		CreateAt:      at,
		UpdateAt:      at,
	}
	if validErr := record.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "ImportResult", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}
	detailsBytes, err := jsonByteLen(jsonbMap(record.Details))
	if err != nil {
		return nil, err
	}
	if err = s.insertImportResults(tx, []*model.ImportResultRecord{record}); err != nil {
		return nil, err
	}
	retained := retainedResultRowBytes(record, detailsBytes)

	issueBytes, err := s.recordImportPageIssues(tx, job, staged, write)
	if err != nil {
		return nil, err
	}
	if err = s.chargeImportExecutionRow(tx, job, retained+issueBytes, issueBytes, write.mappingInputsChanged); err != nil {
		return nil, err
	}

	return &ImportPageOutcome{
		Ordinal:       record.Ordinal,
		ExternalID:    record.ExternalId,
		LocalID:       record.LocalId,
		Title:         record.Title,
		PlannedAction: record.PlannedAction,
		ActualAction:  record.ActualAction,
		Outcome:       record.Outcome,
		TreeChanged:   write.wrote,
	}, nil
}

// recordImportPageIssues writes one page's execution issues and returns what they cost, or records a single
// truncation notice once the job's discretionary issue allowance is spent.
//
// Outcomes are mandatory and issues are not, so this is where the two pools diverge: a report that runs out
// of room stops explaining pages but never stops recording what happened to them.
func (s *Store) recordImportPageIssues(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	staged *model.ImportStagedPage,
	write *importPageWrite,
) (int64, error) {
	codes := write.issues
	if len(codes) > model.ImportMaxIssueCodesPerPage {
		codes = codes[:model.ImportMaxIssueCodesPerPage]
	}
	if len(codes) == 0 {
		return 0, nil
	}
	if job.IssueBudgetRemaining() <= 0 {
		return 0, s.recordImportIssueTruncation(tx, job.Id, model.ImportStageExecution)
	}

	records := make([]*model.ImportIssueRecord, 0, len(codes))
	var spent int64
	for i, code := range codes {
		record := &model.ImportIssueRecord{
			JobId:      job.Id,
			Stage:      model.ImportStageExecution,
			Ordinal:    staged.Ordinal*model.ImportIssuesPerPage + i,
			Severity:   importer.IssueSeverity(code),
			Code:       code,
			EntityType: model.ImportEntityTypePage,
			ExternalId: staged.ExternalId,
			LocalId:    write.localID,
			Title:      staged.Title,
			Message:    importer.IssueMessage(code),
		}
		record.Remediation = importer.IssueRemediation(code)
		detailsBytes, err := jsonByteLen(jsonbMap(record.Details))
		if err != nil {
			return spent, err
		}
		cost := retainedIssueRowBytes(record, detailsBytes)
		if spent+cost > job.IssueBudgetRemaining() {
			if err = s.recordImportIssueTruncation(tx, job.Id, model.ImportStageExecution); err != nil {
				return spent, err
			}
			break
		}
		spent += cost
		records = append(records, record)
	}
	if len(records) == 0 {
		return spent, nil
	}
	return spent, s.insertImportIssues(tx, records)
}

// recordImportIssueTruncation records, at most once per job and stage, that findings stopped being listed.
//
// ON CONFLICT DO NOTHING is right here and nowhere else in terminal writing: the row is a singleton at a
// fixed ordinal whose content never varies, so a second attempt is genuinely the same row rather than a
// conflicting one.
func (s *Store) recordImportIssueTruncation(tx sqlx.ExtContext, jobID string, stage model.ImportIssueStage) error {
	record := &model.ImportIssueRecord{
		JobId:       jobID,
		Stage:       stage,
		Ordinal:     model.ImportTerminalIssueOrdinalBase + importTerminalIssueTruncated,
		Severity:    model.ImportSeverityWarning,
		Code:        importer.IssueReportTruncated,
		Message:     importer.IssueMessage(importer.IssueReportTruncated),
		Remediation: importer.IssueRemediation(importer.IssueReportTruncated),
	}
	builder := s.getQueryBuilder().
		Insert("DOCS_ImportIssue").
		Columns(importIssueColumns...).
		Values(record.JobId, string(record.Stage), record.Ordinal, string(record.Severity), record.Code,
			record.EntityType, record.ExternalId, record.LocalId, record.Title,
			record.Message, record.Remediation, jsonbMap(record.Details)).
		Suffix("ON CONFLICT (JobId, Stage, Ordinal) DO NOTHING")
	if _, err := s.execBuilder(tx, builder); err != nil {
		return errors.Wrap(err, "unable_to_save_import_truncation_issue")
	}
	return nil
}

// chargeImportExecutionRow adds one page's retained cost to the job's accounting and, when needed, sets the
// durable flag terminalization reads to decide whether to bump the source's mapping revision.
//
// The flag is written in the same transaction as the page decision on purpose: a revision bump inferred after
// the fact could miss a page whose transaction committed just before a crash. Must be called inside tx.
func (s *Store) chargeImportExecutionRow(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	retained, issueBytes int64,
	mappingInputsChanged bool,
) error {
	builder := s.getQueryBuilder().Update("DOCS_ImportJob")
	if retained != 0 {
		builder = builder.Set("RetainedBytes", sq.Expr("RetainedBytes + ?", retained))
	}
	if issueBytes != 0 {
		builder = builder.Set("RetainedIssueBytes", sq.Expr("RetainedIssueBytes + ?", issueBytes))
	}
	if mappingInputsChanged {
		builder = builder.Set("MappingInputsChanged", true)
	}
	if retained == 0 && issueBytes == 0 && !mappingInputsChanged {
		return nil
	}
	if _, err := s.execBuilder(tx, builder.Where(sq.Eq{"Id": job.Id})); err != nil {
		return errors.Wrap(err, "unable_to_charge_import_execution_row")
	}
	job.RetainedBytes += retained
	job.RetainedIssueBytes += issueBytes
	job.MappingInputsChanged = job.MappingInputsChanged || mappingInputsChanged
	return nil
}

// --- locked reads ---

// getImportExecutionResult returns a page's immutable execution checkpoint, or nil when it has none. Must be
// called inside tx with the job row locked.
func (s *Store) getImportExecutionResult(tx sqlx.ExtContext, jobID string, ordinal int) (*model.ImportResultRecord, error) {
	var record model.ImportResultRecord
	builder := s.getQueryBuilder().
		Select(importResultColumns...).
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": jobID, "Stage": string(model.ImportStageExecution), "Ordinal": ordinal})
	if err := s.getBuilder(tx, &record, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "unable_to_get_import_execution_result")
	}
	return &record, nil
}

// getImportStagedPage reads one staged page inside the execution transaction. Must be called inside tx.
func (s *Store) getImportStagedPage(tx sqlx.ExtContext, jobID string, ordinal int) (*model.ImportStagedPage, error) {
	var staged model.ImportStagedPage
	builder := s.getQueryBuilder().
		Select(importStagedPageColumns...).
		From("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": jobID, "Ordinal": ordinal})
	if err := s.getBuilder(tx, &staged, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ImportStagedPage", ID: jobID}
		}
		return nil, errors.Wrap(err, "unable_to_get_import_staged_page")
	}
	return &staged, nil
}

// lockImportSourceForTarget locks the job's selected ImportSource and verifies it still belongs to the
// target Space. Must be called inside tx.
func (s *Store) lockImportSourceForTarget(tx sqlx.ExtContext, job *model.ImportJob) (*model.ImportSource, error) {
	var source model.ImportSource
	builder := s.getQueryBuilder().
		Select(importSourceColumns...).
		From("DOCS_ImportSource").
		Where(sq.Eq{"Id": job.SelectedImportSourceId}).
		Suffix("FOR UPDATE")
	if err := s.getBuilder(tx, &source, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrImportSourceMissing{JobID: job.Id, SourceID: job.SelectedImportSourceId}
		}
		return nil, errors.Wrap(err, "unable_to_lock_import_source")
	}
	if source.SpaceId != job.TargetSpaceId {
		return nil, &ErrConflict{Resource: "ImportSource space_id=" + source.SpaceId}
	}
	return &source, nil
}

// lockImportMapping locks a page's mapping and the page it points at, following the global lock order
// (job, space, source, mapping, page). The mapped page is read including soft-deleted rows: whether it was
// deleted is a decision input, not a reason to fail. Must be called inside tx.
func (s *Store) lockImportMapping(tx sqlx.ExtContext, sourceID, externalID string) (*model.ImportEntity, *model.Page, error) {
	var mapping model.ImportEntity
	builder := s.getQueryBuilder().
		Select(importEntityColumns...).
		From("DOCS_ImportEntity").
		Where(sq.Eq{"ImportSourceId": sourceID, "EntityType": model.ImportEntityTypePage, "ExternalId": externalID}).
		Suffix("FOR UPDATE")
	if err := s.getBuilder(tx, &mapping, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, errors.Wrap(err, "unable_to_lock_import_entity")
	}

	var page model.Page
	pageBuilder := s.getQueryBuilder().
		Select(pageColumnList...).
		From("DOCS_Page").
		Where(sq.Eq{"Id": mapping.LocalId, "OriginalId": ""}).
		Suffix("FOR UPDATE")
	if err := s.getBuilder(tx, &page, pageBuilder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &mapping, nil, nil
		}
		return nil, nil, errors.Wrap(err, "unable_to_lock_mapped_page")
	}
	return &mapping, &page, nil
}

// resolveImportParent resolves a staged page's source parent to a live local page in the target Space.
//
// The parent's *mapping* is the authority, not any in-memory plan: by the time a child is processed its
// parent's own transaction has either committed a mapping or not, which makes parent availability a durable
// fact that survives a restart mid-import. Must be called inside tx.
func (s *Store) resolveImportParent(tx *sqlx.Tx, job *model.ImportJob, sourceID, parentExternalID string) (bool, string, error) {
	var localID string
	builder := s.getQueryBuilder().
		Select("LocalId").
		From("DOCS_ImportEntity").
		Where(sq.Eq{"ImportSourceId": sourceID, "EntityType": model.ImportEntityTypePage, "ExternalId": parentExternalID})
	if err := s.getBuilder(tx, &localID, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", errors.Wrap(err, "unable_to_resolve_import_parent")
	}

	live, err := s.tryLockLiveParent(tx, localID, job.TargetSpaceId)
	if err != nil {
		return false, "", err
	}
	if !live {
		// A mapped parent that is deleted or has moved out of the Space cannot receive children. Rooting them
		// at the Space instead would silently flatten a hierarchy the user never asked to change.
		return false, "", nil
	}
	return true, localID, nil
}

// importSourceAtMappingCap reports whether the source already holds its full complement of mappings.
//
// OFFSET cap-1 LIMIT 1 asks exactly the question that matters — "are there already this many" — and stops as
// soon as it knows, rather than counting a set whose exact size is irrelevant. Must be called inside tx.
func (s *Store) importSourceAtMappingCap(tx sqlx.ExtContext, sourceID string) (bool, error) {
	var atCap bool
	query := `SELECT EXISTS (
		SELECT 1 FROM DOCS_ImportEntity
		WHERE ImportSourceId = $1 AND EntityType = 'page'
		ORDER BY ExternalId
		OFFSET $2 LIMIT 1
	)`
	if err := s.get(tx, &atCap, query, sourceID, model.ImportMaxMappingsPerSource-1); err != nil {
		return false, errors.Wrap(err, "unable_to_check_import_mapping_cap")
	}
	return atCap, nil
}

// --- small projections ---

// importLocalPageState projects a locked mapped page into the classifier's view, computing its current
// applied-content hash.
//
// A body that no longer canonicalizes is hashed as opaque rather than treated as an error: an opaque hash
// compares unequal to any canonical baseline, so the page reads as a definite local edit and is protected
// instead of overwritten. A hashing failure is handled the same way, which is the safe direction.
func importLocalPageState(page *model.Page) importer.LocalPageState {
	if page == nil {
		return importer.LocalPageState{Exists: false}
	}
	state := importer.LocalPageState{
		Exists:          true,
		Deleted:         page.DeleteAt != 0,
		SpaceID:         page.SpaceId,
		ParentID:        page.ParentId,
		BodyIsCanonical: true,
	}
	body := page.Body
	bodyFormat := importer.BodyFormatCanonicalTipTap
	if canonical, _, _, err := importer.CanonicalizeAndExtractSearchText(page.Body); err == nil {
		body = canonical
	} else {
		bodyFormat = importer.BodyFormatOpaqueRaw
		state.BodyIsCanonical = false
	}
	hash, err := importer.HashAppliedContent(importer.AppliedContentHashInput{
		Title:                  page.Title,
		BodyFormat:             bodyFormat,
		Body:                   body,
		DocsImportSourceFields: importer.DocsImportSourceFields(importer.DocsImportNamespace(page.Props)),
	})
	if err != nil {
		state.AppliedContentHash = ""
		state.BodyIsCanonical = false
		return state
	}
	state.AppliedContentHash = hash
	return state
}

// mappingBaselineOf projects a mapping row onto the classifier's baseline, returning nil for an absent
// mapping so a never-imported page classifies as a create.
func mappingBaselineOf(e *model.ImportEntity) *importer.MappingBaseline {
	if e == nil {
		return nil
	}
	return &importer.MappingBaseline{
		ExternalID:                 e.ExternalId,
		LocalID:                    e.LocalId,
		LastSourceContentHash:      e.LastSourceContentHash,
		LastAppliedContentHash:     e.LastAppliedContentHash,
		LastAppliedParentID:        e.LastAppliedParentId,
		LastSourceParentExternalID: e.LastSourceParentExternalId,
		LastSourceOrdinal:          e.LastSourceOrdinal,
		LastSourceTitle:            e.LastSourceTitle,
		UpdateAt:                   e.UpdateAt,
	}
}

// mappingUpdateAtOf returns a mapping's UpdateAt, or zero when there is no mapping.
func mappingUpdateAtOf(e *model.ImportEntity) int64 {
	if e == nil {
		return 0
	}
	return e.UpdateAt
}

// replaceIssueCode swaps one issue code for another, leaving the rest of the list and its order intact.
func replaceIssueCode(codes []string, from, to string) []string {
	for i, code := range codes {
		if code == from {
			codes[i] = to
		}
	}
	return codes
}

// importSourceTimestamp returns the source timestamp to use for a page's CreateAt and whether the source
// value was usable. A missing or future timestamp is replaced by the import's own clock: a page dated after
// the import that created it would sort and read as nonsense.
func importSourceTimestamp(sourceAt, now int64) (int64, bool) {
	if sourceAt <= 0 || sourceAt > now {
		return now, false
	}
	return sourceAt, true
}

// importDocsImportProps builds the docs_import namespace for one page.
//
// The username recorded is the *effective* proposal — the manifest's mapping when it has one, otherwise the
// page's own user field — because that is the identity the source-content hash was computed from. Recording a
// different one would let a real change of attribution leave both hashes untouched.
func importDocsImportProps(
	job *model.ImportJob,
	source *model.ImportSource,
	staged *model.ImportStagedPage,
	in ImportPageExecution,
) map[string]any {
	return importer.BuildDocsImportProps(importer.DocsImportInput{
		ImportSourceID:  source.Id,
		OrganizationID:  source.OrganizationId,
		SpaceKey:        source.ExternalSpaceKey,
		ExternalPageID:  staged.ExternalId,
		SourceAccountID: staged.SourceAuthorAccountId,
		SourceUsername:  in.AuthorProposal,
		ResolvedUserID:  in.ResolvedUserID,
		FallbackReason:  in.AuthorFallbackReason,
		SourceCreateAt:  staged.SourceCreateAt,
		SourceUpdateAt:  staged.SourceUpdateAt,
		LastJobID:       job.Id,
		SourceProps:     staged.SourceProps,
	})
}

// setImportPhase records the job's current phase for progress reporting. Must be called inside tx.
func (s *Store) setImportPhase(tx sqlx.ExtContext, job *model.ImportJob, phase model.ImportJobPhase) error {
	if job.Phase == phase {
		return nil
	}
	at := mmmodel.GetMillis()
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("Phase", string(phase)).
		Set("UpdateAt", monotonicBump("UpdateAt", at)).
		Where(sq.Eq{"Id": job.Id})
	if _, err := s.execBuilder(tx, builder); err != nil {
		return errors.Wrap(err, "unable_to_set_import_phase")
	}
	job.Phase = phase
	job.UpdateAt = max(job.UpdateAt+1, at)
	return nil
}
