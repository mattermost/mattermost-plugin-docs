// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// importPreflightBatch is how many staged pages one preflight pass loads at a time. Each batch also
// bulk-loads the local pages its mapped entries point at, so this bounds resident page bodies rather
// than just row count — a five-thousand-page bundle of 2 MiB bodies would not fit otherwise.
const importPreflightBatch = 100

// Stable job-level error codes recorded when preflight cannot complete.
const (
	// ImportErrorPreflightFailed marks a job that failed during preflight computation.
	ImportErrorPreflightFailed = "preflight_failed"
	// ImportErrorSourceMissing marks a job whose selected ImportSource no longer exists.
	ImportErrorSourceMissing = "selected_source_missing"
)

// RunImportPreflight computes and publishes one job's preflight.
//
// The computation runs outside any lock and is published all-or-nothing against the mapping revision it
// was computed from. That pairing is what lets a five-thousand-page preflight take its time without
// blocking other work, while still refusing to publish a plan whose inputs moved underneath it — the job
// simply returns to the queue and recomputes.
func (s *Service) RunImportPreflight(jobID string) error {
	job, mappingRevision, err := s.store.BeginImportPreflight(jobID)
	if err != nil {
		if store.IsErrConflict(err) || store.IsErrNotFound(err) {
			// The job advanced or vanished between work selection and this transition. The CAS losing is
			// the intended outcome, not an error worth failing the job over.
			s.log.Debug("Import preflight skipped: job is no longer queued", "job_id", jobID, "err", err)
			return nil
		}
		return errors.Wrap(err, "begin import preflight")
	}

	publication, err := s.computeImportPreflight(job, mappingRevision)
	if err != nil {
		s.log.Error("Import preflight failed",
			"job_id", job.Id, "actor_id", job.ActorId, "target_space_id", job.TargetSpaceId, "err", err)
		code := ImportErrorPreflightFailed
		if store.IsErrNotFound(err) {
			code = ImportErrorSourceMissing
		}
		if _, termErr := s.store.EnterImportTerminalizing(job.Id, model.ImportIntentFailed, code); termErr != nil {
			return errors.Wrap(termErr, "terminalize after failed preflight")
		}
		return nil
	}

	published, err := s.store.PublishImportPreflight(publication)
	if err != nil {
		if store.IsErrPreflightStale(err) {
			// Another job changed this source's mappings mid-computation. Nothing is published and the job
			// is left queued, so the next pass recomputes against the new revision.
			s.log.Info("Import preflight discarded: source mappings changed during computation",
				"job_id", job.Id, "mapping_revision", mappingRevision)
			return s.requeueImportPreflight(job.Id)
		}
		if store.IsErrConflict(err) {
			s.log.Debug("Import preflight not published: job state changed", "job_id", job.Id, "err", err)
			return nil
		}
		return errors.Wrap(err, "publish import preflight")
	}

	s.log.Info("Import preflight published",
		"job_id", published.Id, "actor_id", published.ActorId, "target_space_id", published.TargetSpaceId,
		"preflight_revision", published.PreflightRevision, "mapping_revision", published.PreflightMappingRevision,
		"pages", len(publication.Plans), "results", len(publication.Results), "issues", len(publication.Issues),
		"create", publication.Summary.Actions.Create, "update", publication.Summary.Actions.Update,
		"noop", publication.Summary.Actions.Noop, "preserve_local", publication.Summary.Actions.PreserveLocal,
		"conflict", publication.Summary.Actions.Conflict, "blocked", publication.Summary.Actions.Blocked,
		"stale", publication.Summary.Actions.Stale)
	return nil
}

// requeueImportPreflight returns a job to the preflight queue after its inputs changed.
func (s *Service) requeueImportPreflight(jobID string) error {
	if err := s.store.RequeueImportPreflight(jobID); err != nil {
		if store.IsErrConflict(err) || store.IsErrNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "requeue import preflight")
	}
	return nil
}

