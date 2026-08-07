// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// Execution loop bounds.
const (
	// importExecutionBatch is how many staged ordinals one pass loads at a time. Each ordinal costs one short
	// transaction, so this only bounds the ordinal list.
	importExecutionBatch = 200
	// importReauthorizeEvery is how many pages may be written between authorization rechecks. Rechecking per
	// page would add two membership lookups to every page transaction; rechecking never would let a
	// deactivated user's import keep writing for the length of a five-thousand-page bundle.
	importReauthorizeEvery = 100
	// importProgressEvery is how many pages may be applied between progress publications.
	importProgressEvery = 25
)

// Stable job-level error codes recorded when execution cannot continue.
const (
	// ImportErrorAuthorizationRevoked marks a job stopped because the actor lost the access that authorized it.
	ImportErrorAuthorizationRevoked = "authorization_revoked"
	// ImportErrorProvisioningFailed marks a job that could not stand up its target Space.
	ImportErrorProvisioningFailed = "provisioning_failed"
	// ImportErrorExecutionFailed marks a job that failed while writing pages.
	ImportErrorExecutionFailed = "execution_failed"
)

// RunImportExecution applies one confirmed job's pages, or resumes one that was interrupted.
//
// Starting and resuming are the same code path on purpose: what has already been done is read from the
// committed execution checkpoints rather than remembered, so a process that dies halfway through picks up
// exactly where it left off with no special recovery logic to get wrong.
//
// It always ends by handing the job to terminalization rather than writing a terminal state itself. That is
// what guarantees a durable report exists before the job is reported finished, including when it stops early.
func (s *Service) RunImportExecution(jobID string) error {
	job, err := s.store.BeginImportExecution(jobID)
	if err != nil {
		switch {
		case store.IsErrPreflightStale(err):
			// The mappings moved between confirmation and now, so the approved plan no longer describes the
			// source. The job is already back in the preflight queue; the user reviews and confirms again.
			s.log.Info("Import execution deferred: the confirmed plan is stale and is being recomputed", "job_id", jobID)
			return nil
		case store.IsErrImportSourceMissing(err):
			return s.failImportJob(jobID, ImportErrorSourceMissing, err)
		case store.IsErrConflict(err), store.IsErrNotFound(err):
			// The job was canceled or advanced between work selection and this transition; losing the CAS is
			// the intended outcome.
			s.log.Debug("Import execution skipped: job is no longer executable", "job_id", jobID, "err", err)
			return nil
		}
		return errors.Wrap(err, "begin import execution")
	}

	// Authorization is rechecked before anything is written, not merely at confirmation: minutes or a restart
	// may have passed, and an import must never write into a Space its actor can no longer reach.
	if appErr := s.requireImportTargetStillAuthorized(job, job.ActorId); appErr != nil {
		return s.failImportJob(job.Id, ImportErrorAuthorizationRevoked, appErr)
	}

	provisioned, err := s.provisionImportTarget(job)
	if err != nil {
		// Only a conflict means the job itself moved on — cancellation, or a state this pass no longer owns —
		// and is therefore safe to skip. Every other failure leaves the job in importing, where skipping it would
		// have the worker re-select the same job on every pass and starve every job behind it, so it is failed
		// and reported instead. No current provisioning error reaches here as a not-found (the authorization
		// recheck above catches a deleted target Space first), but treating "unrecognized" as skippable is how
		// that starvation loop has been introduced twice already.
		if store.IsErrConflict(err) {
			s.log.Debug("Import provisioning skipped: the job left the importing state", "job_id", job.Id, "err", err)
			return nil
		}
		return s.failImportJob(job.Id, ImportErrorProvisioningFailed, err)
	}
	job = provisioned

	stopped, err := s.writeImportedPages(job)
	if err != nil {
		return s.failImportJob(job.Id, ImportErrorExecutionFailed, err)
	}
	if stopped {
		// The job left the importing state under us — cancellation, or a failure already recorded. Whoever
		// changed it owns the outcome, so this pass adds nothing.
		return nil
	}

	if _, err = s.store.EnterImportTerminalizing(job.Id, model.ImportIntentCompleted, ""); err != nil {
		if store.IsErrConflict(err) || store.IsErrNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "terminalize completed import job")
	}
	return nil
}

