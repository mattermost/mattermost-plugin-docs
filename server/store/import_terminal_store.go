// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Offsets within model.ImportTerminalIssueOrdinalBase. Every issue terminalization writes gets a documented,
// non-overlapping slot so a retry derives exactly the same rows: terminalization must be able to insert its
// full set without tolerating conflicts, because a conflict it tolerated would hide a real collision.
const (
	// importTerminalIssueTruncated is written by per-page execution when the issue allowance runs out, so it
	// is reserved here rather than assigned.
	importTerminalIssueTruncated = 0
	// importTerminalIssueReason explains, once, why a job's pages carry not-attempted outcomes.
	importTerminalIssueReason = 1
	// importTerminalIssueInvariant records a completion claimed with a page outcome missing.
	importTerminalIssueInvariant = 2
	// importTerminalIssueChannelBase is the first slot for per-attempt channel compensation findings.
	importTerminalIssueChannelBase = 3
)

// ImportCompensation is one channel attempt's compensation outcome, as observed by the app before the final
// transaction. Resolved means the channel is known to be gone — archived by this pass, or already absent.
//
// An unresolved compensation never blocks the report: a job whose channel could not be archived still needs
// to tell its owner what happened. It does become an error-severity finding, because it is real operator
// work rather than a footnote.
type ImportCompensation struct {
	AttemptID string
	ChannelID string
	Resolved  bool
	Reason    string
}

// TerminalizeImportJob completes a job sitting in terminalizing, honouring the intent recorded when it
// entered that state, and returns the finished job.
//
// Terminalization is worker work rather than part of the transition that decided the outcome, because the
// durable report has to exist before the job is terminal: a crash mid-way must resume, not leave a terminal
// job with an empty report. Everything it writes happens in one transaction, so a crash before commit rolls
// the whole set back and the retry derives it again identically.
//
// compensations are the channel-archive outcomes the caller established beforehand, because archiving a
// channel is an external call that must not happen inside a database transaction.
func (s *Store) TerminalizeImportJob(jobID string, compensations []ImportCompensation) (_ *model.ImportJob, err error) {
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
	if err = s.finishImportJob(tx, job, job.TerminalIntent, job.ErrorCode, now, compensations); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}
	return job, nil
}

// ImportErrorIncompleteOutcomes is the stable error code for a job that claimed completion while a staged
// page had no execution checkpoint.
const ImportErrorIncompleteOutcomes = "incomplete_page_outcomes"

