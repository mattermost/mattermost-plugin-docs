// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	stderrors "errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// SelectImportSource records the actor's explicit ImportSource choice for a job awaiting one, and queues
// it for preflight.
//
// Selection is always explicit. Two Confluence instances can share an organization id, a space key, and a
// display name while being genuinely different sources, so an automatic match could silently merge two
// unrelated page histories into one mapping set — which is why candidate scores only order suggestions.
func (s *Service) SelectImportSource(jobID, actorID string, req model.ImportSourceSelectionRequest) (*model.ImportJobView, *mmmodel.AppError) {
	if appErr := req.IsValid(); appErr != nil {
		return nil, appErr
	}
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, appErr
	}
	if job.State != model.ImportStateAwaitingSource {
		return nil, mmmodel.NewAppError("SelectImportSource", "app.import.source.not_awaiting_source.app_error", nil, "", http.StatusConflict)
	}
	// A new-Space target has exactly one possible source identity and never reaches awaiting_source, so a
	// selection for one is a client error rather than something to apply.
	if job.TargetKind == model.ImportTargetNew {
		return nil, mmmodel.NewAppError("SelectImportSource", "app.import.source.new_target_not_selectable.app_error", nil, "", http.StatusBadRequest)
	}
	// Passing the upload's authorization grants nothing later: re-check membership now, because access can
	// be revoked between uploading a bundle and choosing where its history belongs.
	if appErr = s.requireImportTargetStillAuthorized(job, actorID); appErr != nil {
		return nil, appErr
	}

	selected, err := s.store.SelectImportSource(jobID, actorID, req)
	if err != nil {
		return nil, storeAppError("SelectImportSource", err)
	}

	s.log.Info("Import source selected",
		"job_id", selected.Id, "actor_id", actorID, "target_space_id", selected.TargetSpaceId,
		"mode", string(selected.SourceSelectionMode), "import_source_id", selected.SelectedImportSourceId)
	return buildImportJobViewWithoutCandidates(selected), nil
}

// ConfirmImportJob records the user's confirmation of a reviewed preflight and queues the import.
//
// Confirmation is the point of no return, so everything it depends on is rechecked here rather than
// trusted from review time: the actor's access, the exact preflight revision, the source's mapping
// revision, and each approved conflict against the persisted results. The browser never sends hashes —
// approval carries intent, while the server-owned baselines carry safety.
func (s *Service) ConfirmImportJob(jobID, actorID string, req model.ImportConfirmRequest) (*model.ImportJobView, *mmmodel.AppError) {
	if appErr := req.IsValid(); appErr != nil {
		return nil, appErr
	}
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, appErr
	}
	if job.State != model.ImportStateAwaitingConfirmation {
		return nil, mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.not_awaiting_confirmation.app_error", nil, "", http.StatusConflict)
	}
	if job.PreflightRevision == "" || job.PreflightRevision != req.PreflightRevision {
		return nil, mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.revision_mismatch.app_error", nil, "", http.StatusConflict)
	}
	if appErr = s.requireImportTargetStillAuthorized(job, actorID); appErr != nil {
		return nil, appErr
	}

	if appErr = s.checkImportAcknowledgements(job, req); appErr != nil {
		return nil, appErr
	}
	if appErr = s.checkImportNewSpaceMetadata(job, req); appErr != nil {
		return nil, appErr
	}
	if appErr = s.checkImportOverwriteApprovals(job, req); appErr != nil {
		return nil, appErr
	}

	confirmation := model.ImportConfirmation{
		PreflightRevision:  req.PreflightRevision,
		NewSpace:           req.NewSpace,
		Acknowledgements:   req.ToAcknowledgements(),
		OverwriteConflicts: req.OverwriteConflicts,
	}
	confirmed, err := s.store.ConfirmImportJob(jobID, actorID, confirmation)
	if err != nil {
		if store.IsErrPreflightStale(err) {
			// The plan the user reviewed no longer describes the source. The job has already been returned
			// to the preflight queue in the same transaction, so the client waits for a new revision rather
			// than retrying this one.
			s.log.Info("Import confirmation rejected: preflight is stale and recomputing",
				"job_id", jobID, "actor_id", actorID)
			return nil, mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.preflight_stale_recomputing.app_error",
				map[string]any{"Code": ImportErrorPreflightStale}, "", http.StatusConflict)
		}
		return nil, storeAppError("ConfirmImportJob", err)
	}

	s.log.Info("Import confirmed",
		"job_id", confirmed.Id, "actor_id", actorID, "target_space_id", confirmed.TargetSpaceId,
		"preflight_revision", confirmed.PreflightRevision, "mapping_revision", confirmed.PreflightMappingRevision,
		"approved_overwrites", len(req.OverwriteConflicts))
	return buildImportJobViewWithoutCandidates(confirmed), nil
}