// writeImportedPages walks the job's staged pages in ordinal order, applying each in its own transaction.
//
// Ordinal order is parents-before-children, which the producer guarantees and inspection verifies. That is
// what makes a single forward pass sufficient: by the time a child is reached, its parent has either committed
// a mapping or has not, and either answer is durable.
//
// It reports stopped=true when the job left the importing state, which is how cancellation takes effect: the
// page transaction's own state guard refuses, and the loop returns without inventing an outcome.
func (s *Service) writeImportedPages(job *model.ImportJob) (bool, error) {
	authors := newImportAuthorRevalidator(s, job)
	sinceRecheck, sinceProgress := 0, 0
	afterOrdinal := -1

	for {
		ordinals, err := s.store.GetImportStagedOrdinals(job.Id, afterOrdinal, importExecutionBatch)
		if err != nil {
			return false, errors.Wrap(err, "list staged ordinals")
		}
		if len(ordinals) == 0 {
			s.flushImportProgress(job)
			return false, nil
		}

		for _, ordinal := range ordinals {
			if sinceRecheck >= importReauthorizeEvery {
				if appErr := s.requireImportTargetStillAuthorized(job, job.ActorId); appErr != nil {
					// Committed pages stay: they are real content the actor was entitled to create at the time.
					// The job fails so the rest is reported as not attempted rather than silently abandoned.
					return true, s.failImportJob(job.Id, ImportErrorAuthorizationRevoked, appErr)
				}
				sinceRecheck = 0
			}

			outcome, err := s.applyOneImportedPage(job, ordinal, authors)
			if err != nil {
				if store.IsErrConflict(err) {
					// The job is no longer importing. Cancellation and failure both arrive this way, and both
					// mean the terminalizer owns the rest.
					s.log.Info("Import execution stopped: the job left the importing state",
						"job_id", job.Id, "ordinal", ordinal)
					return true, nil
				}
				return false, err
			}
			afterOrdinal = ordinal
			if !outcome.Replayed {
				sinceRecheck++
				sinceProgress++
			}
			if sinceProgress >= importProgressEvery {
				s.flushImportProgress(job)
				sinceProgress = 0
			}
		}
	}
}

// applyOneImportedPage resolves the page's author and applies it.
func (s *Service) applyOneImportedPage(
	job *model.ImportJob,
	ordinal int,
	authors *importAuthorRevalidator,
) (*store.ImportPageOutcome, error) {
	staged, err := s.store.GetImportStagedPageSummary(job.Id, ordinal)
	if err != nil {
		return nil, errors.Wrap(err, "read staged page summary")
	}
	author := authors.revalidate(staged.ResolvedUserId, staged.AuthorFallbackReason)

	outcome, err := s.store.ApplyImportedPage(store.ImportPageExecution{
		JobID:                job.Id,
		Ordinal:              ordinal,
		ResolvedUserID:       author.userID,
		AuthorFallbackReason: author.reason,
		AuthorProposal: importer.EffectiveAuthorProposal(
			authors.manifestProposal(staged.SourceAuthorAccountId), staged.SourceUserProposal),
		Now:               mmmodel.GetMillis(),
		ApprovedOverwrite: authors.approvedOverwrite(staged.ExternalId),
	})
	if err != nil {
		return nil, err
	}
	s.log.Debug("Import applied a page",
		"job_id", job.Id, "ordinal", outcome.Ordinal, "external_id", outcome.ExternalID,
		"planned", string(outcome.PlannedAction), "actual", string(outcome.ActualAction),
		"outcome", string(outcome.Outcome), "replayed", outcome.Replayed)
	return outcome, nil
}

// flushImportProgress republishes progress from the committed checkpoint count and notifies the actor.
// Progress is cosmetic, so a failure here is logged rather than allowed to stop an import.
func (s *Service) flushImportProgress(job *model.ImportJob) {
	current, err := s.store.SetImportExecutionProgress(job.Id)
	if err != nil {
		s.log.Warn("Could not publish import progress", "job_id", job.Id, "err", err)
		return
	}
	job.ProgressCurrent = current
	s.publishImportJobUpdate(job)
}