// finishImportJob is the one path from "this job is over" to a terminal state, shared by direct cancellation
// and worker terminalization. It records a durable outcome for every entity that lacks one, aggregates the
// final summary from those outcomes, publishes the source's mapping revision bump, releases the staged
// reservation, and trues up the retained one — all in the caller's transaction.
//
// The job value is updated in place to mirror every column written, so the caller returns what a subsequent
// read would return rather than a half-stale copy.
func (s *Store) finishImportJob(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	intent model.ImportTerminalIntent,
	errorCode string,
	now int64,
	compensations []ImportCompensation,
) error {
	previousState := job.State

	// A completion claim is verified before it is honoured. Every staged page must already carry an immutable
	// outcome; one missing means the worker stopped believing it had finished when it had not, and reporting
	// success would tell the user their whole space imported when part of it did not.
	terminalIssues := make([]*model.ImportIssueRecord, 0, len(compensations)+2)
	if intent == model.ImportIntentCompleted {
		missing, err := s.countImportPagesWithoutOutcome(tx, job.Id)
		if err != nil {
			return err
		}
		if missing > 0 {
			intent = model.ImportIntentFailed
			errorCode = ImportErrorIncompleteOutcomes
			terminalIssues = append(terminalIssues, s.terminalIssue(job.Id, importTerminalIssueInvariant,
				importer.IssueMissingPageOutcome, mmmodel.StringInterface{"pages_without_outcome": missing}))
		}
	}

	if err := s.recordMissingImportOutcomes(tx, job, intent, errorCode, now); err != nil {
		return err
	}
	if intent != model.ImportIntentCompleted {
		reason := importer.IssueNotAttemptedFailed
		if intent == model.ImportIntentCanceled {
			reason = importer.IssueNotAttemptedCanceled
		}
		details := mmmodel.StringInterface{"finished_from_state": string(previousState)}
		if errorCode != "" {
			details["reason"] = errorCode
		}
		terminalIssues = append(terminalIssues, s.terminalIssue(job.Id, importTerminalIssueReason, reason, details))
	}
	terminalIssues = append(terminalIssues, s.compensationIssues(job.Id, compensations)...)
	if err := s.insertImportIssues(tx, terminalIssues); err != nil {
		return err
	}
	for _, issue := range terminalIssues {
		detailsBytes, err := jsonByteLen(jsonbMap(issue.Details))
		if err != nil {
			return err
		}
		cost := retainedIssueRowBytes(issue, detailsBytes)
		job.RetainedBytes += cost
		job.RetainedIssueBytes += cost
	}

	finalSummary, treeChanged, err := s.composeImportFinalSummary(tx, job)
	if err != nil {
		return err
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
	if err = s.publishImportSourceRevision(tx, job, intent, now); err != nil {
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
		// The invalidation flag and the terminal state commit together, so a crash can neither lose the
		// obligation to invalidate nor leave a job claiming one it never incurred.
		Set("InvalidationPending", treeChanged).
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
	job.InvalidationPending = treeChanged
	job.StagedBytes = 0
	return nil
}

// terminalIssue builds one job-level terminal issue at its reserved ordinal slot.
func (s *Store) terminalIssue(jobID string, slot int, code string, details mmmodel.StringInterface) *model.ImportIssueRecord {
	return &model.ImportIssueRecord{
		JobId:       jobID,
		Stage:       model.ImportStageExecution,
		Ordinal:     model.ImportTerminalIssueOrdinalBase + slot,
		Severity:    importer.IssueSeverity(code),
		Code:        code,
		Message:     importer.IssueMessage(code),
		Remediation: importer.IssueRemediation(code),
		Details:     details,
	}
}

// compensationIssues records what became of each channel created for a Space that never existed, in attempt
// order so the ordinals are stable across retries.
func (s *Store) compensationIssues(jobID string, compensations []ImportCompensation) []*model.ImportIssueRecord {
	issues := make([]*model.ImportIssueRecord, 0, len(compensations))
	for i, c := range compensations {
		code := importer.IssueChannelCompensated
		details := mmmodel.StringInterface{"attempt_id": c.AttemptID, "channel_id": c.ChannelID}
		if !c.Resolved {
			code = importer.IssueChannelCompensationFailed
			details["reason"] = c.Reason
		}
		issues = append(issues, s.terminalIssue(jobID, importTerminalIssueChannelBase+i, code, details))
	}
	return issues
}

// countImportPagesWithoutOutcome counts staged pages with no immutable execution checkpoint. Must be called
// inside tx, before the staged rows are released.
func (s *Store) countImportPagesWithoutOutcome(tx sqlx.ExtContext, jobID string) (int, error) {
	var count int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportStagedPage p").
		Where(sq.Eq{"p.JobId": jobID}).
		Where(`NOT EXISTS (
			SELECT 1 FROM DOCS_ImportResult x
			WHERE x.JobId = p.JobId AND x.Stage = 'execution' AND x.Ordinal = p.Ordinal
		)`)
	if err := s.getBuilder(tx, &count, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_count_import_pages_without_outcome")
	}
	return count, nil
}

// publishImportSourceRevision applies the once-per-job mapping revision bump and, for a genuine completion,
// records the source's successful import.
//
// The increment happens in the same transaction as the transition out of terminalizing, which is exactly why
// it can neither be missed nor applied twice: a restart either sees a job still terminalizing and no bump, or
// a terminal job and the bump. Per-page increments would give neither guarantee. Must be called inside tx.
func (s *Store) publishImportSourceRevision(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	intent model.ImportTerminalIntent,
	now int64,
) error {
	if job.SelectedImportSourceId == "" {
		return nil
	}
	builder := s.getQueryBuilder().Update("DOCS_ImportSource").Set("UpdateAt", now)
	touched := false
	if job.MappingInputsChanged {
		// One bump per job, not per page: every other job's reviewed preflight is invalidated by this, and a
		// per-page bump would invalidate them once per page for no additional safety.
		builder = builder.Set("MappingRevision", sq.Expr("MappingRevision + 1"))
		touched = true
	}
	if intent == model.ImportIntentCompleted {
		builder = builder.Set("LastImportAt", now).Set("LastSuccessfulJobId", job.Id)
		touched = true
	}
	if !touched {
		return nil
	}
	if _, err := s.execBuilder(tx, builder.Where(sq.Eq{"Id": job.SelectedImportSourceId})); err != nil {
		return errors.Wrap(err, "unable_to_publish_import_source_revision")
	}
	if job.MappingInputsChanged {
		// The flag is cleared with the bump it caused, so "this job still owes a revision bump" stops being
		// true the moment it stops being true. Relying on the terminal-state compare-and-set to prevent a second
		// bump would make the invariant depend on something two transactions away.
		clearBuilder := s.getQueryBuilder().
			Update("DOCS_ImportJob").
			Set("MappingInputsChanged", false).
			Where(sq.Eq{"Id": job.Id})
		if _, err := s.execBuilder(tx, clearBuilder); err != nil {
			return errors.Wrap(err, "unable_to_clear_import_mapping_inputs_changed")
		}
		job.MappingInputsChanged = false
	}
	return nil
}

// terminalStateFor maps a terminal intent onto the state the job lands in.
//
// A completed job that recorded any warning or error lands in completed_with_issues rather than completed, so
// "it worked" never hides a page that was skipped, an author who could not be matched, or content that was
// counted but not imported. Warnings count, not just errors: preserved local edits and omitted comments are
// exactly the outcomes a user needs to look at, and neither prevents the import from having succeeded.
// Must be called inside tx.
func (s *Store) terminalStateFor(tx sqlx.ExtContext, jobID string, intent model.ImportTerminalIntent) (model.ImportJobState, error) {
	switch intent {
	case model.ImportIntentCanceled:
		return model.ImportStateCanceled, nil
	case model.ImportIntentFailed:
		return model.ImportStateFailed, nil
	}
	var notable int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportIssue").
		Where(sq.Eq{"JobId": jobID}).
		Where(sq.Eq{"Severity": []string{
			string(model.ImportSeverityWarning), string(model.ImportSeverityError),
		}})
	if err := s.getBuilder(tx, &notable, builder); err != nil {
		return "", errors.Wrap(err, "unable_to_count_import_notable_issues")
	}
	if notable > 0 {
		return model.ImportStateCompletedWithIssues, nil
	}
	return model.ImportStateCompleted, nil
}

// --- deterministic terminal rows ---

// importEntityIdentity is the subset of an entity needed to record its terminal outcome. Only these columns
// are read so terminalizing a five-thousand-page job never pulls its bodies into memory.
type importEntityIdentity struct {
	Ordinal       int
	ExternalId    string
	Title         string
	PlannedAction string
}

// recordMissingImportOutcomes gives every entity without an outcome the one its intent implies.
//
// The two intents need opposite treatment, which is why they are not one query. A failed or canceled job
// records not-attempted for whatever it never reached, and deliberately does *not* classify unprocessed
// mappings as stale: it never looked at them, so it has no grounds to say the source dropped them. A completed
// job has looked at everything, so mappings the bundle no longer contains genuinely are stale.
// Must be called inside tx, before the staged rows are released.
func (s *Store) recordMissingImportOutcomes(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	intent model.ImportTerminalIntent,
	errorCode string,
	now int64,
) error {
	if intent == model.ImportIntentCompleted {
		return s.recordCompletionStaleOutcomes(tx, job, now)
	}

	outcome := model.ImportOutcomeNotAttemptedCancel
	if intent == model.ImportIntentFailed {
		outcome = model.ImportOutcomeNotAttemptedFailure
	}
	details := mmmodel.StringInterface{"finished_from_state": string(job.State)}
	if errorCode != "" {
		details["reason"] = errorCode
	}
	detailsBytes, err := jsonByteLen(details)
	if err != nil {
		return err
	}

	// Staged pages first: their ordinals are the execution stage's own key range. The anti-join is what makes
	// terminalization idempotent — a page that already has an execution result keeps it, so a partially
	// executed job never has a real outcome overwritten by "not attempted".
	stagedPages := s.getQueryBuilder().
		Select("Ordinal", "ExternalId", "Title", "PlannedAction").
		From("DOCS_ImportStagedPage").
		Where(`NOT EXISTS (
			SELECT 1 FROM DOCS_ImportResult x
			WHERE x.JobId = DOCS_ImportStagedPage.JobId AND x.Stage = 'execution'
			  AND x.Ordinal = DOCS_ImportStagedPage.Ordinal
		)`)
	if _, err = s.recordOutcomesFrom(tx, job, stagedPages, outcome, details, detailsBytes, now); err != nil {
		return err
	}

	// Then anything preflight classified that has no staged page of its own — notably the stale mappings it
	// recorded, which would otherwise reach a terminal state with a plan and no outcome. Preflight's rows are
	// the authority for those: they are what it actually decided, rather than a guess reconstructed later.
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
	_, err = s.recordOutcomesFrom(tx, job, preflightOnly, outcome, details, detailsBytes, now)
	return err
}

// recordOutcomesFrom streams entity identities from one source query and writes one execution result each.
// source must select (Ordinal, ExternalId, Title, PlannedAction) and is filtered by job and paged on Ordinal
// here, so neither the read nor the write ever materializes the whole entity set.
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

		records := make([]*model.ImportResultRecord, 0, len(entities))
		for _, e := range entities {
			records = append(records, &model.ImportResultRecord{
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
			})
			job.RetainedBytes += retainedResultRowBytes(records[len(records)-1], detailsBytes)
			lastOrdinal = e.Ordinal
			written++
		}
		if err := s.insertImportResults(tx, records); err != nil {
			return written, err
		}
	}
}

