// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	stderrors "errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

// errImportRetryable marks a failure that says nothing about whether the import *should* proceed — a lookup
// that timed out, a backend that was briefly unavailable — as opposed to one that decides it must not.
//
// The distinction matters because the two need opposite handling. A definitive answer means the job is over and
// must be reported. An inconclusive one means try again: failing the job would destroy an import, possibly
// half-written, over a blip, and would label it with a reason that is not true. A retryable failure leaves the
// job in importing, which the worker re-enters on its next pass.
type errImportRetryable struct{ cause error }

func (e *errImportRetryable) Error() string { return "retryable import failure: " + e.cause.Error() }
func (e *errImportRetryable) Unwrap() error { return e.cause }

// retryableImportError wraps cause as retryable.
func retryableImportError(cause error) error {
	return &errImportRetryable{cause: cause}
}

// isRetryableImportError reports whether err is an inconclusive failure the worker should retry.
func isRetryableImportError(err error) bool {
	var e *errImportRetryable
	return stderrors.As(err, &e)
}

// importAuthorizationDenied reports whether an authorization failure is a genuine denial rather than an
// inconclusive lookup.
//
// requireImportTargetStillAuthorized returns 403 and 404 for real answers — the actor is inactive, not a
// member, lacks the permission — and 500 when it could not find out. Treating the second as revocation would
// let a transient outage terminate imports and tell their owners they had lost access they still have.
func importAuthorizationDenied(appErr *mmmodel.AppError) bool {
	return appErr.StatusCode == http.StatusForbidden || appErr.StatusCode == http.StatusNotFound
}

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
		if !importAuthorizationDenied(appErr) {
			s.log.Warn("Could not confirm import authorization; leaving the job for a later pass",
				"job_id", job.Id, "err", appErr)
			return retryableImportError(appErr)
		}
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
		if isRetryableImportError(err) {
			return err
		}
		return s.failImportJob(job.Id, ImportErrorProvisioningFailed, err)
	}
	job = provisioned

	stopped, err := s.writeImportedPages(job)
	if err != nil {
		if isRetryableImportError(err) {
			return err
		}
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
	authors, err := newImportAuthorRevalidator(s, job)
	if err != nil {
		return false, err
	}
	sinceRecheck, sinceProgress := 0, 0
	afterOrdinal := -1

	for {
		ordinals, ordErr := s.store.GetImportStagedOrdinals(job.Id, afterOrdinal, importExecutionBatch)
		if ordErr != nil {
			return false, errors.Wrap(ordErr, "list staged ordinals")
		}
		if len(ordinals) == 0 {
			s.flushImportProgress(job)
			return false, nil
		}

		for _, ordinal := range ordinals {
			if sinceRecheck >= importReauthorizeEvery {
				if appErr := s.requireImportTargetStillAuthorized(job, job.ActorId); appErr != nil {
					if !importAuthorizationDenied(appErr) {
						// Inconclusive: stop writing, but leave the job importing so a later pass resumes from its
						// checkpoints. Failing here would abandon a half-written import over a transient lookup.
						return false, retryableImportError(appErr)
					}
					// Committed pages stay: they are real content the actor was entitled to create at the time.
					// The job fails so the rest is reported as not attempted rather than silently abandoned.
					return true, s.failImportJob(job.Id, ImportErrorAuthorizationRevoked, appErr)
				}
				sinceRecheck = 0
			}

			outcome, applyErr := s.applyOneImportedPage(job, ordinal, authors)
			if applyErr != nil {
				if store.IsErrConflict(applyErr) {
					// The job is no longer importing. Cancellation and failure both arrive this way, and both
					// mean the terminalizer owns the rest.
					s.log.Info("Import execution stopped: the job left the importing state",
						"job_id", job.Id, "ordinal", ordinal)
					return true, nil
				}
				return false, applyErr
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
	author, err := authors.revalidate(staged.ResolvedUserId, staged.AuthorFallbackReason)
	if err != nil {
		return nil, err
	}

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
//
// A manifest that cannot be read is a retryable failure rather than something to proceed without. Proceeding
// would silently attribute every page to its own user field instead of the manifest's mapping, writing an
// author into the page's props that differs from the one the reviewed plan hashed — a quiet, permanent
// divergence from what the user approved, produced by a transient read.
func newImportAuthorRevalidator(svc *Service, job *model.ImportJob) (*importAuthorRevalidator, error) {
	r := &importAuthorRevalidator{
		svc:       svc,
		job:       job,
		checked:   map[string]resolvedAuthor{},
		proposals: map[string]string{},
		approved:  map[string]struct{}{},
	}
	users, err := svc.store.GetImportManifestUsers(job.Id)
	if err != nil {
		return nil, retryableImportError(errors.Wrap(err, "load manifest users for import execution"))
	}
	for _, u := range users {
		r.proposals[u.AccountId] = u.MattermostUsername
	}
	for _, externalID := range job.Confirmation.OverwriteConflicts {
		r.approved[externalID] = struct{}{}
	}
	return r, nil
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
//
// Only a definitive answer justifies a fallback. Attribution is written once and kept, so treating a failed
// lookup as "this user does not exist" would permanently credit someone else's page to the importing actor
// because a request happened to time out. An inconclusive lookup is retryable instead, and the page is not
// applied until the question can be answered.
func (r *importAuthorRevalidator) revalidate(resolvedUserID, fallbackReason string) (resolvedAuthor, error) {
	if fallbackReason != "" || resolvedUserID == "" {
		// Preflight already fell back, and nothing about execution can improve on that: re-resolving here would
		// make attribution depend on when the worker happened to run.
		return resolvedAuthor{userID: r.job.ActorId, reason: orFallbackReason(fallbackReason)}, nil
	}
	if cached, ok := r.checked[resolvedUserID]; ok {
		return cached, nil
	}
	if r.svc.client == nil {
		return resolvedAuthor{}, retryableImportError(errors.New("plugin client unavailable for author revalidation"))
	}

	result := resolvedAuthor{userID: resolvedUserID}
	user, err := r.svc.client.User.Get(resolvedUserID)
	switch {
	case stderrors.Is(err, pluginapi.ErrNotFound), err == nil && user == nil:
		result = resolvedAuthor{userID: r.job.ActorId, reason: model.ImportFallbackUserNotFound}
	case err != nil:
		return resolvedAuthor{}, retryableImportError(errors.Wrapf(err, "revalidate import author %s", resolvedUserID))
	case user.DeleteAt != 0:
		result = resolvedAuthor{userID: r.job.ActorId, reason: model.ImportFallbackUserInactive}
	}
	r.checked[resolvedUserID] = result
	return result, nil
}

// orFallbackReason keeps a recorded fallback reason, or names the generic one when preflight left it empty.
func orFallbackReason(reason string) string {
	if reason != "" {
		return reason
	}
	return model.ImportFallbackSourceAuthorMissing
}