// preflightState is the accumulating result of one computation.
type preflightState struct {
	job             *model.ImportJob
	mappingRevision int64
	// mappings is every retained mapping for the selected source, keyed by source external id.
	mappings map[string]*importer.MappingBaseline
	// seen records which mapped external ids the bundle still contains, so the rest are stale.
	seen map[string]struct{}
	// plannedIDs maps a staged external id to the local id its plan will use, so a child can be parented
	// under a sibling create from the same bundle.
	plannedIDs map[string]string
	// blocked records staged external ids whose plan cannot proceed, so their descendants are blocked too
	// rather than being silently rooted at the Space.
	blocked map[string]struct{}
	// authorCache resolves each distinct source account or username proposal once per job rather than
	// once per page: a five-thousand-page space typically has a handful of authors.
	authorCache map[string]resolvedAuthor
	// userProposals maps a source account id to the manifest's username proposal.
	userProposals map[string]string

	plans   []store.ImportStagedPagePlan
	results []*model.ImportResultRecord
	issues  []*model.ImportIssueRecord
	actions model.ImportActionCounts
	authors model.ImportAuthorCounts

	// creates records, per intended local parent, how many new pages the plan would add, for the
	// projected sibling-capacity check.
	createsPerParent map[string]int
	// createDepthNeeded is the set of existing local parent ids new pages would be added under, whose
	// depth must be projected.
	createDepthNeeded map[string]int
	// createParentByOrdinal records each planned create's intended local parent. It is not a persisted
	// column — an existing page's PreflightCurrentParentId means the parent it already has, which a create
	// by definition does not — so the projection keeps its own map.
	createParentByOrdinal map[int]string
}

// resolvedAuthor is one author resolution outcome.
type resolvedAuthor struct {
	userID string
	reason string
}

// computeImportPreflight builds the complete preflight for a job without writing anything.
func (s *Service) computeImportPreflight(job *model.ImportJob, mappingRevision int64) (*store.ImportPreflightPublication, error) {
	st := &preflightState{
		job:                   job,
		mappingRevision:       mappingRevision,
		mappings:              map[string]*importer.MappingBaseline{},
		seen:                  map[string]struct{}{},
		plannedIDs:            map[string]string{},
		blocked:               map[string]struct{}{},
		authorCache:           map[string]resolvedAuthor{},
		userProposals:         map[string]string{},
		createsPerParent:      map[string]int{},
		createDepthNeeded:     map[string]int{},
		createParentByOrdinal: map[int]string{},
	}

	users, err := s.store.GetImportManifestUsers(job.Id)
	if err != nil {
		return nil, errors.Wrap(err, "load manifest users")
	}
	for _, u := range users {
		// Durable manifest rows are the authority here, never the upload request's in-memory manifest: a
		// restart must resolve authors identically to the original pass.
		st.userProposals[u.AccountId] = u.MattermostUsername
	}

	if job.SourceSelectionMode == model.ImportSourceModeExisting && job.SelectedImportSourceId != "" {
		entities, entErr := s.store.GetImportEntitiesForSource(job.SelectedImportSourceId)
		if entErr != nil {
			return nil, errors.Wrap(entErr, "load import mappings")
		}
		if len(entities) > model.ImportMaxMappingsPerSource {
			// The read asks for one row past the cap precisely so this is detectable rather than silently
			// truncated: classifying against a partial mapping set would misreport existing pages as creates.
			return nil, errors.Errorf("import source %s holds more than %d mappings",
				job.SelectedImportSourceId, model.ImportMaxMappingsPerSource)
		}
		for _, e := range entities {
			st.mappings[e.ExternalId] = &importer.MappingBaseline{
				ExternalID:                 e.ExternalId,
				LocalID:                    e.LocalId,
				LastSourceContentHash:      e.LastSourceContentHash,
				LastAppliedContentHash:     e.LastAppliedContentHash,
				LastAppliedParentID:        e.LastAppliedParentId,
				LastSourceParentExternalID: e.LastSourceParentExternalId,
				LastSourceOrdinal:          e.LastSourceOrdinal,
			}
		}
	}

	if err = s.classifyStagedPages(st); err != nil {
		return nil, err
	}
	if err = s.applyStructuralProjections(st); err != nil {
		return nil, err
	}
	s.appendStaleResults(st)

	summary := model.ImportPreflightSummary{
		Manifest: job.BundleSummary.Counts,
		Actions:  st.actions,
		Authors:  st.authors,
	}
	revision, err := importPreflightRevision(st, summary)
	if err != nil {
		return nil, err
	}
	return &store.ImportPreflightPublication{
		JobID:           job.Id,
		MappingRevision: mappingRevision,
		Plans:           st.plans,
		Results:         st.results,
		Issues:          st.issues,
		Summary:         summary,
		Revision:        revision,
	}, nil
}