// recordCompletionStaleOutcomes records every retained mapping the completed bundle no longer contains.
//
// Stale entries are reported, never deleted: a page that left the Confluence space is still a real local page
// someone may be using, and V1 has no mandate to remove it. Ordinals come from the sorted external ids so a
// retry derives exactly the same rows. Must be called inside tx, before the staged rows are released.
func (s *Store) recordCompletionStaleOutcomes(tx sqlx.ExtContext, job *model.ImportJob, now int64) error {
	if job.SelectedImportSourceId == "" {
		return nil
	}

	type staleMapping struct {
		ExternalId      string
		LocalId         string
		LastSourceTitle string
	}
	index := 0
	lastExternalID := ""
	for {
		var stale []staleMapping
		builder := s.getQueryBuilder().
			Select("e.ExternalId", "e.LocalId", "e.LastSourceTitle").
			From("DOCS_ImportEntity e").
			Where(sq.Eq{"e.ImportSourceId": job.SelectedImportSourceId, "e.EntityType": model.ImportEntityTypePage}).
			Where(sq.Gt{"e.ExternalId": lastExternalID}).
			Where(`NOT EXISTS (
				SELECT 1 FROM DOCS_ImportStagedPage p
				WHERE p.JobId = ? AND p.ExternalId = e.ExternalId
			)`, job.Id).
			OrderBy("e.ExternalId ASC")
		builder = applyLimitOffset(builder, 0, importRowBatchRows)
		if err := s.selectBuilder(tx, &stale, builder); err != nil {
			return errors.Wrap(err, "unable_to_list_stale_import_mappings")
		}
		if len(stale) == 0 {
			return nil
		}

		results := make([]*model.ImportResultRecord, 0, len(stale))
		issues := make([]*model.ImportIssueRecord, 0, len(stale))
		for _, m := range stale {
			if index >= model.ImportMaxMappingsPerSource {
				// The ordinal ranges are sized for the mapping cap. Past it, ordinals would run into the
				// job-level issue range, so the sweep stops and says so rather than colliding.
				return s.recordImportIssueTruncation(tx, job.Id, model.ImportStageExecution)
			}
			result := &model.ImportResultRecord{
				JobId:         job.Id,
				Stage:         model.ImportStageExecution,
				Ordinal:       model.ImportStaleOrdinalBase + index,
				EntityType:    model.ImportEntityTypePage,
				ExternalId:    m.ExternalId,
				LocalId:       m.LocalId,
				Title:         m.LastSourceTitle,
				PlannedAction: model.ImportActionStale,
				ActualAction:  model.ImportActionStale,
				Outcome:       model.ImportOutcomeStale,
				CreateAt:      now,
				UpdateAt:      now,
			}
			issue := &model.ImportIssueRecord{
				JobId:       job.Id,
				Stage:       model.ImportStageExecution,
				Ordinal:     model.ImportJobIssueOrdinalBase + index,
				Severity:    importer.IssueSeverity(importer.IssueSourcePageStale),
				Code:        importer.IssueSourcePageStale,
				EntityType:  model.ImportEntityTypePage,
				ExternalId:  m.ExternalId,
				LocalId:     m.LocalId,
				Title:       m.LastSourceTitle,
				Message:     importer.IssueMessage(importer.IssueSourcePageStale),
				Remediation: importer.IssueRemediation(importer.IssueSourcePageStale),
			}
			results = append(results, result)
			issues = append(issues, issue)
			job.RetainedBytes += retainedResultRowBytes(result, 0)
			issueCost := retainedIssueRowBytes(issue, 0)
			job.RetainedBytes += issueCost
			job.RetainedIssueBytes += issueCost
			lastExternalID = m.ExternalId
			index++
		}
		if err := s.insertImportResults(tx, results); err != nil {
			return err
		}
		if err := s.insertImportIssues(tx, issues); err != nil {
			return err
		}
	}
}

