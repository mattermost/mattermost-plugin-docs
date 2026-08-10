// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	stderrors "errors"
	"net/http"
	"slices"

	"github.com/pkg/errors"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// Retention windows for import jobs.
const (
	// importBodyRetentionMillis is how long a terminal job keeps its staged page bodies. After this
	// they are purged and their staged capacity released; results, issues, manifest users, and
	// summaries stay so the report is still downloadable.
	importBodyRetentionMillis = int64(7 * 24 * 60 * 60 * 1000)
)

// Stable job-level error codes recorded on the job and surfaced through ImportJobView.Error.
const (
	// ImportErrorJobExpired marks a job canceled because nobody confirmed it inside the review window.
	ImportErrorJobExpired = "job_expired"
	// ImportErrorCanceledByUser marks a job the owning actor canceled explicitly.
	ImportErrorCanceledByUser = "canceled_by_user"
)

// CancelImportJob cancels the actor's own import job and releases the admission capacity it held.
//
// Cancellation exists as much for capacity as for user intent: admission bounds concurrent jobs and
// staged bytes per user, so without a way to give a job back an abandoned upload would keep consuming
// that budget until retention expired it.
func (s *Service) CancelImportJob(jobID, actorID string) (*model.ImportJobView, *mmmodel.AppError) {
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, appErr
	}
	if job.State.IsTerminal() {
		return nil, mmmodel.NewAppError("CancelImportJob", "app.import.cancel.already_terminal.app_error", nil, "", http.StatusConflict)
	}

	canceled, immediate, err := s.store.CancelImportJob(jobID, actorID, ImportErrorCanceledByUser)
	if err != nil {
		return nil, storeAppError("CancelImportJob", err)
	}

	if immediate {
		s.log.Info("Import canceled by user",
			"job_id", canceled.Id, "actor_id", actorID, "team_id", canceled.TeamId,
			"target_space_id", canceled.TargetSpaceId, "previous_state", string(job.State),
			"released_staged_bytes", job.StagedBytes,
			"released_retained_bytes", job.RetainedReservedBytes-canceled.RetainedReservedBytes,
			"not_attempted_pages", canceled.FinalSummary.Actions.NotAttempted)
	} else {
		// The job had reached a state that may already have written pages, so it goes to the terminalizer
		// rather than straight to canceled: committed work must be reconciled and reported, and an unattached
		// backing channel compensated, before any final report is published.
		s.log.Info("Import cancellation requested; the job is terminalizing",
			"job_id", canceled.Id, "actor_id", actorID, "team_id", canceled.TeamId,
			"target_space_id", canceled.TargetSpaceId, "previous_state", string(job.State))
	}

	// Owning a job authorizes cancelling it, but it does not authorize reading the target. An actor who
	// has lost Space access gets the same minimal projection GET returns — otherwise cancelling would be
	// a way to read back target- and source-identifying fields (notably the selected source's display
	// name, which is Space-side data the actor may never have seen) that GET deliberately withholds.
	//
	// A lookup failure redacts rather than errors: the cancel already committed, so failing the response
	// would report an error for work that succeeded. Failing closed on disclosure is the safe direction.
	entitled, entitlementErr := s.actorStillEntitled(canceled, actorID)
	if entitlementErr != nil {
		s.log.Warn("Import cancel: entitlement check failed, returning the minimal view",
			"job_id", canceled.Id, "actor_id", actorID, "err", entitlementErr)
		return minimalImportJobView(canceled), nil
	}
	if !entitled {
		return minimalImportJobView(canceled), nil
	}
	return buildImportJobViewWithoutCandidates(canceled), nil
}