// classifyStagedPages walks the job's staged pages in ordinal order, deciding each one.
//
// Ordinal order matters: the producer emits parents before children, so by the time a child is reached
// its source parent has either produced a planned id or been recorded as blocked. That is what makes
// parent availability answerable in a single pass.
func (s *Service) classifyStagedPages(st *preflightState) error {
	for offset := 0; ; offset += importPreflightBatch {
		pages, err := s.store.GetImportStagedPages(st.job.Id, offset, importPreflightBatch)
		if err != nil {
			return errors.Wrap(err, "load staged pages")
		}
		if len(pages) == 0 {
			return nil
		}

		locals, err := s.loadLocalStateForBatch(st, pages)
		if err != nil {
			return err
		}
		for _, page := range pages {
			if err = s.classifyOneStagedPage(st, page, locals); err != nil {
				return err
			}
		}
		if len(pages) < importPreflightBatch {
			return nil
		}
	}
}

// loadLocalStateForBatch bulk-loads the current local pages that this batch's mappings point at.
func (s *Service) loadLocalStateForBatch(st *preflightState, pages []*model.ImportStagedPage) (map[string]*store.ImportLocalPage, error) {
	ids := make([]string, 0, len(pages))
	for _, page := range pages {
		if mapping, ok := st.mappings[page.ExternalId]; ok && mapping.LocalID != "" {
			ids = append(ids, mapping.LocalID)
		}
	}
	locals, err := s.store.GetImportLocalPages(ids)
	if err != nil {
		return nil, errors.Wrap(err, "load local pages")
	}
	return locals, nil
}

// classifyOneStagedPage decides one page and records its plan, result, and issues.
func (s *Service) classifyOneStagedPage(st *preflightState, page *model.ImportStagedPage, locals map[string]*store.ImportLocalPage) error {
	sourceProps := map[string]any(page.SourceProps)
	incomingHash, err := importer.HashSourceContent(importer.SourceContentHashInput{
		Title:           page.Title,
		CanonicalBody:   page.CanonicalBody,
		AuthorAccountID: page.SourceAuthorAccountId,
		AuthorProposal:  st.userProposals[page.SourceAuthorAccountId],
		SourceCreateAt:  page.SourceCreateAt,
		SourceUpdateAt:  page.SourceUpdateAt,
		SourceProps:     importer.BuildDocsImportProps(importer.DocsImportInput{SourceProps: sourceProps})[importer.DocsImportKeySourceProps].(map[string]any),
	})
	if err != nil {
		return errors.Wrap(err, "hash source content")
	}

	author := s.resolveImportAuthor(st, page)
	mapping := st.mappings[page.ExternalId]
	if mapping != nil {
		st.seen[page.ExternalId] = struct{}{}
	}

	local := s.localStateFor(mapping, locals)
	parentAvailable, parentLocalID := st.parentAvailability(page, mapping)

	classification := importer.Classify(importer.ClassifyInput{
		IncomingSourceContentHash: incomingHash,
		IncomingParentExternalID:  page.ParentExternalId,
		IncomingSourceOrdinal:     page.SourceOrdinal,
		TargetSpaceID:             st.job.TargetSpaceId,
		Mapping:                   mapping,
		Local:                     local,
		ParentAvailable:           parentAvailable,
		MappingCapacityExceeded:   st.mappingCapacityExceeded(mapping),
	})

	plannedPageID := classification.LocalID
	switch {
	case classification.Action == model.ImportActionCreate:
		// The planned id is generated now and execution must use exactly it, so link rewriting and child
		// parenting can refer to a page that does not exist yet.
		plannedPageID = mmmodel.NewId()
		st.plannedIDs[page.ExternalId] = plannedPageID
		st.createsPerParent[parentLocalID]++
		st.createParentByOrdinal[page.Ordinal] = parentLocalID
		if parentLocalID != "" {
			st.createDepthNeeded[parentLocalID] = 0
		}
	case classification.Action == model.ImportActionBlocked:
		st.blocked[page.ExternalId] = struct{}{}
	case mapping != nil:
		// An existing page keeps its own local id available for descendants, whatever the content decision.
		st.plannedIDs[page.ExternalId] = mapping.LocalID
	}

	st.recordPlan(page, classification, plannedPageID, author, incomingHash, mapping, local)
	st.recordResult(page, classification, plannedPageID)
	st.recordIssues(page, classification, author)
	st.countAction(classification.Action)
	return nil
}

