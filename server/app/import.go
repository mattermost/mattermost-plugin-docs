// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"cmp"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// unconfirmedJobRetentionMillis is how long a job that has not yet been confirmed is kept before
// cleanup expires it. Terminal jobs get a much longer retention set at terminalization.
const unconfirmedJobRetentionMillis = int64(7 * 24 * 60 * 60 * 1000)

// ImportTarget is an authorized, resolved import destination. The HTTP layer obtains one *before*
// reading any bundle bytes, so an unauthorized upload is rejected without spending disk or parser
// work on it, then passes it back when staging the bundle.
//
// TeamID always comes from the server's own view — for an existing Space, from the Space itself —
// never from the request body.
type ImportTarget struct {
	Kind      model.ImportTargetKind
	SpaceID   string
	TeamID    string
	TeamName  string
	Existed   bool
	SpaceName string
}

// AuthorizeImportTarget validates that the actor may import into the requested target and resolves
// the ids the job will carry. Authorization is re-checked at source selection, confirmation, and
// immediately before execution: passing here grants nothing later.
func (s *Service) AuthorizeImportTarget(actorID string, req model.ImportTargetRequest) (*ImportTarget, *mmmodel.AppError) {
	if !mmmodel.IsValidId(actorID) {
		return nil, mmmodel.NewAppError("AuthorizeImportTarget", "app.import.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if appErr := req.IsValid(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireClient("AuthorizeImportTarget", "actor_id", actorID); appErr != nil {
		return nil, appErr
	}

	switch req.Kind {
	case model.ImportTargetExisting:
		// Membership of the Space's backing channel (plus active membership of its team) is the
		// current authorization boundary; CheckSpaceMembership enforces both.
		space, appErr := s.CheckSpaceMembership(req.SpaceId, actorID, false)
		if appErr != nil {
			return nil, appErr
		}
		// The job's TeamId must be a real id (model validation requires it) and every later team
		// re-check compares against it, so a Space with no team cannot be an import target.
		if !mmmodel.IsValidId(space.TeamId) {
			return nil, mmmodel.NewAppError("AuthorizeImportTarget", "app.import.target.space_without_team.app_error", nil, "", http.StatusBadRequest)
		}
		return &ImportTarget{
			Kind:      model.ImportTargetExisting,
			SpaceID:   space.Id,
			TeamID:    space.TeamId,
			TeamName:  s.lookupTeamName(space.TeamId),
			Existed:   true,
			SpaceName: space.Title,
		}, nil

	case model.ImportTargetNew:
		active, memberErr := s.isActiveTeamMember(req.TeamId, actorID)
		if memberErr != nil {
			return nil, mmmodel.NewAppError("AuthorizeImportTarget", "app.import.target.team_lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
		}
		if !active {
			return nil, mmmodel.NewAppError("AuthorizeImportTarget", "app.import.target.not_team_member.app_error", nil, "", http.StatusForbidden)
		}
		if !s.client.User.HasPermissionToTeam(actorID, req.TeamId, mmmodel.PermissionCreatePublicChannel) {
			return nil, mmmodel.NewAppError("AuthorizeImportTarget", "app.import.target.cannot_create_space.app_error", nil, "", http.StatusForbidden)
		}
		// The target Space id is generated up front, before the Space exists, so the job has a stable
		// planned id from the moment it is created.
		return &ImportTarget{
			Kind:     model.ImportTargetNew,
			SpaceID:  mmmodel.NewId(),
			TeamID:   req.TeamId,
			TeamName: s.lookupTeamName(req.TeamId),
			Existed:  false,
		}, nil

	default:
		return nil, mmmodel.NewAppError("AuthorizeImportTarget", "model.import_target.is_valid.kind.app_error", nil, "", http.StatusBadRequest)
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

// CreateImportFromBundle inspects an uploaded mmetl bundle and persists a new import job together
// with its normalized staged pages, manifest users, and inspection issues. The target must already
// have been authorized by AuthorizeImportTarget.
//
// Inspection is synchronous but memory-bounded: the archive is streamed, each page is canonicalized
// and written into the open staging transaction one at a time, and no page body is retained. No page
// is written to the tree and no Space is provisioned here — the job is left in the state its target
// kind implies (awaiting_source for an existing Space, whose ImportSource the user must still choose;
// queued_preflight for a new Space, which has exactly one possible source).
func (s *Service) CreateImportFromBundle(actorID string, target *ImportTarget, bundle io.ReaderAt, size int64, bundleSha256 string) (*model.ImportJobView, *mmmodel.AppError) {
	if target == nil {
		return nil, mmmodel.NewAppError("CreateImportFromBundle", "app.import.create.nil_request.app_error", nil, "", http.StatusBadRequest)
	}
	if !importer.IsValidSHA256Hex(bundleSha256) {
		return nil, mmmodel.NewAppError("CreateImportFromBundle", "app.import.create.invalid_bundle_digest.app_error", nil, "", http.StatusInternalServerError)
	}

	archive, err := importer.OpenArchive(bundle, size)
	if err != nil {
		s.logImportRejected(actorID, target, bundleSha256, err)
		return nil, importFailureAppError("CreateImportFromBundle", err)
	}

	job := s.newImportJob(actorID, target, bundleSha256)

	// The inspector streams into the store's writer while the staging transaction is open. Issue
	// ordinals are assigned in emission order, which the inspector keeps deterministic.
	var summary *importer.InspectionSummary
	issueOrdinal := 0
	saved, staging, storeErr := s.store.CreateImportJobStreaming(job, store.DefaultImportAdmissionLimits(),
		func(w store.ImportStagingWriter) (model.ImportBundleSummary, error) {
			sink := importer.StreamSink{
				Page: func(p *importer.StagedPage) error {
					return w.AddPage(stagedPageRecord(job.Id, p))
				},
				ManifestUser: func(u *importer.StagedManifestUser) error {
					return w.AddManifestUser(&model.ImportManifestUser{
						JobId:              job.Id,
						Ordinal:            u.Ordinal,
						AccountId:          u.AccountID,
						ConfluenceUsername: u.ConfluenceUsername,
						MattermostUsername: u.MattermostUsername,
					})
				},
				Issue: func(i *importer.InspectionIssue) error {
					record := inspectionIssueRecord(job.Id, issueOrdinal, i)
					issueOrdinal++
					return w.AddIssue(record)
				},
			}
			var inspectErr error
			summary, inspectErr = importer.Inspect(archive, importer.InspectOptions{
				RequestedTeamName: target.TeamName,
				Now:               mmmodel.GetMillis(),
			}, sink)
			if inspectErr != nil {
				return model.ImportBundleSummary{}, inspectErr
			}
			return bundleSummaryOf(summary), nil
		})
	if storeErr != nil {
		return nil, s.importStagingAppError(actorID, target, bundleSha256, storeErr)
	}

	// Operator-facing audit line. Deliberately carries counts and identifiers only — never page
	// bodies or archive bytes.
	s.log.Info("Import upload inspection accepted",
		"job_id", saved.Id,
		"actor_id", actorID,
		"team_id", saved.TeamId,
		"target_kind", string(saved.TargetKind),
		"target_space_id", saved.TargetSpaceId,
		"target_space_existed", saved.TargetSpaceExisted,
		"state", string(saved.State),
		"bundle_sha256", saved.BundleSha256,
		"source_space_key", summary.SpaceKey,
		"source_organization_id", summary.OrganizationID,
		"pages", staging.Pages,
		"comments", summary.CommentCount,
		"attachments", summary.AttachmentCount,
		"restricted_emitted_pages", summary.Restricted.EmittedPages,
		"restricted_manifest_only", summary.Restricted.ManifestOnly,
		"manifest_users", staging.ManifestUsers,
		"inspection_issues", staging.Issues,
		"staged_bytes", staging.StagedBytes,
	)

	return s.BuildImportJobView(saved)
}

// newImportJob assembles the job row inserted at the start of the staging transaction. Counts and the
// bundle summary are filled in by the store once streaming settles them.
func (s *Service) newImportJob(actorID string, target *ImportTarget, bundleSha256 string) *model.ImportJob {
	now := mmmodel.GetMillis()
	job := &model.ImportJob{
		Id:                 mmmodel.NewId(),
		ActorId:            actorID,
		TeamId:             target.TeamID,
		TargetKind:         target.Kind,
		TargetSpaceId:      target.SpaceID,
		TargetSpaceExisted: target.Existed,
		BundleSha256:       bundleSha256,
		CreateAt:           now,
		UpdateAt:           now,
		RetainUntil:        now + unconfirmedJobRetentionMillis,
	}
	if target.Kind == model.ImportTargetNew {
		// A new Space has exactly one possible source identity, so there is nothing for the user to
		// choose: the source is created during execution and the job goes straight to preflight.
		job.SourceSelectionMode = model.ImportSourceModeNew
		job.SelectedImportSourceId = mmmodel.NewId()
		job.State = model.ImportStateQueuedPreflight
	} else {
		// An existing Space may already hold several import sources, so the user must pick one (or
		// ask for a new identity) before mappings can be consulted.
		job.State = model.ImportStateAwaitingSource
	}
	return job
}

// stagedPageRecord converts one streamed page into its staging row.
func stagedPageRecord(jobID string, p *importer.StagedPage) *model.ImportStagedPage {
	return &model.ImportStagedPage{
		JobId:                     jobID,
		Ordinal:                   p.Ordinal,
		SourceLine:                p.SourceLine,
		ExternalId:                p.ExternalID,
		ParentExternalId:          p.ParentExternalID,
		SourceOrdinal:             p.SourceOrdinal,
		Restricted:                p.Restricted,
		Title:                     p.Title,
		CanonicalBody:             p.CanonicalBody,
		SearchText:                p.SearchText,
		SourceUserProposal:        p.SourceUserProposal,
		SourceAuthorAccountId:     p.SourceAuthorAccountID,
		SourceCreateAt:            p.SourceCreateAt,
		SourceUpdateAt:            p.SourceUpdateAt,
		SourceProps:               mmmodel.StringInterface(p.SourceProps),
		IncomingSourceContentHash: p.IncomingSourceContentHash,
	}
}

// inspectionIssueRecord converts one streamed inspection finding into its issue row. Ordinals follow
// the inspector's deterministic emission order within the independent inspection stage.
func inspectionIssueRecord(jobID string, ordinal int, i *importer.InspectionIssue) *model.ImportIssueRecord {
	entityType := ""
	if i.ExternalID != "" {
		entityType = model.ImportEntityTypePage
	}
	return &model.ImportIssueRecord{
		JobId:       jobID,
		Stage:       model.ImportStageInspection,
		Ordinal:     ordinal,
		Severity:    model.ImportIssueSeverity(i.Severity),
		Code:        i.Code,
		EntityType:  entityType,
		ExternalId:  i.ExternalID,
		Title:       i.Title,
		Message:     i.Message,
		Remediation: i.Remediation,
		Details:     mmmodel.StringInterface(i.Details),
	}
}

// bundleSummaryOf projects the inspection summary onto the API-safe bundle summary persisted on the
// job, so it survives staged-body cleanup and can be rebuilt without re-reading the archive.
func bundleSummaryOf(summary *importer.InspectionSummary) model.ImportBundleSummary {
	return model.ImportBundleSummary{
		Version: summary.Version,
		Source: model.ImportReportSource{
			OrganizationId: summary.OrganizationID,
			SpaceKey:       summary.SpaceKey,
			SpaceName:      summary.SpaceName,
		},
		SpaceDefaults: model.ImportSpaceDefaults{
			Title:       summary.SpaceTitle,
			Description: summary.SpaceDescription,
		},
		Counts: model.ImportBundleCounts{
			Pages:                   summary.PageCount,
			Comments:                summary.CommentCount,
			Attachments:             summary.AttachmentCount,
			RestrictedManifestTotal: summary.Restricted.ManifestTotal,
			RestrictedEmittedPages:  summary.Restricted.EmittedPages,
			RestrictedManifestOnly:  summary.Restricted.ManifestOnly,
		},
	}
}

// importStagingAppError maps a staging failure onto its HTTP contract. An inspection rejection that
// surfaced through the transaction keeps its stable importer code; an admission failure becomes 429.
func (s *Service) importStagingAppError(actorID string, target *ImportTarget, bundleSha256 string, err error) *mmmodel.AppError {
	if importFailureCode(err) != "" {
		s.logImportRejected(actorID, target, bundleSha256, err)
		return importFailureAppError("CreateImportFromBundle", err)
	}
	if store.IsErrAdmissionExhausted(err) {
		var admission *store.ErrAdmissionExhausted
		_ = errors.As(err, &admission)
		s.log.Warn("Import upload rejected: admission exhausted",
			"actor_id", actorID, "team_id", target.TeamID, "target_space_id", target.SpaceID,
			"bundle_sha256", bundleSha256, "limit", admission.Limit)
		return mmmodel.NewAppError("CreateImportFromBundle", "app.import.admission_exhausted.app_error",
			map[string]any{"Limit": admission.Limit}, "", http.StatusTooManyRequests).Wrap(err)
	}
	s.log.Error("Import upload rejected: failed to persist job and staging",
		"actor_id", actorID, "team_id", target.TeamID, "target_space_id", target.SpaceID, "err", err)
	return storeAppError("CreateImportFromBundle", err)
}

// logImportRejected writes the operator-facing rejection line, without any bundle content.
func (s *Service) logImportRejected(actorID string, target *ImportTarget, bundleSha256 string, err error) {
	s.log.Warn("Import upload rejected: bundle inspection failed",
		"actor_id", actorID, "team_id", target.TeamID, "target_space_id", target.SpaceID,
		"bundle_sha256", bundleSha256, "code", importFailureCode(err))
}

// importContentLimitCodes are the inspection/archive failure codes that mean "this is a structurally
// valid bundle whose content exceeds a Docs limit". They map to 422 rather than 400 so a client can
// distinguish a malformed bundle from an oversized one.
var importContentLimitCodes = map[string]struct{}{
	importer.InspectErrTooManyPages:     {},
	importer.InspectErrDepthExceeded:    {},
	importer.InspectErrPageTitleTooLong: {},
	importer.TipTapErrTooManyNodes:      {},
	importer.TipTapErrTooDeep:           {},
	importer.TipTapErrBodyTooLarge:      {},
	importer.TipTapErrSearchTooLarge:    {},
	importer.InspectErrSpaceNameTooLong: {},
	importer.InspectErrSpaceTextTooLong: {},
	// TipTapErrSanitizerRejected is deliberately absent: content the sanitizer's allowlist refuses is
	// malformed, not oversized, so it stays a 400. The sanitizer's own size and nesting rejections are
	// translated into the specific codes above rather than collapsing into it.
}

// importArchiveSizeCodes are the failure codes that mean "the upload itself is too large", which the
// error contract maps to 413.
var importArchiveSizeCodes = map[string]struct{}{
	importer.ArchiveErrTooLarge:         {},
	importer.ArchiveErrManifestTooLarge: {},
	importer.ArchiveErrJSONLTooLarge:    {},
	importer.ArchiveErrTooManyEntries:   {},
	importer.InspectErrLineTooLong:      {},
}

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
// A TipTap error is checked before the inspection error that may wrap it, because it is the more
// specific of the two: inspection reports every content rejection as page_content_invalid, which cannot
// distinguish an over-limit document (422) from a malformed one (400). Taking the innermost code keeps
// that distinction — and keeps the TipTap entries in importContentLimitCodes reachable instead of dead.
func importFailureCode(err error) string {
	var archiveErr *importer.ArchiveError
	if errors.As(err, &archiveErr) {
		return archiveErr.Code
	}
	var tiptapErr *importer.TipTapError
	if errors.As(err, &tiptapErr) {
		return tiptapErr.Code
	}
	var inspectErr *importer.InspectError
	if errors.As(err, &inspectErr) {
		return inspectErr.Code
	}
	return ""
}

// GetImportJob returns the actor's own job. Job visibility is actor-only in V1, and another user's
// job is reported as not found rather than forbidden so the endpoint cannot be used to probe for the
// existence of someone else's import.
//
// Ownership alone is not sufficient to see the whole job: an actor who has since been deactivated or
// lost access to the target team/Space gets a minimal projection carrying only the fields they need
// to understand that the import stopped (id, state, error code, timestamps). Everything target- or
// source-identifying is omitted, because a job record must not become a way to read Space metadata
// after losing access to that Space.
func (s *Service) GetImportJob(jobID, actorID string) (*model.ImportJobView, *mmmodel.AppError) {
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, appErr
	}
	entitled, appErr := s.actorStillEntitled(job, actorID)
	if appErr != nil {
		return nil, appErr
	}
	if !entitled {
		return minimalImportJobView(job), nil
	}
	return s.BuildImportJobView(job)
}

// actorStillEntitled reports whether the actor remains an active user who may still reach the job's
// target. It mirrors the gate the upload passed: current Space membership for an existing target, or
// active team membership plus Space-creation permission for a new one. A backend failure is returned
// as an error rather than silently downgrading the response, so a transient outage never looks like
// revoked access.
func (s *Service) actorStillEntitled(job *model.ImportJob, actorID string) (bool, *mmmodel.AppError) {
	if appErr := s.requireClient("actorStillEntitled", "actor_id", actorID); appErr != nil {
		return false, appErr
	}
	user, err := s.client.User.Get(actorID)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return false, nil
		}
		return false, mmmodel.NewAppError("actorStillEntitled", "app.import.entitlement.lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	if user == nil || user.DeleteAt != 0 {
		return false, nil
	}

	// Which gate applies follows whether the target Space exists, not what was true at upload: a new-Space job
	// that has since provisioned its Space must be judged on membership of that Space, or an actor removed from
	// it would keep reading target- and source-identifying fields out of the job record.
	targeted, targetErr := s.importTargetSpaceExists(job)
	if targetErr != nil {
		return false, mmmodel.NewAppError("actorStillEntitled", "app.import.entitlement.lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(targetErr)
	}
	if targeted {
		// CheckSpaceMembership covers both halves of the access gate (active team member plus backing
		// channel member) and yields 403 for any failure, which here simply means "not entitled".
		if _, spaceErr := s.CheckSpaceMembership(job.TargetSpaceId, actorID, false); spaceErr != nil {
			if spaceErr.StatusCode == http.StatusForbidden || spaceErr.StatusCode == http.StatusNotFound {
				return false, nil
			}
			return false, spaceErr
		}
		return true, nil
	}

	// Before provisioning there is no Space to check, so the entitlement is the one that authorized the upload.
	active, memberErr := s.isActiveTeamMember(job.TeamId, actorID)
	if memberErr != nil {
		return false, mmmodel.NewAppError("actorStillEntitled", "app.import.entitlement.lookup_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(memberErr)
	}
	if !active {
		return false, nil
	}
	return s.client.User.HasPermissionToTeam(actorID, job.TeamId, mmmodel.PermissionCreatePublicChannel), nil
}

// minimalImportJobView is the access-loss projection: enough for the owning actor to see that their
// import exists and why it stopped, with no target, source, bundle, or report detail.
func minimalImportJobView(job *model.ImportJob) *model.ImportJobView {
	view := &model.ImportJobView{
		Id:                       job.Id,
		State:                    job.State,
		SourceCandidates:         []model.ImportSourceCandidate{},
		RequiredAcknowledgements: []string{},
		CreateAt:                 job.CreateAt,
		UpdateAt:                 job.UpdateAt,
		FinishedAt:               job.FinishedAt,
	}
	if job.ErrorCode != "" {
		view.Error = &model.ImportPublicError{Code: job.ErrorCode}
	}
	return view
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

	// Entitlement cannot be expressed in SQL — it depends on live team and channel membership — so the
	// page is assembled by scanning store rows and filtering, rather than by filtering a page that was
	// already cut. Filtering after the cut is what produces sparse pages and a has_more computed from
	// rows the caller never sees: a request for 20 could return 3 alongside has_more=false while entitled
	// jobs sat just past the offset.
	//
	// Positions are therefore counted in *entitled* rows: offset entitled rows are skipped, then up to
	// limit (perPage+1) are collected, so the probe row means what it means everywhere else in this
	// repository.
	views := make([]*model.ImportJobView, 0, limit)
	// One entitlement answer per distinct target, not per row. Entitlement depends only on the target a job
	// points at, and one actor's jobs point at very few distinct targets — so memoizing turns a scan of
	// thousands of rows into a handful of membership lookups, which is what makes a scan cap generous enough to
	// reach a long history affordable at all.
	entitled := map[string]bool{}
	skipped, scanned, storeOffset := 0, 0, 0
	for len(views) < limit && scanned < importListScanLimit {
		jobs, err := s.store.GetImportJobsForActor(actorID, teamID, storeOffset, importListScanBatch)
		if err != nil {
			return nil, false, storeAppError("GetImportJobsForActor", err)
		}
		if len(jobs) == 0 {
			break
		}
		storeOffset += len(jobs)
		scanned += len(jobs)

		for _, job := range jobs {
			// Jobs whose target the actor can no longer reach are omitted entirely rather than downgraded:
			// a list is a discovery surface, and a placeholder row would still disclose that an import
			// exists for a Space the caller has lost access to.
			key := importTargetKey(job)
			allowed, known := entitled[key]
			if !known {
				var appErr *mmmodel.AppError
				allowed, appErr = s.actorStillEntitled(job, actorID)
				if appErr != nil {
					return nil, false, appErr
				}
				entitled[key] = allowed
			}
			if !allowed {
				continue
			}
			if skipped < offset {
				skipped++
				continue
			}
			// The list view omits source candidates: they are a per-job interactive concern, and querying
			// them per row would turn one listing into N queries.
			views = append(views, buildImportJobViewWithoutCandidates(job))
			if len(views) == limit {
				break
			}
		}
		if len(jobs) < importListScanBatch {
			break // the store is exhausted
		}
	}

	views, hasMore := trimPage(views, limit)
	if scanned >= importListScanLimit && len(views) == 0 {
		// The cap was reached without finding a single row for this page. Reporting has-more here would be a
		// promise this endpoint cannot keep: every subsequent page restarts the same scan, hits the same cap, and
		// returns empty again — a client that trusts the flag pages forever and never reaches anything. Saying
		// "no more" is the honest answer for a request this far past what an offset-paged scan can reach.
		s.log.Warn("Import job listing hit its scan cap with nothing to return; jobs past this depth are unreachable by offset",
			"actor_id", actorID, "team_id", teamID, "offset", offset, "scanned", scanned)
		return views, false, nil
	}
	if scanned >= importListScanLimit && !hasMore {
		// The page filled short of the cap's reach, so unexamined rows may remain.
		return views, true, nil
	}
	return views, hasMore, nil
}

// importTargetKey identifies the target a job points at, for the entitlement memo. It includes the kind because
// a pre-provisioning new-Space job is judged on its team while every other job is judged on its Space.
func importTargetKey(job *model.ImportJob) string {
	if job.TargetSpaceExisted {
		return "space:" + job.TargetSpaceId
	}
	return "new:" + job.TeamId + ":" + job.TargetSpaceId
}

// Bounds for the entitlement-filtered job listing. The scan is capped so a caller deep into a long history of
// jobs they can no longer reach cannot trigger an unbounded sweep. With the per-target entitlement memo above,
// a scanned row costs one query row rather than a membership lookup, so the cap can be the store's own
// unpaginated ceiling instead of the few hundred rows a per-row lookup would have made affordable.
const (
	importListScanBatch = 200
	importListScanLimit = store.MaxRowsPerQuery
)

// GetImportIssues returns one page of a job's persisted issues, for the actor that owns the job.
func (s *Service) GetImportIssues(jobID, actorID, stage, severity string, page, perPage int) ([]*model.ImportIssue, bool, *mmmodel.AppError) {
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, false, appErr
	}
	// Issues name pages, titles, and local IDs from the target Space, so losing access to that target
	// hides them entirely. 404 rather than 403, matching how another user's job reads.
	entitled, appErr := s.actorStillEntitled(job, actorID)
	if appErr != nil {
		return nil, false, appErr
	}
	if !entitled {
		return nil, false, mmmodel.NewAppError("GetImportIssues", "app.store.not_found.app_error", nil, "", http.StatusNotFound)
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
	if stage != "" && !model.ImportIssueStage(stage).IsValid() {
		return mmmodel.NewAppError("validateIssueFilters", "app.import.issues.invalid_stage.app_error", nil, "", http.StatusBadRequest)
	}
	if severity != "" && !model.ImportIssueSeverity(severity).IsValid() {
		return mmmodel.NewAppError("validateIssueFilters", "app.import.issues.invalid_severity.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// importIssueViewOf projects a persisted issue row onto the report-facing issue shape.
func importIssueViewOf(r *model.ImportIssueRecord) *model.ImportIssue {
	issue := &model.ImportIssue{
		Stage:       string(r.Stage),
		Severity:    string(r.Severity),
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
// omits the provisioned channel id, lease/accounting internals, raw confirmation, persisted
// baselines, and internal error messages: only the stable error code is exposed.
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
		Bundle:           job.BundleSummary,
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
	if job.PreflightRevision != "" {
		view.Preflight = importPreflightReportSummary(job)
	}
	if job.State.IsTerminal() {
		// A terminal job's report is the whole point of retaining it for ninety days, so its summary is
		// projected even when nothing ran: a canceled job still reports which pages it held and that none
		// were attempted.
		view.Final = importFinalReportSummary(job)
	}
	view.RequiredAcknowledgements = requiredAcknowledgements(job)
	return view
}

// importPreflightReportSummary projects the persisted preflight summary for the wizard.
//
// The revision is exposed here because the client must echo it back to confirm, and it is the only piece
// of the internal job model a client legitimately needs: it names *which* plan was reviewed, without
// revealing any of the baselines that make applying that plan safe.
func importPreflightReportSummary(job *model.ImportJob) *model.ImportReportSummary {
	summary := job.PreflightSummary
	return &model.ImportReportSummary{
		Stage:       string(model.ImportStagePreflight),
		GeneratedAt: job.UpdateAt,
		Fidelity:    model.NewImportFidelity(),
		Revision:    job.PreflightRevision,
		Counts: model.ImportReportCounts{
			Pages:                   summary.Manifest.Pages,
			Comments:                summary.Manifest.Comments,
			Attachments:             summary.Manifest.Attachments,
			RestrictedManifestTotal: summary.Manifest.RestrictedManifestTotal,
			RestrictedEmittedPages:  summary.Manifest.RestrictedEmittedPages,
			RestrictedManifestOnly:  summary.Manifest.RestrictedManifestOnly,
			Actions:                 importActionCountsMap(summary.Actions),
			Authors: map[string]int{
				"mapped":            summary.Authors.Mapped,
				"fallback_to_actor": summary.Authors.FallbackToActor,
			},
		},
	}
}

// importFinalReportSummary projects the persisted final summary. Unlike the preflight summary it carries
// no revision: there is nothing left to confirm, and outcomes rather than planned actions are what a
// reader of a finished import needs.
func importFinalReportSummary(job *model.ImportJob) *model.ImportReportSummary {
	summary := job.FinalSummary
	return &model.ImportReportSummary{
		Stage:       string(model.ImportStageExecution),
		GeneratedAt: job.FinishedAt,
		Fidelity:    model.NewImportFidelity(),
		Counts: model.ImportReportCounts{
			Pages:                   summary.Manifest.Pages,
			Comments:                summary.Manifest.Comments,
			Attachments:             summary.Manifest.Attachments,
			RestrictedManifestTotal: summary.Manifest.RestrictedManifestTotal,
			RestrictedEmittedPages:  summary.Manifest.RestrictedEmittedPages,
			RestrictedManifestOnly:  summary.Manifest.RestrictedManifestOnly,
			Actions:                 importActionCountsMap(summary.Actions),
			Outcomes:                summary.Outcomes,
			Authors: map[string]int{
				"mapped":            summary.Authors.Mapped,
				"fallback_to_actor": summary.Authors.FallbackToActor,
			},
		},
	}
}

// importActionCountsMap flattens the typed action counts into the report's map shape, omitting zeros so a
// reader sees only what the plan actually does.
func importActionCountsMap(actions model.ImportActionCounts) map[string]int {
	out := map[string]int{}
	for action, count := range map[model.ImportAction]int{
		model.ImportActionCreate:        actions.Create,
		model.ImportActionUpdate:        actions.Update,
		model.ImportActionNoop:          actions.Noop,
		model.ImportActionPreserveLocal: actions.PreserveLocal,
		model.ImportActionConflict:      actions.Conflict,
		model.ImportActionBlocked:       actions.Blocked,
		model.ImportActionStale:         actions.Stale,
		model.ImportActionNotAttempted:  actions.NotAttempted,
	} {
		if count > 0 {
			out[string(action)] = count
		}
	}
	return out
}

// requiredAcknowledgements lists the confirmation acknowledgements a job currently demands. Before
// preflight the reimport acknowledgement is absent because the job's preflight action counts are still
// zero — whether anything is a reimport is only knowable once the bundle has been compared against
// existing mappings.
func requiredAcknowledgements(job *model.ImportJob) []string {
	return importRequiredAcknowledgements(job.TargetKind, job.BundleSummary.Counts, job.PreflightSummary.Actions)
}

// importRequiredAcknowledgements derives the acknowledgement set from the target, the bundle, and the
// planned actions. It is shared by the job view, the preflight revision digest, and confirmation
// validation so all three demand exactly the same set: a client told to set one key and then refused for
// missing another would be unable to proceed at all.
func importRequiredAcknowledgements(
	targetKind model.ImportTargetKind,
	counts model.ImportBundleCounts,
	actions model.ImportActionCounts,
) []string {
	acks := []string{}
	if targetKind == model.ImportTargetNew {
		acks = append(acks, model.ImportAckNewSpaceMetadata)
	}
	if counts.Comments > 0 || counts.Attachments > 0 {
		acks = append(acks, model.ImportAckPageOnlyPartial)
	}
	// Only restricted entries that intersect emitted pages can widen real access; manifest-only
	// entries are reported instead, so they must not demand an acknowledgement.
	if counts.RestrictedEmittedPages > 0 {
		acks = append(acks, model.ImportAckWidenRestricted)
	}
	// Any plan that touches a page which already exists — including one it deliberately leaves alone —
	// means the user is reimporting rather than importing fresh, and must say so.
	if actions.Update+actions.Noop+actions.PreserveLocal+actions.Conflict+actions.Stale > 0 {
		acks = append(acks, model.ImportAckReimportExisting)
	}
	return acks
}

// importSourceCandidates suggests existing ImportSources in the job's target Space that may be the
// same Confluence Space as the uploaded bundle, each with the reasons it matched. Scoring only orders
// the suggestions: selection is always explicit, because two Confluence instances can share an
// organization id, a space key, and a display name while being genuinely different sources.
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
		candidates = append(candidates, model.ImportSourceCandidate{
			ImportSourceId:   src.Id,
			DisplayName:      src.DisplayName,
			OrganizationId:   src.OrganizationId,
			ExternalSpaceKey: src.ExternalSpaceKey,
			MappedPageCount:  mapped[src.Id],
			LastImportAt:     src.LastImportAt,
			MatchReasons:     candidateMatchReasons(src, bundle),
		})
	}

	// Strongest match first, then most recently imported, then id — a stable total order so the UI
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

// displayNameSuggestsSource reports whether a source's user-chosen display name mentions the bundle's
// space key or name. This is a deliberately simple containment check: the display name is free text a
// user typed, so it is a hint for ordering, never evidence of identity.
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