// RunImportWork performs at most one unit of worker work and reports whether it found any.
//
// One unit at a time, with the next selection re-read from the database, is what makes the worker
// restartable: every transition is a compare-and-set, so a crash between units leaves a state the next
// pass can resume from rather than in-memory progress that is simply lost. The caller loops.
//
// V1 runs exactly one worker on one node. There is deliberately no lease, claim token, or heartbeat: with
// a single worker, losing a compare-and-set is the only contention that can occur, and it resolves by
// re-reading rather than by fencing.
func (s *Service) RunImportWork() (bool, error) {
	job, err := s.store.GetNextImportWork()
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	switch job.State {
	case model.ImportStateTerminalizing:
		return true, s.RunImportTerminalization(job.Id)
	case model.ImportStateQueuedImport, model.ImportStateImporting:
		// One call covers both: starting a confirmed job and resuming one interrupted mid-flight are the same
		// operation, because execution is driven entirely by which pages already have committed checkpoints.
		return true, s.RunImportExecution(job.Id)
	case model.ImportStateQueuedPreflight:
		return true, s.RunImportPreflight(job.Id)
	case model.ImportStatePreflighting:
		// A job found already preflighting was interrupted mid-computation, so nothing it derived can be
		// trusted. Returning it to the queue discards that partial work and the next pass recomputes from
		// scratch — which is safe precisely because preflight publishes all-or-nothing.
		s.log.Info("Import preflight interrupted; requeuing for recomputation", "job_id", job.Id)
		return true, s.requeueImportPreflight(job.Id)
	default:
		// Unreachable: work selection only offers states this release advances. Reaching it would mean a
		// state was added to the selection list without a handler, which starves everything below it — so it
		// is reported as the invariant violation it is rather than silently skipped.
		s.log.Error("Import worker selected a state it cannot advance; work below it is starved",
			"job_id", job.Id, "state", string(job.State))
		return false, nil
	}
}

// RunImportTerminalization finishes a job that has decided its outcome, writing the durable report and
// moving it to a terminal state.
//
// It is a worker step rather than part of the transition that decided the outcome so that a crash between
// the two resumes here instead of leaving a terminal job with no report. Every write is idempotent, so
// re-running it after an interruption completes the job rather than duplicating outcomes.
//
// Compensation runs first and outside the transaction, because archiving a channel is an external call. Its
// result is passed in so the report can state what happened either way: a channel that could not be archived
// becomes an operator-actionable finding rather than a silent orphan.
func (s *Service) RunImportTerminalization(jobID string) error {
	job, err := s.store.GetImportJob(jobID)
	if err != nil {
		if store.IsErrNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "load terminalizing import job")
	}
	compensations := s.compensateImportChannels(job)

	finished, err := s.store.TerminalizeImportJob(jobID, compensations)
	if err != nil {
		if store.IsErrConflict(err) || store.IsErrNotFound(err) {
			// The job advanced or vanished between selection and this transition; the CAS losing is the
			// intended outcome.
			s.log.Debug("Import terminalization skipped: job is no longer terminalizing", "job_id", jobID, "err", err)
			return nil
		}
		return errors.Wrap(err, "terminalize import job")
	}
	s.log.Info("Import terminalized",
		"job_id", finished.Id, "actor_id", finished.ActorId, "state", string(finished.State),
		"intent", string(finished.TerminalIntent), "error_code", finished.ErrorCode,
		"created", finished.FinalSummary.Actions.Create, "updated", finished.FinalSummary.Actions.Update,
		"noop", finished.FinalSummary.Actions.Noop, "preserved", finished.FinalSummary.Actions.PreserveLocal,
		"conflict", finished.FinalSummary.Actions.Conflict, "blocked", finished.FinalSummary.Actions.Blocked,
		"stale", finished.FinalSummary.Actions.Stale,
		"not_attempted", finished.FinalSummary.Actions.NotAttempted,
		"compensated_channels", len(compensations))

	s.publishImportJobUpdate(finished)
	s.publishPendingImportInvalidation(finished)
	return nil
}