// localStateFor projects a loaded local page into the classifier's view, computing its applied-content
// hash. A body that cannot be canonicalized is hashed as opaque rather than failing the preflight: an
// opaque hash compares unequal to any canonical baseline, so the page reads as a definite local edit and
// is protected instead of overwritten.
func (s *Service) localStateFor(mapping *importer.MappingBaseline, locals map[string]*store.ImportLocalPage) importer.LocalPageState {
	if mapping == nil || mapping.LocalID == "" {
		return importer.LocalPageState{}
	}
	page, ok := locals[mapping.LocalID]
	if !ok {
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
		// Hashing a decoded map cannot fail in practice; treating it as an opaque mismatch keeps the page
		// protected rather than letting an unexpected error read as "unchanged".
		s.log.Warn("Could not hash local page content; treating it as locally edited", "page_id", page.Id, "err", err)
		state.AppliedContentHash = ""
		state.BodyIsCanonical = false
		return state
	}
	state.AppliedContentHash = hash
	return state
}

// parentAvailability reports whether a staged page's source parent resolves to something the import can
// parent under, and the local parent id it would use ("" meaning the Space root).
func (st *preflightState) parentAvailability(page *model.ImportStagedPage, mapping *importer.MappingBaseline) (bool, string) {
	if mapping != nil {
		// An existing page keeps its current local parent in V1, so its own parent is never in question.
		return true, mapping.LastAppliedParentID
	}
	if page.ParentExternalId == "" {
		return true, "" // a source root becomes a Space root
	}
	if _, isBlocked := st.blocked[page.ParentExternalId]; isBlocked {
		return false, ""
	}
	if localID, ok := st.plannedIDs[page.ParentExternalId]; ok && localID != "" {
		return true, localID
	}
	return false, ""
}

// mappingCapacityExceeded reports whether adopting one more page would push the selected source past its
// retained-mapping cap. Existing mappings are already counted, so only new pages can breach it.
func (st *preflightState) mappingCapacityExceeded(mapping *importer.MappingBaseline) bool {
	if mapping != nil {
		return false
	}
	return len(st.mappings)+len(st.plannedIDs) >= model.ImportMaxMappingsPerSource
}

// recordPlan stores the reviewed baseline for one page. Every hash and parent recorded here is what
// execution rechecks under locks before applying anything.
func (st *preflightState) recordPlan(
	page *model.ImportStagedPage,
	classification importer.Classification,
	plannedPageID string,
	author resolvedAuthor,
	incomingHash string,
	mapping *importer.MappingBaseline,
	local importer.LocalPageState,
) {
	plan := store.ImportStagedPagePlan{
		Ordinal:                     page.Ordinal,
		PlannedAction:               classification.Action,
		PlannedPageId:               plannedPageID,
		ResolvedUserId:              author.userID,
		AuthorFallbackReason:        author.reason,
		IncomingSourceContentHash:   incomingHash,
		PreflightCurrentContentHash: local.AppliedContentHash,
		PreflightCurrentParentId:    local.ParentID,
	}
	if mapping != nil {
		plan.PreflightMappingContentHash = mapping.LastAppliedContentHash
		plan.PreflightMappingParentId = mapping.LastAppliedParentID
	}
	st.plans = append(st.plans, plan)
}

