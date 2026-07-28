// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// unconfirmedJobRetention is how long a job that has not yet been confirmed is kept before cleanup
// reclaims it (plan §21). Promotion out of the source queue resets this window so time spent waiting
// does not consume the user's review time.
const unconfirmedJobRetentionMillis = int64(7 * 24 * 60 * 60 * 1000)

// CreateImportFromBundle synchronously inspects an uploaded mmetl bundle and, on success, persists a
// new import job together with its normalized staged pages and inspection issues. No page is written
// and no Space is provisioned here: this is the upload/inspection half of the flow, and the returned
// job is left in the state its target kind implies (awaiting_source for an existing Space, whose
// ImportSource the user must still choose; queued_preflight for a new Space, which has exactly one
// possible source).
//
// bundle/size address the already-uploaded archive (the HTTP layer streams it to a temp file and
// owns its lifetime); bundleSha256 is the digest computed over those bytes during the upload.
func (s *Service) CreateImportFromBundle(actorID string, req *model.ImportUploadRequest, bundle io.ReaderAt, size int64, bundleSha256 string) (*model.ImportJobView, *mmmodel.AppError) {
	if req == nil {
		return nil, mmmodel.NewAppError("CreateImportFromBundle", "app.import.create.nil_request.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(actorID) {
		return nil, mmmodel.NewAppError("CreateImportFromBundle", "app.import.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := req.Target.IsValid(); appErr != nil {
		return nil, appErr
	}
	if !importer.IsValidSHA256Hex(bundleSha256) {
		return nil, mmmodel.NewAppError("CreateImportFromBundle", "app.import.create.invalid_bundle_digest.app_error", nil, "", http.StatusInternalServerError)
	}

	target, appErr := s.authorizeImportTarget(actorID, req.Target)
	if appErr != nil {
		s.log.Warn("Import upload rejected: target authorization failed",
			"actor_id", actorID, "target_kind", string(req.Target.Kind), "error_id", appErr.Id, "status", appErr.StatusCode)
		return nil, appErr
	}

	inspection, appErr := s.inspectImportBundle(bundle, size, target)
	if appErr != nil {
		s.log.Warn("Import upload rejected: bundle inspection failed",
			"actor_id", actorID, "team_id", target.teamID, "target_space_id", target.spaceID,
			"bundle_sha256", bundleSha256, "error_id", appErr.Id, "status", appErr.StatusCode)
		return nil, appErr
	}

	job, appErr := buildImportJob(actorID, target, inspection, bundleSha256)
	if appErr != nil {
		return nil, appErr
	}
	staged := buildStagedPages(job.Id, inspection)
	issues := buildInspectionIssues(job.Id, inspection)

	if _, err := s.store.CreateImportJobWithStaging(job, staged, issues); err != nil {
		s.log.Error("Import upload rejected: failed to persist job and staging",
			"actor_id", actorID, "job_id", job.Id, "team_id", target.teamID, "err", err)
		return nil, storeAppError("CreateImportFromBundle", err)
	}

	// Operator-facing audit line (plan §24). Deliberately carries counts and identifiers only —
	// never page bodies or archive bytes.
	s.log.Info("Import upload inspection accepted",
		"job_id", job.Id,
		"actor_id", actorID,
		"team_id", job.TeamId,
		"target_kind", string(job.TargetKind),
		"target_space_id", job.TargetSpaceId,
		"target_space_existed", job.TargetSpaceExisted,
		"state", string(job.State),
		"bundle_sha256", job.BundleSha256,
		"source_space_key", inspection.SpaceKey,
		"source_organization_id", inspection.OrganizationID,
		"pages", len(inspection.Pages),
		"comments", inspection.CommentCount,
		"attachments", inspection.AttachmentCount,
		"restricted_emitted_pages", inspection.Restricted.EmittedPages,
		"restricted_manifest_only", inspection.Restricted.ManifestOnly,
		"inspection_issues", len(issues),
	)

	return s.BuildImportJobView(job)
}

// importTarget is the authorized, resolved destination of an import: the ids the job is created with
// after the request has been checked. teamID always comes from the server's own view (the Space's
// team for an existing target), never from the request body for an existing Space.
type importTarget struct {
	kind      model.ImportTargetKind
	spaceID   string
	teamID    string
	teamName  string
	existed   bool
	spaceName string
}

// authorizeImportTarget validates that the actor may import into the requested target and resolves
// the ids the job will carry. Authorization is re-checked at source selection, confirmation, and
// immediately before execution: passing here grants nothing later.
func (s *Service) authorizeImportTarget(actorID string, req model.ImportTargetRequest) (importTarget, *mmmodel.AppError) {
	if appErr := s.requireClient("authorizeImportTarget", "actor_id", actorID); appErr != nil {
		return importTarget{}, appErr
	}

	switch req.Kind {
	case model.ImportTargetExisting:
		// Membership of the Space's backing channel (plus active membership of its team) is the
		// current authorization boundary; CheckSpaceMembership enforces both.
		space, appErr := s.CheckSpaceMembership(req.SpaceId, actorID, false)
		if appErr != nil {
			return importTarget{}, appErr
		}
		// The job's TeamId must be a real id (model validation requires it) and every later team
		// re-check compares against it, so a Space with no team cannot be an import target.
		if !mmmodel.IsValidId(space.TeamId) {
			return importTarget{}, mmmodel.NewAppError("authorizeImportTarget", "app.import.target.space_without_team.app_error", nil, "", http.StatusBadRequest)
		}
		return importTarget{
			kind:      model.ImportTargetExisting,
			spaceID:   space.Id,
			teamID:    space.TeamId,
			teamName:  s.lookupTeamName(space.TeamId),
			existed:   true,
			spaceName: space.Title,
		}, nil

	case model.ImportTargetNew:
		active, memberErr := s.isActiveTeamMember(req.TeamId, actorID)
		if memberErr != nil {
			return importTarget{}, mmmodel.NewAppError("authorizeImportTarget", "app.import.target.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
		if !active {
			return importTarget{}, mmmodel.NewAppError("authorizeImportTarget", "app.import.target.not_team_member.app_error", nil, "", http.StatusForbidden)
		}
		if !s.client.User.HasPermissionToTeam(actorID, req.TeamId, mmmodel.PermissionCreatePublicChannel) {
			return importTarget{}, mmmodel.NewAppError("authorizeImportTarget", "app.import.target.cannot_create_space.app_error", nil, "", http.StatusForbidden)
		}
		// The target Space id is generated up front, before the Space exists, so execution can
		// serialize per target without a nullable key (plan §8.2).
		return importTarget{
			kind:     model.ImportTargetNew,
			spaceID:  mmmodel.NewId(),
			teamID:   req.TeamId,
			teamName: s.lookupTeamName(req.TeamId),
			existed:  false,
		}, nil

	default:
		return importTarget{}, mmmodel.NewAppError("authorizeImportTarget", "model.import_target.is_valid.kind.app_error", nil, "", http.StatusBadRequest)
	}
}

// lookupTeamName resolves a team's name for the advisory bundle-team comparison only. A failure is
// not fatal: the comparison is a reporting nicety, and the request's team id already decided the
// destination, so an empty name simply skips the bundle_team_mismatch warning.
func (s *Service) lookupTeamName(teamID string) string {
	team, err := s.client.Team.Get(teamID)
	if err != nil || team == nil {
		s.log.Warn("Could not resolve team name for the advisory bundle-team comparison; the warning will be skipped", "team_id", teamID, "err", err)
		return ""
	}
	return team.Name
}

// inspectImportBundle runs the pure archive/JSONL inspection and maps its stable failure codes onto
// HTTP statuses.
func (s *Service) inspectImportBundle(bundle io.ReaderAt, size int64, target importTarget) (*importer.InspectionResult, *mmmodel.AppError) {
	contents, err := importer.InspectArchive(bundle, size)
	if err != nil {
		return nil, importFailureAppError("inspectImportBundle", err)
	}
	inspection, err := importer.Inspect(contents, importer.InspectOptions{
		RequestedTeamName: target.teamName,
		Now:               mmmodel.GetMillis(),
	})
	if err != nil {
		return nil, importFailureAppError("inspectImportBundle", err)
	}
	return inspection, nil
}

// importContentLimitCodes are the inspection/archive failure codes that mean "this is a structurally
// valid bundle whose content exceeds a Docs limit". They map to 422 rather than 400, matching the
// plan's error contract (§23), so a client can distinguish a malformed bundle from an oversized one.
var importContentLimitCodes = map[string]struct{}{
	importer.InspectErrTooManyPages:     {},
	importer.InspectErrDepthExceeded:    {},
	importer.InspectErrPageTitleTooLong: {},
	importer.TipTapErrTooManyNodes:      {},
	importer.TipTapErrTooDeep:           {},
	importer.TipTapErrBodyTooLarge:      {},
	importer.TipTapErrSearchTooLarge:    {},
}

// importArchiveSizeCodes are the archive failure codes that mean "the upload itself is too large",
// which the plan maps to 413.
var importArchiveSizeCodes = map[string]struct{}{
	importer.ArchiveErrManifestTooLarge: {},
	importer.ArchiveErrJSONLTooLarge:    {},
	importer.ArchiveErrTooManyEntries:   {},
	importer.InspectErrLineTooLong:      {},
}

// importFailureAppError converts an importer rejection into an *AppError carrying the importer's
// stable code as a parameter, so the client sees a machine-readable reason without any internal
// detail (paths, SQL, page bodies) leaking into the response.
// The stable importer code is carried as a message parameter rather than as DetailedError, because
// writeAppError scrubs DetailedError before responding and the code is the one detail that makes the
// failure actionable. Each branch constructs its AppError with a string-literal id so the i18n
// extraction tool can discover the key.
func importFailureAppError(where string, err error) *mmmodel.AppError {
	code := importFailureCode(err)
	params := map[string]any{"Code": code}
	if _, ok := importArchiveSizeCodes[code]; ok {
		return mmmodel.NewAppError(where, "app.import.bundle_too_large.app_error", params, "", http.StatusRequestEntityTooLarge).Wrap(err)
	}
	if _, ok := importContentLimitCodes[code]; ok {
		return mmmodel.NewAppError(where, "app.import.bundle_content_not_processable.app_error", params, "", http.StatusUnprocessableEntity).Wrap(err)
	}
	return mmmodel.NewAppError(where, "app.import.bundle_invalid.app_error", params, "", http.StatusBadRequest).Wrap(err)
}

// importFailureCode extracts the stable code from an importer error, or "" for an unexpected one.
func importFailureCode(err error) string {
	var archiveErr *importer.ArchiveError
	if errors.As(err, &archiveErr) {
		return archiveErr.Code
	}
	var inspectErr *importer.InspectError
	if errors.As(err, &inspectErr) {
		return inspectErr.Code
	}
	var tiptapErr *importer.TipTapError
	if errors.As(err, &tiptapErr) {
		return tiptapErr.Code
	}
	return ""
}

// buildImportJob assembles the job row from the authorized target and the inspection result.
func buildImportJob(actorID string, target importTarget, inspection *importer.InspectionResult, bundleSha256 string) (*model.ImportJob, *mmmodel.AppError) {
	now := mmmodel.GetMillis()
	job := &model.ImportJob{
		Id:                 mmmodel.NewId(),
		ActorId:            actorID,
		TeamId:             target.teamID,
		TargetKind:         target.kind,
		TargetSpaceId:      target.spaceID,
		TargetSpaceExisted: target.existed,
		BundleSha256:       bundleSha256,
		ProgressTotal:      int64(len(inspection.Pages)),
		CreateAt:           now,
		UpdateAt:           now,
		RetainUntil:        now + unconfirmedJobRetentionMillis,
	}

	if target.kind == model.ImportTargetNew {
		// A new Space has exactly one possible source identity, so there is nothing for the user to
		// choose: the source is created as part of execution and the job goes straight to preflight.
		job.SourceSelectionMode = model.ImportSourceModeNew
		job.SelectedImportSourceId = mmmodel.NewId()
		job.State = model.ImportStateQueuedPreflight
	} else {
		// An existing Space may already hold several import sources, so the user must pick one (or
		// ask for a new identity) before mappings can be consulted.
		job.State = model.ImportStateAwaitingSource
	}

	summary, err := toStringInterface(bundleSummaryOf(inspection))
	if err != nil {
		return nil, mmmodel.NewAppError("buildImportJob", "app.import.create.summary_encode_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	job.BundleSummary = summary

	return job, nil
}

// bundleSummaryOf projects the inspection result onto the API-safe bundle summary persisted on the
// job, so the summary survives staged-body cleanup and can be rebuilt without re-reading the archive.
func bundleSummaryOf(inspection *importer.InspectionResult) model.ImportBundleSummary {
	return model.ImportBundleSummary{
		Version: inspection.Version,
		Source: model.ImportReportSource{
			OrganizationId: inspection.OrganizationID,
			SpaceKey:       inspection.SpaceKey,
			SpaceName:      inspection.SpaceName,
		},
		SpaceDefaults: model.ImportSpaceDefaults{
			Title:       inspection.SpaceTitle,
			Description: inspection.SpaceDescription,
		},
		Counts: model.ImportBundleCounts{
			Pages:                   len(inspection.Pages),
			Comments:                inspection.CommentCount,
			Attachments:             inspection.AttachmentCount,
			RestrictedManifestTotal: inspection.Restricted.ManifestTotal,
			RestrictedEmittedPages:  inspection.Restricted.EmittedPages,
			RestrictedManifestOnly:  inspection.Restricted.ManifestOnly,
		},
	}
}

// buildStagedPages converts the inspector's normalized pages into staging rows. Bodies and search
// text are carried as-is; the importer has already canonicalized and size-checked them.
func buildStagedPages(jobID string, inspection *importer.InspectionResult) []*model.ImportStagedPage {
	staged := make([]*model.ImportStagedPage, 0, len(inspection.Pages))
	for i := range inspection.Pages {
		p := &inspection.Pages[i]
		staged = append(staged, &model.ImportStagedPage{
			JobId:                 jobID,
			Ordinal:               p.Ordinal,
			ExternalId:            p.ExternalID,
			ParentExternalId:      p.ParentExternalID,
			SourceOrdinal:         p.SourceOrdinal,
			Title:                 p.Title,
			CanonicalBody:         p.CanonicalBody,
			SearchText:            p.SearchText,
			SourceUserProposal:    p.SourceUserProposal,
			SourceAuthorAccountId: p.SourceAuthorAccountID,
			SourceCreateAt:        p.SourceCreateAt,
			SourceUpdateAt:        p.SourceUpdateAt,
			SourceProps:           mmmodel.StringInterface(p.SourceProps),
			IncomingSourceHash:    p.IncomingSourceHash,
		})
	}
	return staged
}

// buildInspectionIssues converts inspection findings into issue rows with deterministic ordinals
// (the finding's index), so a replay of the same bundle produces the same rows. The count is bounded
// by the importer: manifest warnings are capped, and per-page findings are a small constant times
// the page cap.
func buildInspectionIssues(jobID string, inspection *importer.InspectionResult) []*model.ImportIssueRecord {
	issues := make([]*model.ImportIssueRecord, 0, len(inspection.Issues))
	for i, is := range inspection.Issues {
		entityType := ""
		if is.ExternalID != "" {
			entityType = model.ImportEntityTypePage
		}
		issues = append(issues, &model.ImportIssueRecord{
			JobId:       jobID,
			Stage:       model.ImportStageInspection,
			Ordinal:     i,
			Severity:    is.Severity,
			Code:        is.Code,
			EntityType:  entityType,
			ExternalId:  is.ExternalID,
			Title:       is.Title,
			Message:     is.Message,
			Remediation: is.Remediation,
			Details:     mmmodel.StringInterface(is.Details),
		})
	}
	return issues
}

// GetImportJob returns the actor's own job. Job visibility is actor-only in V1, and another user's
// job is reported as not found rather than forbidden so the endpoint cannot be used to probe for the
// existence of someone else's import.
func (s *Service) GetImportJob(jobID, actorID string) (*model.ImportJobView, *mmmodel.AppError) {
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, appErr
	}
	return s.BuildImportJobView(job)
}

// getOwnImportJob loads a job and enforces actor-only visibility.
func (s *Service) getOwnImportJob(jobID, actorID string) (*model.ImportJob, *mmmodel.AppError) {
	if !mmmodel.IsValidId(jobID) {
		return nil, mmmodel.NewAppError("getOwnImportJob", "app.import.get.invalid_id.app_error", nil, "", http.StatusNotFound)
	}
	if !mmmodel.IsValidId(actorID) {
		return nil, mmmodel.NewAppError("getOwnImportJob", "app.import.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	job, err := s.store.GetImportJob(jobID)
	if err != nil {
		return nil, storeAppError("getOwnImportJob", err)
	}
	if job.ActorId != actorID {
		return nil, mmmodel.NewAppError("getOwnImportJob", "app.store.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return job, nil
}

// GetImportJobsForActor returns one page of the actor's own import jobs, newest first, optionally
// restricted to one team.
func (s *Service) GetImportJobsForActor(actorID, teamID string, page, perPage int) ([]*model.ImportJobView, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(actorID) {
		return nil, false, mmmodel.NewAppError("GetImportJobsForActor", "app.import.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if teamID != "" && !mmmodel.IsValidId(teamID) {
		return nil, false, mmmodel.NewAppError("GetImportJobsForActor", "app.import.list.invalid_team_id.app_error", nil, "", http.StatusBadRequest)
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	jobs, err := s.store.GetImportJobsForActor(actorID, teamID, offset, limit)
	if err != nil {
		return nil, false, storeAppError("GetImportJobsForActor", err)
	}
	jobs, hasMore := trimPage(jobs, limit)

	views := make([]*model.ImportJobView, 0, len(jobs))
	for _, job := range jobs {
		// The list view omits source candidates: they are a per-job interactive concern, and querying
		// them per row would turn one listing into N queries.
		views = append(views, buildImportJobViewWithoutCandidates(job))
	}
	return views, hasMore, nil
}

// GetImportIssues returns one page of a job's persisted issues, for the actor that owns the job.
func (s *Service) GetImportIssues(jobID, actorID, stage, severity string, page, perPage int) ([]*model.ImportIssue, bool, *mmmodel.AppError) {
	if _, appErr := s.getOwnImportJob(jobID, actorID); appErr != nil {
		return nil, false, appErr
	}
	if appErr := validateIssueFilters(stage, severity); appErr != nil {
		return nil, false, appErr
	}
	offset, limit := paginationOffsetLimit(page, perPage)
	records, err := s.store.GetImportIssues(jobID, stage, severity, offset, limit)
	if err != nil {
		return nil, false, storeAppError("GetImportIssues", err)
	}
	records, hasMore := trimPage(records, limit)

	issues := make([]*model.ImportIssue, 0, len(records))
	for _, r := range records {
		issues = append(issues, importIssueViewOf(r))
	}
	return issues, hasMore, nil
}

// validateIssueFilters rejects a stage/severity filter outside the persisted enumerations, so a typo
// silently returning an empty page is not mistaken for "no issues".
func validateIssueFilters(stage, severity string) *mmmodel.AppError {
	switch stage {
	case "", model.ImportStageInspection, model.ImportStagePreflight, model.ImportStageExecution:
	default:
		return mmmodel.NewAppError("validateIssueFilters", "app.import.issues.invalid_stage.app_error", nil, "", http.StatusBadRequest)
	}
	switch severity {
	case "", model.ImportSeverityInfo, model.ImportSeverityWarning, model.ImportSeverityError:
	default:
		return mmmodel.NewAppError("validateIssueFilters", "app.import.issues.invalid_severity.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// importIssueViewOf projects a persisted issue row onto the report-facing issue shape.
func importIssueViewOf(r *model.ImportIssueRecord) *model.ImportIssue {
	issue := &model.ImportIssue{
		Stage:       r.Stage,
		Severity:    r.Severity,
		Code:        r.Code,
		Message:     r.Message,
		Remediation: r.Remediation,
		Details:     r.Details,
	}
	if r.EntityType != "" || r.ExternalId != "" || r.LocalId != "" {
		issue.Entity = &model.ImportEntityRef{
			Type:       r.EntityType,
			ExternalId: r.ExternalId,
			LocalId:    r.LocalId,
			Title:      r.Title,
		}
	}
	return issue
}

// BuildImportJobView projects a job onto its API-safe view, including the ImportSource candidates a
// user choosing an existing target needs. Candidates are suggestions only and are never
// auto-selected.
func (s *Service) BuildImportJobView(job *model.ImportJob) (*model.ImportJobView, *mmmodel.AppError) {
	view := buildImportJobViewWithoutCandidates(job)

	// Candidates only matter while the user still has a choice to make.
	if job.State == model.ImportStateAwaitingSource && job.TargetSpaceExisted {
		candidates, appErr := s.importSourceCandidates(job, view.Bundle)
		if appErr != nil {
			return nil, appErr
		}
		view.SourceCandidates = candidates
	}
	return view, nil
}

// buildImportJobViewWithoutCandidates projects the fields that need no extra queries. It deliberately
// omits claim tokens, lease owners, the provisioned channel id, raw confirmation, and internal error
// messages: only the stable error code is exposed.
func buildImportJobViewWithoutCandidates(job *model.ImportJob) *model.ImportJobView {
	view := &model.ImportJobView{
		Id:    job.Id,
		State: job.State,
		Phase: job.Phase,
		Progress: model.ImportProgress{
			Phase:   job.Phase,
			Current: job.ProgressCurrent,
			Total:   job.ProgressTotal,
		},
		Target: model.ImportTargetView{
			Kind:    job.TargetKind,
			SpaceId: job.TargetSpaceId,
			TeamId:  job.TeamId,
			Existed: job.TargetSpaceExisted,
		},
		Bundle:           bundleSummaryFromJob(job),
		SourceCandidates: []model.ImportSourceCandidate{},
		CreateAt:         job.CreateAt,
		UpdateAt:         job.UpdateAt,
		FinishedAt:       job.FinishedAt,
	}
	if job.SourceSelectionMode != model.ImportSourceModeUnset {
		view.SelectedSource = &model.ImportSelectedSource{
			Mode:           job.SourceSelectionMode,
			ImportSourceId: job.SelectedImportSourceId,
			DisplayName:    job.SelectedSourceDisplayName,
		}
	}
	if job.ErrorCode != "" {
		view.Error = &model.ImportPublicError{Code: job.ErrorCode}
	}
	view.RequiredAcknowledgements = requiredAcknowledgements(job, view.Bundle)
	return view
}

// bundleSummaryFromJob decodes the summary persisted on the job. A summary that cannot be decoded
// yields the zero value rather than failing the read: the job and its issues remain inspectable,
// which matters more than the counts block on a status call.
func bundleSummaryFromJob(job *model.ImportJob) model.ImportBundleSummary {
	var summary model.ImportBundleSummary
	if len(job.BundleSummary) == 0 {
		return summary
	}
	raw, err := json.Marshal(job.BundleSummary)
	if err != nil {
		return summary
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		return model.ImportBundleSummary{}
	}
	return summary
}

// requiredAcknowledgements lists the confirmation acknowledgements already implied by the bundle and
// target. reimport_existing_pages is deliberately absent until preflight has compared the bundle
// against existing mappings — only then is it known whether any page is a reimport.
func requiredAcknowledgements(job *model.ImportJob, bundle model.ImportBundleSummary) []string {
	acks := []string{}
	if job.TargetKind == model.ImportTargetNew {
		acks = append(acks, model.ImportAckNewSpaceMetadata)
	}
	if bundle.Counts.Comments > 0 || bundle.Counts.Attachments > 0 {
		acks = append(acks, model.ImportAckPageOnlyPartial)
	}
	// Only restricted entries that intersect emitted pages widen real access; manifest-only entries
	// are reported instead, so they must not demand an acknowledgement.
	if bundle.Counts.RestrictedEmittedPages > 0 {
		acks = append(acks, model.ImportAckWidenRestricted)
	}
	return acks
}

// importSourceCandidates suggests existing ImportSources in the job's target Space that may be the
// same Confluence Space as the uploaded bundle, each with the reasons it matched. Scoring only
// orders the suggestions: selection is always explicit, because two Confluence instances can share
// an organization id, a space key, and a display name while being genuinely different sources.
func (s *Service) importSourceCandidates(job *model.ImportJob, bundle model.ImportBundleSummary) ([]model.ImportSourceCandidate, *mmmodel.AppError) {
	sources, err := s.store.GetImportSourcesForSpace(job.TargetSpaceId)
	if err != nil {
		return nil, storeAppError("importSourceCandidates", err)
	}
	if len(sources) == 0 {
		return []model.ImportSourceCandidate{}, nil
	}

	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.Id)
	}
	mapped, err := s.store.CountImportSourceMappedPages(ids)
	if err != nil {
		return nil, storeAppError("importSourceCandidates", err)
	}

	candidates := make([]model.ImportSourceCandidate, 0, len(sources))
	for _, src := range sources {
		reasons := candidateMatchReasons(src, bundle)
		candidates = append(candidates, model.ImportSourceCandidate{
			ImportSourceId:   src.Id,
			DisplayName:      src.DisplayName,
			OrganizationId:   src.OrganizationId,
			ExternalSpaceKey: src.ExternalSpaceKey,
			MappedPageCount:  mapped[src.Id],
			LastImportAt:     src.LastImportAt,
			MatchReasons:     reasons,
		})
	}

	// Strongest match first, then most recently imported, then oldest — a stable order so the UI
	// does not reshuffle between polls.
	sortImportSourceCandidates(candidates)
	return candidates, nil
}

// Candidate match reason codes, strongest first.
const (
	importMatchOrgAndSpaceKey = "exact_organization_id_and_space_key"
	importMatchSpaceKey       = "exact_space_key"
	importMatchDisplayName    = "display_name_similar"
)

// candidateMatchReasons returns why a source may correspond to the uploaded bundle. An empty result
// means "no signal", not "not a match": the user may still legitimately select it.
func candidateMatchReasons(src *model.ImportSource, bundle model.ImportBundleSummary) []string {
	reasons := []string{}
	sameKey := src.ExternalSpaceKey != "" && src.ExternalSpaceKey == bundle.Source.SpaceKey
	sameOrg := src.OrganizationId != "" && src.OrganizationId == bundle.Source.OrganizationId
	switch {
	case sameKey && sameOrg:
		reasons = append(reasons, importMatchOrgAndSpaceKey)
	case sameKey:
		reasons = append(reasons, importMatchSpaceKey)
	}
	if displayNameSuggestsSource(src.DisplayName, bundle) {
		reasons = append(reasons, importMatchDisplayName)
	}
	return reasons
}

// displayNameSuggestsSource reports whether a source's user-chosen display name mentions the
// bundle's space key or name. This is a deliberately simple containment check: the display name is
// free text a user typed, so it is a hint for ordering, never evidence of identity.
func displayNameSuggestsSource(displayName string, bundle model.ImportBundleSummary) bool {
	name := strings.ToLower(strings.TrimSpace(displayName))
	if name == "" {
		return false
	}
	for _, needle := range []string{bundle.Source.SpaceKey, bundle.Source.SpaceName} {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(name, needle) {
			return true
		}
	}
	return false
}

// candidateRank scores a candidate's match strength; higher is a stronger signal.
func candidateRank(c model.ImportSourceCandidate) int {
	rank := 0
	for _, r := range c.MatchReasons {
		switch r {
		case importMatchOrgAndSpaceKey:
			rank += 4
		case importMatchSpaceKey:
			rank += 2
		case importMatchDisplayName:
			rank++
		}
	}
	return rank
}

// sortImportSourceCandidates orders candidates by match strength, then recency of last import, then
// id, giving a total order so repeated calls agree.
func sortImportSourceCandidates(candidates []model.ImportSourceCandidate) {
	slices.SortFunc(candidates, func(a, b model.ImportSourceCandidate) int {
		if c := cmp.Compare(candidateRank(b), candidateRank(a)); c != 0 {
			return c
		}
		if c := cmp.Compare(b.LastImportAt, a.LastImportAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ImportSourceId, b.ImportSourceId)
	})
}

// toStringInterface round-trips a typed value through JSON into the opaque props map the jsonb
// columns are modelled with, so the persisted shape always matches the Go type's JSON tags.
func toStringInterface(v any) (mmmodel.StringInterface, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := mmmodel.StringInterface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
