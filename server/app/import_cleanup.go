// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	"github.com/pkg/errors"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

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

	canceled, err := s.store.CancelImportJob(jobID, actorID, ImportErrorCanceledByUser)
	if err != nil {
		return nil, storeAppError("CancelImportJob", err)
	}

	s.log.Info("Import canceled by user",
		"job_id", canceled.Id, "actor_id", actorID, "team_id", canceled.TeamId,
		"target_space_id", canceled.TargetSpaceId, "previous_state", string(job.State),
		"released_staged_bytes", job.StagedBytes,
		"released_retained_bytes", job.RetainedReservedBytes-canceled.RetainedReservedBytes,
		"not_attempted_pages", canceled.FinalSummary.Actions.NotAttempted)

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
// the two resumes here instead of leaving a terminal job with no report. The store call is idempotent, so
// re-running it after an interruption completes the job rather than duplicating outcomes.
func (s *Service) RunImportTerminalization(jobID string) error {
	finished, err := s.store.TerminalizeImportJob(jobID)
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
		"not_attempted", finished.FinalSummary.Actions.NotAttempted)
	return nil
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
			"released_retained_bytes", counts.ReleasedRetainedBytes, "err", err)
		return
	}
	if counts.ExpiredJobs == 0 && counts.PurgedStagedJobs == 0 && counts.DeletedJobs == 0 &&
		counts.KeptForCompensationJobs == 0 {
		return
	}
	s.log.Info("Import maintenance pass completed",
		"expired_jobs", counts.ExpiredJobs, "purged_staged_jobs", counts.PurgedStagedJobs,
		"deleted_jobs", counts.DeletedJobs, "kept_for_compensation_jobs", counts.KeptForCompensationJobs,
		"released_staged_bytes", counts.ReleasedStagedBytes,
		"released_retained_bytes", counts.ReleasedRetainedBytes)
}