// recordResult appends the typed review row for one page.
func (st *preflightState) recordResult(page *model.ImportStagedPage, classification importer.Classification, plannedPageID string) {
	now := mmmodel.GetMillis()
	localID := classification.LocalID
	if classification.Action == model.ImportActionCreate {
		// A create's planned id is reported so the wizard and link analysis can refer to the page before it
		// exists. It is a plan, not a claim that the page is there.
		localID = plannedPageID
	}
	st.results = append(st.results, &model.ImportResultRecord{
		JobId:         st.job.Id,
		Stage:         model.ImportStagePreflight,
		Ordinal:       page.Ordinal,
		EntityType:    model.ImportEntityTypePage,
		ExternalId:    page.ExternalId,
		LocalId:       localID,
		Title:         page.Title,
		PlannedAction: classification.Action,
		Outcome:       importer.OutcomeForPlannedAction(classification.Action),
		Details:       resultDetails(classification),
		CreateAt:      now,
		UpdateAt:      now,
	})
}

// resultDetails carries the small, typed extras the review projection needs beyond the columns.
func resultDetails(classification importer.Classification) mmmodel.StringInterface {
	details := mmmodel.StringInterface{}
	if classification.OverwriteEligible {
		details["overwrite_eligible"] = true
	}
	if len(classification.Issues) > 0 {
		details["structural_changes"] = structuralChangeCodes(classification.Issues)
	}
	return details
}

// structuralChangeCodes filters a classification's issues down to the structural ones the wizard shows
// next to a page, so a reviewer sees "the source moved this, we kept your layout" without reading the
// whole issue list.
func structuralChangeCodes(issues []string) []string {
	structural := make([]string, 0, len(issues))
	for _, code := range issues {
		switch code {
		case importer.IssueSourceParentChangedNotApplied,
			importer.IssueLocalParentChangedPreserved,
			importer.IssueSourceOrderChangedNotApplied:
			structural = append(structural, code)
		}
	}
	return structural
}

// recordIssues appends the per-page issue rows explaining a decision.
//
// Ordinals are page-strided (Ordinal*ImportIssuesPerPage + index) so a page's issues stay adjacent and
// deterministic across recomputations, and the per-page count is capped so one pathological page cannot
// consume another page's stride.
func (st *preflightState) recordIssues(page *model.ImportStagedPage, classification importer.Classification, author resolvedAuthor) {
	codes := classification.Issues
	if author.reason != "" {
		codes = append(codes, importer.IssueAuthorFallbackToActor)
	}
	if len(codes) > model.ImportMaxIssueCodesPerPage {
		codes = codes[:model.ImportMaxIssueCodesPerPage]
	}
	for i, code := range codes {
		st.issues = append(st.issues, &model.ImportIssueRecord{
			JobId:       st.job.Id,
			Stage:       model.ImportStagePreflight,
			Ordinal:     page.Ordinal*model.ImportIssuesPerPage + i,
			Severity:    preflightIssueSeverity(code),
			Code:        code,
			EntityType:  model.ImportEntityTypePage,
			ExternalId:  page.ExternalId,
			LocalId:     classification.LocalID,
			Title:       page.Title,
			Message:     preflightIssueMessage(code),
			Remediation: preflightIssueRemediation(code),
			Details:     issueDetails(code, author),
		})
	}
}

// issueDetails adds the small extras a specific code needs.
func issueDetails(code string, author resolvedAuthor) mmmodel.StringInterface {
	if code == importer.IssueAuthorFallbackToActor && author.reason != "" {
		return mmmodel.StringInterface{"fallback_reason": author.reason, "resolved_user_id": author.userID}
	}
	return nil
}