// ImportErrorPreflightStale is the stable code a client sees when its reviewed preflight was invalidated.
const ImportErrorPreflightStale = "preflight_stale_recomputing"

// requireImportTargetStillAuthorized re-runs the gate the upload passed. It is deliberately separate from
// actorStillEntitled, which decides how much of a job to *show*: here a failure must refuse the action
// with 403 rather than quietly returning a redacted view.
func (s *Service) requireImportTargetStillAuthorized(job *model.ImportJob, actorID string) *mmmodel.AppError {
	if appErr := s.requireClient("requireImportTargetStillAuthorized", "actor_id", actorID); appErr != nil {
		return appErr
	}
	user, err := s.client.User.Get(actorID)
	if importActorMissing(err) || (err == nil && user == nil) {
		// An actor who definitively does not exist is a denial, not an inconclusive lookup. Reporting it as a
		// 500 would make it retryable, and since a retryable failure leaves the job in importing — the
		// highest-priority state — the worker would re-select that same job on every pass and starve the queue
		// behind it. A deleted actor's job has to reach a terminal state.
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.target.actor_inactive.app_error", nil, "", http.StatusForbidden)
	}
	if err != nil {
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.entitlement.lookup_failed.app_error", nil, "", http.StatusInternalServerError)
	}
	if user.DeleteAt != 0 {
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.target.actor_inactive.app_error", nil, "", http.StatusForbidden)
	}

	targeted, targetErr := s.importTargetSpaceExists(job)
	if targetErr != nil {
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.entitlement.lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(targetErr)
	}
	if targeted {
		if _, spaceErr := s.CheckSpaceMembership(job.TargetSpaceId, actorID, false); spaceErr != nil {
			return spaceErr
		}
		return nil
	}
	active, memberErr := s.isActiveTeamMember(job.TeamId, actorID)
	if memberErr != nil {
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.target.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
	}
	if !active {
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.target.not_team_member.app_error", nil, "", http.StatusForbidden)
	}
	if !s.client.User.HasPermissionToTeam(actorID, job.TeamId, mmmodel.PermissionCreatePublicChannel) {
		return mmmodel.NewAppError("requireImportTargetStillAuthorized", "app.import.target.cannot_create_space.app_error", nil, "", http.StatusForbidden)
	}
	return nil
}

// importActorMissing reports whether a user lookup definitively established that the actor is gone, as opposed
// to failing to establish anything. pluginapi returns its not-found sentinel only for a 404, so this is the one
// error that carries an answer.
func importActorMissing(err error) bool {
	return stderrors.Is(err, pluginapi.ErrNotFound)
}