// importAuthorRevalidator re-checks the authors preflight resolved, immediately before their pages are
// written, and caches each answer.
//
// Preflight may have resolved an author minutes ago, and attribution is written once and kept: a page
// attributed to an account that has since been deactivated would name someone who can no longer be a
// Mattermost user at all. The fallback is the importing actor, with the reason recorded so the report can say
// why the attribution is not the Confluence one.
type importAuthorRevalidator struct {
	svc *Service
	job *model.ImportJob
	// checked caches one answer per resolved user id: a space with thousands of pages typically has a handful
	// of authors, and each distinct one is worth exactly one lookup.
	checked map[string]resolvedAuthor
	// proposals is the durable manifest mapping, loaded once. Reading the persisted rows rather than any
	// in-memory manifest is what makes a restart hash and attribute identically to the original pass.
	proposals map[string]string
	// approved is the set of external ids the confirmation approved for overwrite.
	approved map[string]struct{}
}

// newImportAuthorRevalidator loads the durable inputs author revalidation and overwrite approval need.
func newImportAuthorRevalidator(svc *Service, job *model.ImportJob) *importAuthorRevalidator {
	r := &importAuthorRevalidator{
		svc:       svc,
		job:       job,
		checked:   map[string]resolvedAuthor{},
		proposals: map[string]string{},
		approved:  map[string]struct{}{},
	}
	users, err := svc.store.GetImportManifestUsers(job.Id)
	if err != nil {
		// Without the manifest every page falls back to its own user field, which is the same behaviour as a
		// bundle that carried no manifest at all. Failing the import over it would be worse than degrading.
		svc.log.Warn("Could not load manifest users for import execution; falling back to page author fields",
			"job_id", job.Id, "err", err)
	}
	for _, u := range users {
		r.proposals[u.AccountId] = u.MattermostUsername
	}
	for _, externalID := range job.Confirmation.OverwriteConflicts {
		r.approved[externalID] = struct{}{}
	}
	return r
}

// manifestProposal returns the manifest's username proposal for a Confluence account, or "".
func (r *importAuthorRevalidator) manifestProposal(accountID string) string {
	return r.proposals[accountID]
}

// approvedOverwrite reports whether the confirmation approved overwriting this page's local edits.
func (r *importAuthorRevalidator) approvedOverwrite(externalID string) bool {
	_, ok := r.approved[externalID]
	return ok
}

// revalidate confirms a preflight-resolved author is still usable, falling back to the actor if not.
func (r *importAuthorRevalidator) revalidate(resolvedUserID, fallbackReason string) resolvedAuthor {
	if fallbackReason != "" || resolvedUserID == "" {
		// Preflight already fell back, and nothing about execution can improve on that: re-resolving here would
		// make attribution depend on when the worker happened to run.
		return resolvedAuthor{userID: r.job.ActorId, reason: orFallbackReason(fallbackReason)}
	}
	if cached, ok := r.checked[resolvedUserID]; ok {
		return cached
	}

	result := resolvedAuthor{userID: resolvedUserID}
	if r.svc.client == nil {
		result = resolvedAuthor{userID: r.job.ActorId, reason: model.ImportFallbackUserNotFound}
	} else if user, err := r.svc.client.User.Get(resolvedUserID); err != nil || user == nil {
		result = resolvedAuthor{userID: r.job.ActorId, reason: model.ImportFallbackUserNotFound}
	} else if user.DeleteAt != 0 {
		result = resolvedAuthor{userID: r.job.ActorId, reason: model.ImportFallbackUserInactive}
	}
	r.checked[resolvedUserID] = result
	return result
}

// orFallbackReason keeps a recorded fallback reason, or names the generic one when preflight left it empty.
func orFallbackReason(reason string) string {
	if reason != "" {
		return reason
	}
	return model.ImportFallbackSourceAuthorMissing
}