// countAction tallies one decision into the summary.
func (st *preflightState) countAction(action model.ImportAction) {
	switch action {
	case model.ImportActionCreate:
		st.actions.Create++
	case model.ImportActionUpdate:
		st.actions.Update++
	case model.ImportActionNoop:
		st.actions.Noop++
	case model.ImportActionPreserveLocal:
		st.actions.PreserveLocal++
	case model.ImportActionConflict:
		st.actions.Conflict++
	case model.ImportActionBlocked:
		st.actions.Blocked++
	case model.ImportActionStale:
		st.actions.Stale++
	}
}

// appendStaleResults records every retained mapping the current bundle no longer contains, in
// deterministic external-id order.
//
// Stale entries are reported, never deleted: a page that left the Confluence space is still a real local
// page someone may be using, and V1 has no mandate to remove it.
func (s *Service) appendStaleResults(st *preflightState) {
	stale := make([]string, 0, len(st.mappings))
	for externalID := range st.mappings {
		if _, seen := st.seen[externalID]; !seen {
			stale = append(stale, externalID)
		}
	}
	sort.Strings(stale)

	now := mmmodel.GetMillis()
	for i, externalID := range stale {
		mapping := st.mappings[externalID]
		ordinal := model.ImportStaleOrdinalBase + i
		st.results = append(st.results, &model.ImportResultRecord{
			JobId:         st.job.Id,
			Stage:         model.ImportStagePreflight,
			Ordinal:       ordinal,
			EntityType:    model.ImportEntityTypePage,
			ExternalId:    externalID,
			LocalId:       mapping.LocalID,
			Title:         mapping.LastSourceTitle,
			PlannedAction: model.ImportActionStale,
			Outcome:       model.ImportOutcomeStale,
			CreateAt:      now,
			UpdateAt:      now,
		})
		st.issues = append(st.issues, &model.ImportIssueRecord{
			JobId:       st.job.Id,
			Stage:       model.ImportStagePreflight,
			Ordinal:     model.ImportJobIssueOrdinalBase + i,
			Severity:    model.ImportSeverityInfo,
			Code:        importer.IssueSourcePageStale,
			EntityType:  model.ImportEntityTypePage,
			ExternalId:  externalID,
			LocalId:     mapping.LocalID,
			Title:       mapping.LastSourceTitle,
			Message:     preflightIssueMessage(importer.IssueSourcePageStale),
			Remediation: preflightIssueRemediation(importer.IssueSourcePageStale),
		})
		st.actions.Stale++
	}
}

// applyStructuralProjections blocks planned creates that would breach the target's sibling or depth
// limits.
//
// It runs after content classification because capacity depends on how many creates the whole plan adds
// to each group, which is only known once every page has been classified. Execution rechecks both limits
// under locks: an interactive edit can consume the remaining room between review and execution, so this
// is a review-time projection rather than a guarantee.
func (s *Service) applyStructuralProjections(st *preflightState) error {
	if len(st.createsPerParent) == 0 {
		return nil
	}
	parents := make([]string, 0, len(st.createsPerParent))
	for parentID := range st.createsPerParent {
		parents = append(parents, parentID)
	}
	sort.Strings(parents)

	existingChildren, err := s.store.CountLivePageChildren(st.job.TargetSpaceId, parents)
	if err != nil {
		return errors.Wrap(err, "count target sibling groups")
	}
	depthParents := make([]string, 0, len(st.createDepthNeeded))
	for parentID := range st.createDepthNeeded {
		depthParents = append(depthParents, parentID)
	}
	sort.Strings(depthParents)
	depths, err := s.store.GetLivePageDepths(depthParents)
	if err != nil {
		return errors.Wrap(err, "project target depths")
	}

	overCapacity := map[string]struct{}{}
	for _, parentID := range parents {
		if existingChildren[parentID]+st.createsPerParent[parentID] > store.MaxPageSiblingsLimit {
			overCapacity[parentID] = struct{}{}
		}
	}
	overDepth := map[string]struct{}{}
	for parentID, depth := range depths {
		// A create sits one level below its parent, so the parent must itself be shallower than the limit.
		if depth+1 > model.MaxPageDepth {
			overDepth[parentID] = struct{}{}
		}
	}
	if len(overCapacity) == 0 && len(overDepth) == 0 {
		return nil
	}
	return s.blockCreatesUnder(st, overCapacity, overDepth)
}