// composeImportFinalSummary builds the final summary from the durable outcomes, and reports whether the job
// committed any change to the page tree.
//
// It is rebuilt from result rows rather than accumulated in memory, so a job that finished across several
// worker passes and a restart reports exactly what a reader of those rows would count. Must be called inside
// tx, after every terminal row has been written.
func (s *Store) composeImportFinalSummary(tx sqlx.ExtContext, job *model.ImportJob) (model.ImportFinalSummary, bool, error) {
	summary := model.ImportFinalSummary{Manifest: job.BundleSummary.Counts}

	type outcomeTally struct {
		ActualAction string
		Outcome      string
		Cnt          int
	}
	var tallies []outcomeTally
	builder := s.getQueryBuilder().
		Select("ActualAction", "Outcome", "COUNT(*) AS cnt").
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": job.Id, "Stage": string(model.ImportStageExecution)}).
		GroupBy("ActualAction", "Outcome")
	if err := s.selectBuilder(tx, &tallies, builder); err != nil {
		return summary, false, errors.Wrap(err, "unable_to_aggregate_import_outcomes")
	}

	outcomes := map[string]int{}
	for _, t := range tallies {
		if t.Outcome != "" {
			outcomes[t.Outcome] += t.Cnt
		}
		switch model.ImportAction(t.ActualAction) {
		case model.ImportActionCreate:
			summary.Actions.Create += t.Cnt
		case model.ImportActionUpdate:
			summary.Actions.Update += t.Cnt
		case model.ImportActionNoop:
			summary.Actions.Noop += t.Cnt
		case model.ImportActionPreserveLocal:
			summary.Actions.PreserveLocal += t.Cnt
		case model.ImportActionConflict:
			summary.Actions.Conflict += t.Cnt
		case model.ImportActionBlocked:
			summary.Actions.Blocked += t.Cnt
		case model.ImportActionStale:
			summary.Actions.Stale += t.Cnt
		case model.ImportActionNotAttempted:
			summary.Actions.NotAttempted += t.Cnt
		}
	}
	if len(outcomes) > 0 {
		summary.Outcomes = outcomes
	}

	// Author counts come from result details rather than from issue rows: issues are discretionary and may be
	// truncated, so counting them would under-report fallbacks on exactly the largest imports.
	var fallbacks int
	fallbackBuilder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": job.Id, "Stage": string(model.ImportStageExecution)}).
		Where("Details->>'author_fallback' = 'true'")
	if err := s.getBuilder(tx, &fallbacks, fallbackBuilder); err != nil {
		return summary, false, errors.Wrap(err, "unable_to_count_import_author_fallbacks")
	}
	attributed := summary.Actions.Create
	summary.Authors.FallbackToActor = min(fallbacks, attributed)
	summary.Authors.Mapped = attributed - summary.Authors.FallbackToActor

	// A job that created or updated even one page has changed the tree, so clients must be told to refetch it
	// — including a job that then failed or was canceled. Partial work is still work.
	treeChanged := summary.Actions.Create+summary.Actions.Update > 0
	return summary, treeChanged, nil
}