// importTargetSpaceExists reports whether the job's target Space exists yet, which is what decides *which*
// authorization gate applies.
//
// TargetSpaceExisted records what was true at upload and never changes, so it cannot answer this on its own.
// A new-Space job has no Space to check before provisioning, and the team gate that authorized it is the only
// gate available then — but the moment the Space row exists, membership of that Space is the real boundary. A
// job that kept asking the team question would let an actor removed from the Space it just created carry on
// writing pages into it, and would keep showing them target-identifying job fields afterwards.
//
// Soft-deleted Spaces count as existing: once a Space has been provisioned, the Space is the thing to
// authorize against, and a deleted one must fail that check rather than fall back to a gate that would pass.
func (s *Service) importTargetSpaceExists(job *model.ImportJob) (bool, error) {
	if job.TargetSpaceExisted {
		return true, nil
	}
	if _, err := s.store.GetSpace(job.TargetSpaceId, true); err != nil {
		if store.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// checkImportAcknowledgements requires every acknowledgement the job demands, and no more. The required
// set comes from the job's own persisted bundle counts and preflight action counts rather than from
// anything the request asserts.
func (s *Service) checkImportAcknowledgements(job *model.ImportJob, req model.ImportConfirmRequest) *mmmodel.AppError {
	for _, key := range requiredAcknowledgements(job) {
		if !req.Acknowledged(key) {
			return mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.missing_acknowledgement.app_error",
				map[string]any{"Key": key}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// checkImportNewSpaceMetadata enforces the new-Space rules: a new target must supply the final title and
// description, and an existing target must not supply them at all.
func (s *Service) checkImportNewSpaceMetadata(job *model.ImportJob, req model.ImportConfirmRequest) *mmmodel.AppError {
	if job.TargetKind == model.ImportTargetNew {
		if req.NewSpace == nil {
			return mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.new_space_required.app_error", nil, "", http.StatusBadRequest)
		}
		return nil
	}
	if req.NewSpace != nil {
		return mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.new_space_not_allowed.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// checkImportOverwriteApprovals requires every approved external id to name a page this job's preflight
// actually classified as a conflict.
//
// There is deliberately no blanket overwrite-all flag: each approval is a decision to discard a specific
// person's edits, so it is made per page against the reviewed result set. An id that is not a conflict —
// or that belongs to a different job — is refused rather than ignored, because silently dropping it would
// let a client believe it had approved something it had not.
func (s *Service) checkImportOverwriteApprovals(job *model.ImportJob, req model.ImportConfirmRequest) *mmmodel.AppError {
	if len(req.OverwriteConflicts) == 0 {
		return nil
	}
	conflicts, err := s.store.GetImportConflictExternalIDs(job.Id)
	if err != nil {
		return storeAppError("ConfirmImportJob", err)
	}
	for _, externalID := range req.OverwriteConflicts {
		if _, ok := conflicts[externalID]; !ok {
			return mmmodel.NewAppError("ConfirmImportJob", "app.import.confirm.not_a_conflict.app_error",
				map[string]any{"ExternalId": externalID}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// GetImportPreflightResults returns one page of a job's preflight review rows.
//
// Visibility matches the rest of the import read surface: the job's own actor only, and nothing at all
// once they have lost access to the target — these rows name pages, titles, and local ids inside it.
func (s *Service) GetImportPreflightResults(jobID, actorID string, page, perPage int) ([]*model.ImportPreflightResultView, bool, *mmmodel.AppError) {
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, false, appErr
	}
	entitled, appErr := s.actorStillEntitled(job, actorID)
	if appErr != nil {
		return nil, false, appErr
	}
	if !entitled {
		return nil, false, mmmodel.NewAppError("GetImportPreflightResults", "app.store.not_found.app_error", nil, "", http.StatusNotFound)
	}

	offset, limit := paginationOffsetLimit(page, perPage)
	records, err := s.store.GetImportPreflightResults(jobID, offset, limit)
	if err != nil {
		return nil, false, storeAppError("GetImportPreflightResults", err)
	}
	records, hasMore := trimPage(records, limit)

	views := make([]*model.ImportPreflightResultView, 0, len(records))
	for _, r := range records {
		views = append(views, importPreflightResultView(r))
	}
	return views, hasMore, nil
}

// importPreflightResultView projects one persisted result into the review shape. Hashes, mapping
// timestamps, bodies, and raw props are deliberately absent: the wizard shows what will happen, and the
// baselines that make it safe stay server-side.
func importPreflightResultView(r *model.ImportResultRecord) *model.ImportPreflightResultView {
	view := &model.ImportPreflightResultView{
		ExternalId:    r.ExternalId,
		LocalId:       r.LocalId,
		Title:         r.Title,
		PlannedAction: r.PlannedAction,
		Outcome:       string(r.Outcome),
	}
	if eligible, ok := r.Details["overwrite_eligible"].(bool); ok {
		view.OverwriteEligible = eligible
	}
	if changes, ok := r.Details["structural_changes"].([]any); ok {
		for _, c := range changes {
			if code, isString := c.(string); isString {
				view.StructuralChanges = append(view.StructuralChanges, code)
			}
		}
	}
	return view
}