// blockCreatesUnder rewrites the plan for creates whose target group cannot accept them.
func (s *Service) blockCreatesUnder(st *preflightState, overCapacity, overDepth map[string]struct{}) error {
	// The plan is indexed by ordinal, and results share that ordinal, so both are rewritten together.
	resultByOrdinal := make(map[int]*model.ImportResultRecord, len(st.results))
	for _, r := range st.results {
		resultByOrdinal[r.Ordinal] = r
	}

	for i := range st.plans {
		plan := &st.plans[i]
		if plan.PlannedAction != model.ImportActionCreate {
			continue
		}
		result := resultByOrdinal[plan.Ordinal]
		if result == nil {
			continue
		}
		parentID := st.createParentByOrdinal[plan.Ordinal]
		code := ""
		if _, over := overCapacity[parentID]; over {
			code = importer.IssueTargetSiblingCapacityExceeded
		}
		if _, over := overDepth[parentID]; over {
			code = importer.IssueTargetDepthExceeded
		}
		if code == "" {
			continue
		}
		plan.PlannedAction = model.ImportActionBlocked
		plan.PlannedPageId = ""
		result.PlannedAction = model.ImportActionBlocked
		result.Outcome = model.ImportOutcomeBlocked
		st.actions.Create--
		st.actions.Blocked++
		st.issues = append(st.issues, &model.ImportIssueRecord{
			JobId:       st.job.Id,
			Stage:       model.ImportStagePreflight,
			Ordinal:     plan.Ordinal*model.ImportIssuesPerPage + model.ImportMaxIssueCodesPerPage,
			Severity:    model.ImportSeverityError,
			Code:        code,
			EntityType:  model.ImportEntityTypePage,
			ExternalId:  result.ExternalId,
			Title:       result.Title,
			Message:     preflightIssueMessage(code),
			Remediation: preflightIssueRemediation(code),
		})
	}
	return nil
}

// resolveImportAuthor resolves one staged page's author, caching each distinct source identity.
//
// Authorship never grants access: the resolved user does not need membership of the target team or Space,
// because attributing an imported page to someone is a statement about who wrote it in Confluence, not a
// permission grant.
func (s *Service) resolveImportAuthor(st *preflightState, page *model.ImportStagedPage) resolvedAuthor {
	cacheKey := page.SourceAuthorAccountId + "\x1f" + page.SourceUserProposal
	if cached, ok := st.authorCache[cacheKey]; ok {
		st.countAuthor(cached)
		return cached
	}

	resolved := s.resolveImportAuthorUncached(st, page)
	st.authorCache[cacheKey] = resolved
	st.countAuthor(resolved)
	return resolved
}

// countAuthor tallies one resolution into the summary.
func (st *preflightState) countAuthor(author resolvedAuthor) {
	if author.reason == "" {
		st.authors.Mapped++
		return
	}
	st.authors.FallbackToActor++
}