// compensateImportChannels archives backing channels created for a Space that never came into existence.
//
// It only ever runs when there is no Space row: once one exists the channel is real, user-visible content and
// must be preserved and reported rather than cleaned up. Resolution is by channel id, not by name, because a
// Space channel cannot be found by name; already archived or absent counts as compensated, which is what makes
// a retry after a crash produce the same finding rather than a new failure.
func (s *Service) compensateImportChannels(job *model.ImportJob) []store.ImportCompensation {
	if job.TargetSpaceExisted {
		return nil
	}
	if _, err := s.store.GetSpace(job.TargetSpaceId, true); err == nil {
		return nil
	} else if !store.IsErrNotFound(err) {
		// Not knowing whether the Space exists is not grounds to archive its channel. Leaving the attempt rows
		// alone keeps them visible to the next pass, which is safer than deleting content on a failed read.
		s.log.Warn("Could not determine whether the import Space exists; skipping channel compensation",
			"job_id", job.Id, "target_space_id", job.TargetSpaceId, "err", err)
		return nil
	}

	attempts, err := s.store.GetImportChannelAttempts(job.Id)
	if err != nil {
		s.log.Warn("Could not load import channel attempts for compensation", "job_id", job.Id, "err", err)
		return nil
	}

	compensations := make([]store.ImportCompensation, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.ChannelId == "" {
			// No id was ever recorded, so there is nothing this pass can resolve. The attempt row remains as the
			// only trace of a call whose result was lost.
			continue
		}
		if attempt.State != model.ImportChannelProvisioned && attempt.State != model.ImportChannelPendingCompensation {
			continue
		}
		result := s.archiveImportChannelAttempt(job, attempt)
		compensations = append(compensations, result)

		state := model.ImportChannelCompensated
		errorCode := ""
		if !result.Resolved {
			state = model.ImportChannelPendingCompensation
			errorCode = result.Reason
		}
		if stateErr := s.store.SetImportChannelAttemptState(job.Id, attempt.AttemptId, state, errorCode); stateErr != nil {
			s.log.Warn("Could not record import channel compensation state",
				"job_id", job.Id, "attempt_id", attempt.AttemptId, "err", stateErr)
		}
	}
	return compensations
}

// archiveImportChannelAttempt archives one attempt's channel, treating an already-gone channel as success.
//
// Only a definitive answer counts as resolved. A lookup that merely *failed* says nothing about whether the
// channel is still there, and treating it as success would let the job publish "this channel was cleaned up"
// while a live orphan remained — the exact claim the compensation record exists to make honestly.
func (s *Service) archiveImportChannelAttempt(job *model.ImportJob, attempt *model.ImportChannelAttempt) store.ImportCompensation {
	result := store.ImportCompensation{AttemptID: attempt.AttemptId, ChannelID: attempt.ChannelId}
	if s.client == nil {
		result.Reason = "plugin_client_unavailable"
		return result
	}

	channel, err := s.client.Channel.GetChannelOfType(attempt.ChannelId, mmmodel.ChannelTypeSpace)
	switch {
	case stderrors.Is(err, pluginapi.ErrNotFound), err == nil && channel == nil:
		// Genuinely absent is the successful end state: there is nothing left to clean up.
		result.Resolved = true
		return result
	case err != nil:
		s.log.Warn("Could not determine whether an import's orphaned channel still exists; leaving it for a later pass",
			"job_id", job.Id, "attempt_id", attempt.AttemptId, "channel_id", attempt.ChannelId, "err", err)
		result.Reason = "lookup_failed"
		return result
	}
	if channel.DeleteAt != 0 {
		result.Resolved = true
		return result
	}
	if err = s.client.Channel.Delete(attempt.ChannelId); err != nil {
		s.log.Error("Could not archive the channel created for an import Space that was never provisioned; it must be archived manually",
			"job_id", job.Id, "attempt_id", attempt.AttemptId, "channel_id", attempt.ChannelId, "err", err)
		result.Reason = "archive_failed"
		return result
	}
	result.Resolved = true
	return result
}

