// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

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