// --- terminalization entry and invalidation ---

// enterImportTerminalizing records a terminal intent and moves the job into terminalizing. Must be called
// inside tx with the job row already locked.
func (s *Store) enterImportTerminalizing(
	tx sqlx.ExtContext,
	job *model.ImportJob,
	intent model.ImportTerminalIntent,
	errorCode string,
	now int64,
) error {
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateTerminalizing)).
		Set("TerminalIntent", string(intent)).
		Set("ErrorCode", errorCode).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": job.Id, "State": string(job.State)})
	if intent == model.ImportIntentCanceled {
		builder = builder.Set("CancelRequestedAt", now)
	}
	result, err := s.execBuilder(tx, builder)
	if err != nil {
		return errors.Wrap(err, "unable_to_enter_import_terminalizing")
	}
	if err = checkRowsAffected(result, "ImportJob", job.Id); err != nil {
		return err
	}
	job.State = model.ImportStateTerminalizing
	job.TerminalIntent = intent
	job.ErrorCode = errorCode
	job.UpdateAt = max(job.UpdateAt+1, now)
	if intent == model.ImportIntentCanceled {
		job.CancelRequestedAt = now
	}
	return nil
}

// GetImportJobsPendingInvalidation returns terminal jobs whose tree invalidation has not been published yet.
//
// The flag is cleared only after the event is published, so a crash between the two duplicates an idempotent
// invalidation rather than losing it — the direction that matters, since a lost invalidation leaves every
// client showing a stale page tree until something else happens to refresh it.
func (s *Store) GetImportJobsPendingInvalidation(limit int) ([]*model.ImportJob, error) {
	if err := requirePositiveLimit("ImportJob", limit); err != nil {
		return nil, err
	}
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"InvalidationPending": true}).
		OrderBy("UpdateAt ASC", "Id ASC")
	builder = applyLimitOffset(builder, 0, limit)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_list_import_jobs_pending_invalidation")
	}
	return jobs, nil
}