// ReconcileImportCompensations retries the channel archives that failed while their jobs were terminalizing,
// and returns how many it resolved.
//
// Without this a job whose archive failed once is stuck forever: it is already terminal, so it never re-enters
// terminalizing, and retention deliberately refuses to delete it while a pending attempt row is the only
// pointer to the orphaned channel. That is two leaks from one transient failure — the channel and the job's
// retained reservation — neither of which anything else would ever clear.
//
// The report is corrected rather than appended to. A compensation finding is a statement about the channel's
// current state, so once the channel is gone the honest report says compensated; leaving a permanent "could not
// be removed" would send an operator looking for something that is no longer there.
func (s *Service) ReconcileImportCompensations() int {
	jobs, err := s.store.GetImportJobsPendingCompensation(importCompensationBatch)
	if err != nil {
		s.log.Warn("Could not scan for import channel compensations to retry", "err", err)
		return 0
	}

	resolved := 0
	for _, job := range jobs {
		attempts, attemptErr := s.store.GetImportChannelAttempts(job.Id)
		if attemptErr != nil {
			s.log.Warn("Could not load import channel attempts to retry", "job_id", job.Id, "err", attemptErr)
			continue
		}
		for _, attempt := range attempts {
			// provisioned counts too: on a terminal job it means the attempt was never attached, which is what a
			// compensation pass whose own state write failed leaves behind.
			if attempt.ChannelId == "" || !slices.Contains(store.ImportUncompensatedAttemptStates, string(attempt.State)) {
				continue
			}
			result := s.archiveImportChannelAttempt(job, attempt)
			if !result.Resolved {
				continue
			}
			if stateErr := s.store.SetImportChannelAttemptState(
				job.Id, attempt.AttemptId, model.ImportChannelCompensated, ""); stateErr != nil {
				s.log.Warn("Could not record a retried import channel compensation",
					"job_id", job.Id, "attempt_id", attempt.AttemptId, "err", stateErr)
				continue
			}
			if issueErr := s.store.ResolveImportCompensationIssue(job.Id, attempt.AttemptId); issueErr != nil {
				s.log.Warn("Could not correct the compensation finding in an import report",
					"job_id", job.Id, "attempt_id", attempt.AttemptId, "err", issueErr)
			}
			s.log.Info("Archived an import's orphaned channel on a later pass",
				"job_id", job.Id, "attempt_id", attempt.AttemptId, "channel_id", attempt.ChannelId)
			resolved++
		}
	}
	return resolved
}

// importCompensationBatch bounds one compensation-retry sweep.
const importCompensationBatch = 50

// PublishPendingImportInvalidations publishes any tree invalidation a terminal job still owes.
//
// The normal path publishes immediately after terminalization; this is the recovery path for a crash between
// the terminal commit and that publish. Duplicating an idempotent invalidation is harmless, while losing one
// leaves every client showing a page tree that no longer matches the database.
func (s *Service) PublishPendingImportInvalidations() int {
	jobs, err := s.store.GetImportJobsPendingInvalidation(importInvalidationBatch)
	if err != nil {
		s.log.Warn("Could not scan for pending import invalidations", "err", err)
		return 0
	}
	published := 0
	for _, job := range jobs {
		if s.publishPendingImportInvalidation(job) {
			published++
		}
	}
	return published
}

// importInvalidationBatch bounds one pending-invalidation sweep.
const importInvalidationBatch = 50

// publishPendingImportInvalidation publishes one job's tree invalidation and then clears the flag, reporting
// whether it published. The order matters: clearing first would lose the invalidation on a crash in between,
// while publishing first can only repeat it.
func (s *Service) publishPendingImportInvalidation(job *model.ImportJob) bool {
	if !job.InvalidationPending {
		return false
	}
	space, err := s.store.GetSpace(job.TargetSpaceId, true)
	if err != nil {
		s.log.Warn("Could not resolve the imported Space to publish its tree invalidation",
			"job_id", job.Id, "target_space_id", job.TargetSpaceId, "err", err)
		return false
	}
	// Channel-scoped and carrying only the Space id: the event's audience is everyone who can read the Space,
	// and job or source detail would disclose the import to members who never initiated it.
	s.publishToChannels(wsEventSpaceImported, map[string]any{"space_id": space.Id}, space.ChannelId)

	if err = s.store.ClearImportInvalidationPending(job.Id); err != nil {
		s.log.Warn("Could not clear the import invalidation flag; it will be republished",
			"job_id", job.Id, "err", err)
	}
	return true
}

// publishImportJobUpdate sends the actor-scoped progress event. Job and source detail belong here rather
// than on the channel-scoped invalidation, because only the actor is entitled to see it.
func (s *Service) publishImportJobUpdate(job *model.ImportJob) {
	s.publishToUser(wsEventImportJobUpdated, map[string]any{
		"job_id":           job.Id,
		"state":            string(job.State),
		"phase":            string(job.Phase),
		"progress_current": job.ProgressCurrent,
		"progress_total":   job.ProgressTotal,
	}, job.ActorId)
}