// resolveImportAuthorUncached performs one author resolution.
func (s *Service) resolveImportAuthorUncached(st *preflightState, page *model.ImportStagedPage) resolvedAuthor {
	fallback := func(reason string) resolvedAuthor {
		return resolvedAuthor{userID: st.job.ActorId, reason: reason}
	}
	if page.SourceAuthorAccountId == "" && page.SourceUserProposal == "" {
		return fallback(model.ImportFallbackSourceAuthorMissing)
	}

	// The manifest's username is a proposal, never authority: it names who the producer *thinks* the
	// author is, and only a live local user resolves it.
	proposal := st.userProposals[page.SourceAuthorAccountId]
	reason := ""
	if page.SourceAuthorAccountId != "" && proposal == "" {
		if _, mapped := st.userProposals[page.SourceAuthorAccountId]; !mapped {
			reason = model.ImportFallbackManifestUserMissing
		} else {
			reason = model.ImportFallbackUsernameMissing
		}
	}
	if proposal == "" {
		// Fall back to the page's own user field as a username proposal before giving up. Any reason
		// collected above is discarded: the account mapping being absent stops mattering once a usable
		// proposal is found, and a resolution failure below reports its own, more specific reason.
		proposal = page.SourceUserProposal
		if proposal == "" {
			return fallback(reason)
		}
	}

	user, err := s.client.User.GetByUsername(proposal)
	if err != nil || user == nil {
		return fallback(model.ImportFallbackUserNotFound)
	}
	if user.DeleteAt != 0 {
		return fallback(model.ImportFallbackUserInactive)
	}
	return resolvedAuthor{userID: user.Id}
}

// importPreflightRevision is the canonical digest of everything a user reviews.
//
// It covers every per-page baseline, the stale set, the summary, and the mapping revision, so a
// confirmation can only apply to the exact plan that was shown. Anything that would change what the user
// saw — a different classification, a moved parent, a new stale entry, a different source revision —
// changes this value and invalidates the confirmation.
func importPreflightRevision(st *preflightState, summary model.ImportPreflightSummary) (string, error) {
	type pageDigest struct {
		Ordinal        int    `json:"ordinal"`
		ExternalID     string `json:"external_id"`
		Action         string `json:"action"`
		PlannedPageID  string `json:"planned_page_id"`
		ResolvedUserID string `json:"resolved_user_id"`
		FallbackReason string `json:"fallback_reason"`
		IncomingHash   string `json:"incoming_hash"`
		CurrentHash    string `json:"current_hash"`
		MappingHash    string `json:"mapping_hash"`
		CurrentParent  string `json:"current_parent"`
		MappingParent  string `json:"mapping_parent"`
	}
	type digestInput struct {
		Version         int                          `json:"version"`
		JobID           string                       `json:"job_id"`
		MappingRevision int64                        `json:"mapping_revision"`
		Summary         model.ImportPreflightSummary `json:"summary"`
		Pages           []pageDigest                 `json:"pages"`
		Stale           []string                     `json:"stale"`
		RequiredAcks    []string                     `json:"required_acknowledgements"`
	}

	pages := make([]pageDigest, 0, len(st.plans))
	for _, p := range st.plans {
		pages = append(pages, pageDigest{
			Ordinal:        p.Ordinal,
			Action:         string(p.PlannedAction),
			PlannedPageID:  p.PlannedPageId,
			ResolvedUserID: p.ResolvedUserId,
			FallbackReason: p.AuthorFallbackReason,
			IncomingHash:   p.IncomingSourceContentHash,
			CurrentHash:    p.PreflightCurrentContentHash,
			MappingHash:    p.PreflightMappingContentHash,
			CurrentParent:  p.PreflightCurrentParentId,
			MappingParent:  p.PreflightMappingParentId,
		})
	}
	// External ids live on the results rather than the plans, so they are attached by ordinal.
	externalByOrdinal := make(map[int]string, len(st.results))
	for _, r := range st.results {
		externalByOrdinal[r.Ordinal] = r.ExternalId
	}
	for i := range pages {
		pages[i].ExternalID = externalByOrdinal[pages[i].Ordinal]
	}

	stale := make([]string, 0)
	for _, r := range st.results {
		if r.PlannedAction == model.ImportActionStale {
			stale = append(stale, r.ExternalId)
		}
	}
	sort.Strings(stale)

	raw, err := json.Marshal(digestInput{
		Version:         importer.HashFormatVersion,
		JobID:           st.job.Id,
		MappingRevision: st.mappingRevision,
		Summary:         summary,
		Pages:           pages,
		Stale:           stale,
		RequiredAcks:    importRequiredAcknowledgements(st.job.TargetKind, st.job.BundleSummary.Counts, st.actions),
	})
	if err != nil {
		return "", errors.Wrap(err, "marshal preflight revision input")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