// GetImportJobsPendingCompensation returns terminal jobs that still own a channel attempt awaiting
// compensation, oldest first.
//
// These are the jobs retention deliberately refuses to delete, because the attempt row is the only pointer to
// a channel the import created and never cleaned up. Until something resolves them they hold that pointer and
// their retained reservation indefinitely, so they need a retry path rather than only a first attempt.
func (s *Store) GetImportJobsPendingCompensation(limit int) ([]*model.ImportJob, error) {
	if err := requirePositiveLimit("ImportJob", limit); err != nil {
		return nil, err
	}
	builder := s.importJobSelectQuery().
		Where(sq.Eq{"State": []string{
			string(model.ImportStateCompleted), string(model.ImportStateCompletedWithIssues),
			string(model.ImportStateFailed), string(model.ImportStateCanceled),
		}}).
		Where(sq.Expr(`EXISTS (
			SELECT 1 FROM DOCS_ImportChannelAttempt a
			WHERE a.JobId = DOCS_ImportJob.Id AND a.ChannelId <> '' AND a.State = ANY(?)
		)`, pq.Array(ImportUncompensatedAttemptStates))).
		OrderBy("FinishedAt ASC", "Id ASC")
	builder = applyLimitOffset(builder, 0, limit)

	jobs := []*model.ImportJob{}
	if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_list_import_jobs_pending_compensation")
	}
	return jobs, nil
}

// ResolveImportCompensationIssue rewrites one attempt's compensation finding from failed to compensated.
//
// The report is corrected in place rather than appended to. A compensation finding states what became of a
// specific channel, so once the channel is gone the accurate report says so; leaving a permanent "could not be
// removed" alongside a later success would send an operator hunting for something that no longer exists. The
// row is located by the attempt id in its details, because the finding's ordinal depends on the attempt
// ordering at terminalization and is not recomputable here.
func (s *Store) ResolveImportCompensationIssue(jobID, attemptID string) error {
	code := importer.IssueChannelCompensated
	builder := s.getQueryBuilder().
		Update("DOCS_ImportIssue").
		Set("Code", code).
		Set("Severity", string(importer.IssueSeverity(code))).
		Set("Message", importer.IssueMessage(code)).
		Set("Remediation", importer.IssueRemediation(code)).
		Set("Details", sq.Expr("Details - 'reason'")).
		Where(sq.Eq{
			"JobId": jobID,
			"Stage": string(model.ImportStageExecution),
			"Code":  importer.IssueChannelCompensationFailed,
		}).
		Where("Details->>'attempt_id' = ?", attemptID)
	if _, err := s.execBuilder(s.db, builder); err != nil {
		return errors.Wrap(err, "unable_to_resolve_import_compensation_issue")
	}
	return nil
}

// ClearImportInvalidationPending clears the flag after the invalidation event has been published.
func (s *Store) ClearImportInvalidationPending(jobID string) error {
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("InvalidationPending", false).
		Where(sq.Eq{"Id": jobID})
	if _, err := s.execBuilder(s.db, builder); err != nil {
		return errors.Wrap(err, "unable_to_clear_import_invalidation_pending")
	}
	return nil
}