// LogImportWorkerInvariants checks the single-active-job invariant the supported topology relies on and
// reports a violation. It is a diagnostic, not a guard: immutable execution checkpoints and mapping
// revisions are what actually prevent duplicate decisions, so a second active job is processed
// deterministically rather than refused.
func (s *Service) LogImportWorkerInvariants() {
	active, err := s.store.CountActiveImportJobs()
	if err != nil {
		s.log.Warn("Could not check the import worker invariant", "err", err)
		return
	}
	if active > 1 {
		s.log.Error("More than one import job is in a worker-owned state; V1 supports a single importer worker on a single node",
			"active_jobs", active)
	}
}

// RunImportMaintenance performs one hourly maintenance pass: expire stalled pre-execution jobs, purge
// staged bodies of terminal jobs, and delete jobs past their retention. Each step releases the
// admission capacity its jobs were holding, which is what keeps the per-user and global budgets from
// filling up permanently with abandoned work.
//
// It is safe to run repeatedly and does bounded work per pass (see importCleanupBatch), so a large
// backlog drains over successive runs rather than in one long transaction.
func (s *Service) RunImportMaintenance() (store.ImportCleanupCounts, error) {
	now := mmmodel.GetMillis()
	var counts store.ImportCleanupCounts

	// Publish first: an invalidation a crash left owed is the one piece of maintenance whose delay users can
	// see, because until it goes out every client shows a page tree the import has already changed.
	counts.PublishedInvalidations = s.PublishPendingImportInvalidations()
	// Then retry compensations, before the retention sweep below: a job it resolves becomes deletable in the
	// same pass rather than waiting an hour for the next one.
	counts.ResolvedCompensations = s.ReconcileImportCompensations()

	expired, releasedExpired, releasedRetained, err := s.store.ExpireStalledImportJobs(now, ImportErrorJobExpired)
	counts.ExpiredJobs = expired
	counts.ReleasedStagedBytes += releasedExpired
	counts.ReleasedRetainedBytes += releasedRetained
	if err != nil {
		return counts, err
	}

	purged, releasedPurged, err := s.store.PurgeTerminalImportStagedBodies(now-importBodyRetentionMillis, now)
	counts.PurgedStagedJobs = purged
	counts.ReleasedStagedBytes += releasedPurged
	if err != nil {
		return counts, err
	}

	deleted, keptForCompensation, err := s.store.DeleteExpiredImportJobs(now)
	counts.DeletedJobs = deleted
	counts.KeptForCompensationJobs = keptForCompensation
	if err != nil {
		return counts, err
	}
	return counts, nil
}

// LogImportMaintenance writes the operator-facing sweep line, but only when the pass actually did
// something, so an idle server does not emit an hourly no-op.
func (s *Service) LogImportMaintenance(counts store.ImportCleanupCounts, err error) {
	if err != nil {
		s.log.Error("Import maintenance pass failed",
			"expired_jobs", counts.ExpiredJobs, "purged_staged_jobs", counts.PurgedStagedJobs,
			"deleted_jobs", counts.DeletedJobs, "kept_for_compensation_jobs", counts.KeptForCompensationJobs,
			"released_staged_bytes", counts.ReleasedStagedBytes,
			"published_invalidations", counts.PublishedInvalidations,
			"resolved_compensations", counts.ResolvedCompensations,
			"released_retained_bytes", counts.ReleasedRetainedBytes, "err", err)
		return
	}
	if counts.ExpiredJobs == 0 && counts.PurgedStagedJobs == 0 && counts.DeletedJobs == 0 &&
		counts.KeptForCompensationJobs == 0 && counts.PublishedInvalidations == 0 &&
		counts.ResolvedCompensations == 0 {
		return
	}
	s.log.Info("Import maintenance pass completed",
		"expired_jobs", counts.ExpiredJobs, "purged_staged_jobs", counts.PurgedStagedJobs,
		"deleted_jobs", counts.DeletedJobs, "kept_for_compensation_jobs", counts.KeptForCompensationJobs,
		"published_invalidations", counts.PublishedInvalidations,
		"resolved_compensations", counts.ResolvedCompensations,
		"released_staged_bytes", counts.ReleasedStagedBytes,
		"released_retained_bytes", counts.ReleasedRetainedBytes)
}
