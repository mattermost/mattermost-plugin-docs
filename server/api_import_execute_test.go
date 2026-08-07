// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/importfixture"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// --- helpers ---

// uploadAndPreflight uploads a bundle, applies an optional source selection, and drives the worker until the
// job is awaiting confirmation. sourceBody is the JSON for the source-selection step, or "" for a new-Space
// target which has no source to choose.
func (h *apiTestHarness) uploadAndPreflight(
	t *testing.T,
	actorID, target string,
	opts importfixture.Options,
	sourceBody string,
) (string, *model.ImportJobView) {
	t.Helper()
	rec := h.uploadFixture(t, actorID, target, opts)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	if sourceBody != "" {
		selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID, json.RawMessage(sourceBody))
		require.Equal(t, http.StatusAccepted, selected.Code, selected.Body.String())
	}

	h.drainImportWorker(t)
	view := decodeJobView(t, h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil))
	require.Equal(t, model.ImportStateAwaitingConfirmation, view.State, "the job should be ready to confirm")
	return jobID, view
}

// confirmAndExecute confirms a reviewed job and drives the worker until it reaches a terminal state.
func (h *apiTestHarness) confirmAndExecute(
	t *testing.T,
	actorID, jobID string,
	view *model.ImportJobView,
	newSpace bool,
	overwrites ...string,
) *model.ImportJob {
	t.Helper()
	confirm := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID,
		confirmBody(t, view, newSpace, overwrites...))
	require.Equal(t, http.StatusAccepted, confirm.Code, confirm.Body.String())

	h.drainImportWorker(t)
	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.True(t, job.State.IsTerminal(), "the job should have reached a terminal state, got %q", job.State)
	return job
}

// importedPages returns a job's target Space pages keyed by the Confluence external id recorded on each one,
// which is the only link between what the bundle declared and what was written.
func (h *apiTestHarness) importedPages(t *testing.T, sourceID string) map[string]*model.Page {
	t.Helper()
	rows, err := h.db.Query(
		`SELECT ExternalId, LocalId FROM DOCS_ImportEntity WHERE ImportSourceId = $1 AND EntityType = 'page'`,
		sourceID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	byExternalID := map[string]string{}
	for rows.Next() {
		var externalID, localID string
		require.NoError(t, rows.Scan(&externalID, &localID))
		byExternalID[externalID] = localID
	}
	require.NoError(t, rows.Err())

	pages := make(map[string]*model.Page, len(byExternalID))
	for externalID, localID := range byExternalID {
		page, err := h.store.GetPage(localID, true)
		require.NoError(t, err, "mapping for %s points at a missing page", externalID)
		pages[externalID] = page
	}
	return pages
}

// executionOutcomes returns a job's committed execution outcomes keyed by external id.
func (h *apiTestHarness) executionOutcomes(t *testing.T, jobID string) map[string]string {
	t.Helper()
	rows, err := h.db.Query(
		`SELECT ExternalId, Outcome FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution'`, jobID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	outcomes := map[string]string{}
	for rows.Next() {
		var externalID, outcome string
		require.NoError(t, rows.Scan(&externalID, &outcome))
		outcomes[externalID] = outcome
	}
	require.NoError(t, rows.Err())
	return outcomes
}

// executionIssueCodes returns the set of execution-stage issue codes a job recorded.
func (h *apiTestHarness) executionIssueCodes(t *testing.T, jobID string) map[string]int {
	t.Helper()
	rows, err := h.db.Query(
		`SELECT Code, COUNT(*) FROM DOCS_ImportIssue WHERE JobId = $1 AND Stage = 'execution' GROUP BY Code`, jobID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	codes := map[string]int{}
	for rows.Next() {
		var code string
		var count int
		require.NoError(t, rows.Scan(&code, &count))
		codes[code] = count
	}
	require.NoError(t, rows.Err())
	return codes
}

// newSourceSelection is the source-selection body for adopting a fresh source identity.
func newSourceSelection(displayName string) string {
	return fmt.Sprintf(`{"mode":"new","display_name":%q}`, displayName)
}

// existingSourceSelection is the source-selection body for reusing a source a previous import created.
func existingSourceSelection(sourceID string) string {
	return fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)
}

// --- tests ---

// TestImportExecution_CreatesHierarchyAndMappings is the happy path end to end: a new Space is provisioned, a
// chain of pages is created parent-before-child with the planned ids the preflight published, and each page
// gets the durable mapping a later reimport will classify against.
func TestImportExecution_CreatesHierarchyAndMappings(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(teamID),
		importfixture.Options{Pages: 3, Chain: true}, "")

	// The planned ids are published for review, and execution must use exactly them: the report and any link
	// analysis already named these pages before they existed.
	plannedByExternalID := map[string]string{}
	results := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/preflight-results?per_page=100", actorID, nil)
	require.Equal(t, http.StatusOK, results.Code)
	var page paginatedResponse[model.ImportPreflightResultView]
	require.NoError(t, json.Unmarshal(results.Body.Bytes(), &page))
	for _, item := range page.Items {
		plannedByExternalID[item.ExternalId] = item.LocalId
	}
	require.Len(t, plannedByExternalID, 3)

	job := h.confirmAndExecute(t, actorID, jobID, view, true)
	require.Equal(t, 3, job.FinalSummary.Actions.Create)
	require.Equal(t, 3, job.FinalSummary.Outcomes[string(model.ImportOutcomeCreated)])

	space, err := h.store.GetSpace(job.TargetSpaceId, false)
	require.NoError(t, err)

	pages := h.importedPages(t, job.SelectedImportSourceId)
	require.Len(t, pages, 3, "every created page must have a mapping")
	for externalID, planned := range plannedByExternalID {
		require.Equal(t, planned, pages[externalID].Id,
			"execution must use the planned page id for %s", externalID)
	}

	// The chain is preserved: 100 is a Space root, 101 hangs off it, 102 off 101.
	require.Empty(t, pages["100"].ParentId)
	require.Equal(t, pages["100"].Id, pages["101"].ParentId)
	require.Equal(t, pages["101"].Id, pages["102"].ParentId)
	for _, p := range pages {
		require.Equal(t, space.Id, p.SpaceId)
		require.Equal(t, space.ChannelId, p.ChannelId)
		require.Equal(t, model.PageTypePage, p.Type)
		require.Zero(t, p.DeleteAt)
	}

	// The source's create timestamp is preserved, which is the whole point of importing rather than retyping.
	require.Equal(t, int64(1704106800000), pages["100"].CreateAt)

	// The importer's own bookkeeping lives under one namespace, so a reimport can replace it wholesale.
	docsImport := importer.DocsImportNamespace(pages["100"].Props)
	require.NotNil(t, docsImport)
	require.Equal(t, "100", docsImport[importer.DocsImportKeyExternalPageID])
	require.Equal(t, job.SelectedImportSourceId, docsImport[importer.DocsImportKeyImportSourceID])
	require.Equal(t, job.Id, docsImport[importer.DocsImportKeyLastJobID])

	// The source is created with the revision this job's own mapping writes bumped exactly once.
	source, err := h.store.GetImportSource(job.SelectedImportSourceId)
	require.NoError(t, err)
	require.Equal(t, space.Id, source.SpaceId)
	require.Equal(t, int64(1), source.MappingRevision, "one bump per job, not one per page")
	require.Equal(t, job.Id, source.LastSuccessfulJobId)
	require.Positive(t, source.LastImportAt)

	// The tree changed, so the invalidation was owed — and published, which clears the flag.
	require.False(t, job.InvalidationPending, "the invalidation is published immediately after terminalization")
}

// TestImportExecution_ReplayingCommittedPagesCreatesNothingTwice covers the immutable checkpoint. A crash
// leaves committed pages behind, and the pass that resumes must recognize them rather than importing the
// bundle a second time — the difference between a restart and a duplicated Space.
func TestImportExecution_ReplayingCommittedPagesCreatesNothingTwice(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 3, Chain: true}, "")
	job := h.confirmAndExecute(t, actorID, jobID, view, true)
	require.Equal(t, 3, job.FinalSummary.Actions.Create)

	pagesBefore := h.importedPages(t, job.SelectedImportSourceId)
	sourceBefore, err := h.store.GetImportSource(job.SelectedImportSourceId)
	require.NoError(t, err)

	// Put the job back into importing, as a crash between the last page and terminalization would leave it.
	// Every page already has a checkpoint, so the resumed pass must apply nothing.
	_, err = h.db.Exec(
		`UPDATE DOCS_ImportJob SET State='importing', TerminalIntent='', FinishedAt=0, InvalidationPending=FALSE
		   WHERE Id=$1`, jobID)
	require.NoError(t, err)

	h.drainImportWorker(t)

	resumed, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.True(t, resumed.State.IsTerminal())
	require.Equal(t, 3, resumed.FinalSummary.Actions.Create,
		"a resumed pass reports the same outcomes it replayed, not new ones")

	pagesAfter := h.importedPages(t, job.SelectedImportSourceId)
	require.Equal(t, len(pagesBefore), len(pagesAfter))
	for externalID, before := range pagesBefore {
		require.Equal(t, before.Id, pagesAfter[externalID].Id, "page %s was recreated", externalID)
		require.Equal(t, before.EditAt, pagesAfter[externalID].EditAt, "page %s was rewritten", externalID)
	}

	// A replayed page changes no mapping input, so the source revision must not move again: bumping it would
	// invalidate every other job's reviewed plan for work that did not happen.
	sourceAfter, err := h.store.GetImportSource(job.SelectedImportSourceId)
	require.NoError(t, err)
	require.Equal(t, sourceBefore.MappingRevision, sourceAfter.MappingRevision)

	var pageCount int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_Page WHERE SpaceId = $1 AND DeleteAt = 0 AND OriginalId = ''`,
		job.TargetSpaceId).Scan(&pageCount))
	require.Equal(t, 3, pageCount, "the resumed pass must not create a second copy of the bundle")
}

// TestImportExecution_ReimportNoopsThenPreservesLocalEdits is the reimport decision table at execution. The
// same bundle imported twice must change nothing the second time, and a page a user has since edited must keep
// their version — the two failure modes here are pointless rewrites and silently discarded work.
func TestImportExecution_ReimportNoopsThenPreservesLocalEdits(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	opts := importfixture.Options{Pages: 3, Chain: true}

	firstID, firstView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), opts,
		newSourceSelection("Acme Confluence / DOCS"))
	first := h.confirmAndExecute(t, actorID, firstID, firstView, false)
	require.Equal(t, 3, first.FinalSummary.Actions.Create)
	sourceID := first.SelectedImportSourceId
	pages := h.importedPages(t, sourceID)

	// Second import of the identical bundle: nothing in the source changed and nothing locally changed.
	secondID, secondView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), opts,
		existingSourceSelection(sourceID))
	require.Contains(t, secondView.RequiredAcknowledgements, model.ImportAckReimportExisting,
		"touching pages that already exist must be acknowledged")
	second := h.confirmAndExecute(t, actorID, secondID, secondView, false)
	require.Equal(t, 3, second.FinalSummary.Actions.Noop)
	require.Zero(t, second.FinalSummary.Actions.Update)
	require.Zero(t, second.FinalSummary.Actions.Create)

	// A no-op must not touch the page at all: bumping its timestamps would show every reader a spurious edit.
	unchanged := h.importedPages(t, sourceID)
	for externalID, before := range pages {
		require.Equal(t, before.EditAt, unchanged[externalID].EditAt, "page %s was rewritten by a no-op", externalID)
		require.Equal(t, before.UpdateAt, unchanged[externalID].UpdateAt, "page %s was touched by a no-op", externalID)
	}
	// Presence is still recorded, so the page cannot read as stale next time.
	var lastSeen string
	require.NoError(t, h.db.QueryRow(
		`SELECT LastSeenJobId FROM DOCS_ImportEntity WHERE ImportSourceId=$1 AND ExternalId='100'`, sourceID).
		Scan(&lastSeen))
	require.Equal(t, secondID, lastSeen)

	// Now a user edits one page locally, and the source has not changed. Their version wins.
	edited := pages["101"]
	_, appErr := h.plugin.service.UpdatePage(edited.Id, space.Id,
		&model.PagePatch{Title: mmmodel.NewPointer("Edited by a human")}, &edited.EditAt, false, actorID)
	require.Nil(t, appErr)

	thirdID, thirdView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), opts,
		existingSourceSelection(sourceID))
	third := h.confirmAndExecute(t, actorID, thirdID, thirdView, false)
	require.Equal(t, 1, third.FinalSummary.Actions.PreserveLocal)
	require.Equal(t, 2, third.FinalSummary.Actions.Noop)
	require.Equal(t, string(model.ImportOutcomeLocalPreserved), h.executionOutcomes(t, thirdID)["101"])
	require.Contains(t, h.executionIssueCodes(t, thirdID), importer.IssueLocalChangesPreserved)

	preserved, err := h.store.GetPage(edited.Id, false)
	require.NoError(t, err)
	require.Equal(t, "Edited by a human", preserved.Title, "the user's edit must survive a reimport")
}

// TestImportExecution_ApprovedOverwriteAppliesAndStaleApprovalIsRefused covers the one path that deliberately
// discards a user's work. An approval is honoured only against the exact state that was reviewed, so the same
// approval must be refused once the page moves on — the browser's approval carries intent, and only the
// server-owned baselines carry safety.
func TestImportExecution_ApprovedOverwriteAppliesAndStaleApprovalIsRefused(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	first := importfixture.Options{Pages: 2, Chain: true}
	// Revision changes every page's title and body, which is what the source content hash covers, so the
	// reimport sees a genuine Confluence edit on each page rather than an unchanged bundle.
	changed := importfixture.Options{Pages: 2, Chain: true, Revision: 2}

	firstID, firstView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), first,
		newSourceSelection("Acme Confluence / DOCS"))
	imported := h.confirmAndExecute(t, actorID, firstID, firstView, false)
	sourceID := imported.SelectedImportSourceId
	pages := h.importedPages(t, sourceID)

	// The user edits both pages, so a changed source now conflicts with local work on each.
	for _, externalID := range []string{"100", "101"} {
		page := pages[externalID]
		_, appErr := h.plugin.service.UpdatePage(page.Id, space.Id,
			&model.PagePatch{Title: mmmodel.NewPointer("Local edit to " + externalID)}, &page.EditAt, false, actorID)
		require.Nil(t, appErr)
	}

	secondID, secondView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), changed,
		existingSourceSelection(sourceID))
	require.Equal(t, 2, secondView.Preflight.Counts.Actions[string(model.ImportActionConflict)])

	// Approve one page for overwrite, then edit it again *after* confirming. The approval named a state that no
	// longer exists, so it must be refused rather than applied to whatever is there now.
	confirm := h.do(t, http.MethodPost, "/api/v1/imports/"+secondID+"/confirm", actorID,
		confirmBody(t, secondView, false, "100", "101"))
	require.Equal(t, http.StatusAccepted, confirm.Code, confirm.Body.String())

	movedOn, err := h.store.GetPage(pages["101"].Id, false)
	require.NoError(t, err)
	_, appErr := h.plugin.service.UpdatePage(movedOn.Id, space.Id,
		&model.PagePatch{Title: mmmodel.NewPointer("Edited again after approving")}, &movedOn.EditAt, false, actorID)
	require.Nil(t, appErr)

	h.drainImportWorker(t)
	job, err := h.store.GetImportJob(secondID)
	require.NoError(t, err)
	require.True(t, job.State.IsTerminal())

	outcomes := h.executionOutcomes(t, secondID)
	require.Equal(t, string(model.ImportOutcomeUpdated), outcomes["100"],
		"an approval whose baselines still hold must be applied")
	require.Equal(t, string(model.ImportOutcomeConflictSkipped), outcomes["101"],
		"an approval whose baselines moved must be refused")
	require.Contains(t, h.executionIssueCodes(t, secondID), importer.IssueConflictChangedAfterConfirmation)

	overwritten, err := h.store.GetPage(pages["100"].Id, false)
	require.NoError(t, err)
	require.Equal(t, "Imported page 1 (rev 2)", overwritten.Title, "the approved page takes the Confluence version")
	require.Equal(t, actorID, overwritten.LastModifiedBy, "the importing actor is the editor of record")

	kept, err := h.store.GetPage(pages["101"].Id, false)
	require.NoError(t, err)
	require.Equal(t, "Edited again after approving", kept.Title, "the refused page keeps the newer local edit")
}

// TestImportExecution_UnavailableParentBlocksItsDescendants covers the dependency rule. A page whose parent was
// not created has nowhere to go, and promoting it to a Space root would silently flatten a hierarchy nobody
// asked to change.
func TestImportExecution_UnavailableParentBlocksItsDescendants(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	opts := importfixture.Options{Pages: 3, Chain: true}

	firstID, firstView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), opts,
		newSourceSelection("Acme Confluence / DOCS"))
	imported := h.confirmAndExecute(t, actorID, firstID, firstView, false)
	sourceID := imported.SelectedImportSourceId
	pages := h.importedPages(t, sourceID)

	// Delete the middle page. Its mapping survives, so the reimport finds it mapped-but-gone and blocks it
	// rather than resurrecting a page someone deliberately deleted — and its child then has no live parent.
	_, err := h.store.DeletePage(pages["101"].Id, space.Id, actorID)
	require.NoError(t, err)

	secondID, secondView := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id), opts,
		existingSourceSelection(sourceID))
	second := h.confirmAndExecute(t, actorID, secondID, secondView, false)

	outcomes := h.executionOutcomes(t, secondID)
	require.Equal(t, string(model.ImportOutcomeUnchanged), outcomes["100"])
	require.Equal(t, string(model.ImportOutcomeBlocked), outcomes["101"])
	require.Equal(t, string(model.ImportOutcomeUnchanged), outcomes["102"],
		"102 keeps its own mapping and its own live page, so its parent's absence does not block it")
	require.Equal(t, 1, second.FinalSummary.Actions.Blocked)
	require.Contains(t, h.executionIssueCodes(t, secondID), importer.IssueMappedTargetMissing)

	// The deleted page stays deleted: a reimport is not a restore.
	stillDeleted, err := h.store.GetPage(pages["101"].Id, true)
	require.NoError(t, err)
	require.NotZero(t, stillDeleted.DeleteAt)
}

// TestImportExecution_NewChildOfBlockedParentIsNotRooted is the create-side half of the dependency rule: a page
// the bundle introduces beneath a parent that was never created must be skipped, not silently attached to the
// top of the Space.
func TestImportExecution_NewChildOfBlockedParentIsNotRooted(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	jobID, view := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id),
		importfixture.Options{Pages: 3, Chain: true}, newSourceSelection("Acme Confluence / DOCS"))

	// Strand the middle page's create by clearing its planned id, as the structural projection does when it
	// refuses one. Its child must then find no parent mapping at execution.
	_, err := h.db.Exec(
		`UPDATE DOCS_ImportStagedPage SET PlannedAction='blocked', PlannedPageId='' WHERE JobId=$1 AND ExternalId='101'`,
		jobID)
	require.NoError(t, err)

	job := h.confirmAndExecute(t, actorID, jobID, view, false)

	outcomes := h.executionOutcomes(t, jobID)
	require.Equal(t, string(model.ImportOutcomeCreated), outcomes["100"])
	require.Equal(t, string(model.ImportOutcomeBlocked), outcomes["101"])
	require.Equal(t, string(model.ImportOutcomeBlocked), outcomes["102"],
		"a descendant of a page that was never created must be skipped, not rooted at the Space")
	require.Equal(t, 1, job.FinalSummary.Actions.Create)
	require.Equal(t, 2, job.FinalSummary.Actions.Blocked)
	require.Contains(t, h.executionIssueCodes(t, jobID), importer.IssueParentNotAvailableAfterImport)

	// Only the root was created, and nothing was attached to the Space root behind the user's back.
	var roots int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_Page WHERE SpaceId=$1 AND ParentId='' AND DeleteAt=0 AND OriginalId=''`,
		space.Id).Scan(&roots))
	require.Equal(t, 1, roots)
}

// TestImportExecution_CancelKeepsCommittedPages covers cancelling a partially executed import. Committed pages
// are real content and are never rolled back, but every page the job never reached must still end up with a
// durable outcome — otherwise the report would simply be silent about most of the bundle.
func TestImportExecution_CancelKeepsCommittedPages(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	jobID, view := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id),
		importfixture.Options{Pages: 3, Chain: true}, newSourceSelection("Acme Confluence / DOCS"))
	job := h.confirmAndExecute(t, actorID, jobID, view, false)
	require.Equal(t, 3, job.FinalSummary.Actions.Create)
	keptPage := h.importedPages(t, job.SelectedImportSourceId)["100"]

	// Rewind to the state a worker interrupted after its first page would be in: the job is importing again and
	// only ordinal 0 has a committed checkpoint.
	_, err := h.db.Exec(
		`DELETE FROM DOCS_ImportResult WHERE JobId=$1 AND Stage='execution' AND Ordinal > 0`, jobID)
	require.NoError(t, err)
	_, err = h.db.Exec(
		`UPDATE DOCS_ImportJob SET State='importing', TerminalIntent='', ErrorCode='', FinishedAt=0,
		   InvalidationPending=FALSE, FinalSummary='{}'::jsonb WHERE Id=$1`, jobID)
	require.NoError(t, err)

	// Cancelling an importing job cannot finish it on the spot: the terminalizer has to reconcile what was
	// already written first, so the request is accepted and the job goes to terminalizing.
	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code, cancel.Body.String())
	requested, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateTerminalizing, requested.State)

	h.drainImportWorker(t)
	canceled, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateCanceled, canceled.State)
	require.Equal(t, app.ImportErrorCanceledByUser, canceled.ErrorCode)
	require.Equal(t, 1, canceled.FinalSummary.Actions.Create, "the committed page keeps its real outcome")
	require.Equal(t, 2, canceled.FinalSummary.Actions.NotAttempted)
	require.Equal(t, 2, canceled.FinalSummary.Outcomes[string(model.ImportOutcomeNotAttemptedCancel)])
	require.Contains(t, h.executionIssueCodes(t, jobID), importer.IssueNotAttemptedCanceled)

	// The already-imported page is untouched, and its mapping still points at it.
	survivor, err := h.store.GetPage(keptPage.Id, false)
	require.NoError(t, err)
	require.Equal(t, keptPage.Title, survivor.Title)
	require.Zero(t, canceled.StagedBytes, "a terminal job releases its staged input")
}

// TestImportExecution_ProvisioningFailureFailsTheJob covers a new-Space import whose backing channel cannot be
// created. Nothing was created, so there is nothing to compensate — but the job must still reach a terminal
// state with a report rather than sitting in importing forever.
func TestImportExecution_ProvisioningFailureFailsTheJob(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelCreateFails: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 2}, "")
	job := h.confirmAndExecute(t, actorID, jobID, view, true)

	require.Equal(t, model.ImportStateFailed, job.State)
	require.Equal(t, app.ImportErrorProvisioningFailed, job.ErrorCode)
	require.Equal(t, 2, job.FinalSummary.Actions.NotAttempted)
	require.Equal(t, 2, job.FinalSummary.Outcomes[string(model.ImportOutcomeNotAttemptedFailure)])
	require.Contains(t, h.executionIssueCodes(t, jobID), importer.IssueNotAttemptedFailed)

	// No Space, and no channel attempt left claiming a live channel.
	_, err := h.store.GetSpace(job.TargetSpaceId, true)
	require.Error(t, err)
	attempts, err := h.store.GetImportChannelAttempts(jobID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, model.ImportChannelFailed, attempts[0].State)
	require.Empty(t, attempts[0].ChannelId)
}

// TestImportExecution_CompensatesAnUnattachedChannel covers the window between creating a backing channel and
// writing the Space row. A job that dies in it owns a channel nobody references, and terminalization must
// archive it and say so — a channel that cannot be found by name is otherwise invisible operator work.
func TestImportExecution_CompensatesAnUnattachedChannel(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 2}, "")
	job := h.confirmAndExecute(t, actorID, jobID, view, true)
	channelID := mustProvisionedChannelID(t, h, jobID)

	// Rewind to the moment after the channel was created and before the Space existed, then fail the job.
	_, err := h.db.Exec(`DELETE FROM DOCS_ImportResult WHERE JobId=$1 AND Stage='execution'`, jobID)
	require.NoError(t, err)
	_, err = h.db.Exec(`DELETE FROM DOCS_Page WHERE SpaceId=$1`, job.TargetSpaceId)
	require.NoError(t, err)
	_, err = h.db.Exec(`DELETE FROM DOCS_ImportEntity WHERE ImportSourceId=$1`, job.SelectedImportSourceId)
	require.NoError(t, err)
	_, err = h.db.Exec(`DELETE FROM DOCS_Space WHERE Id=$1`, job.TargetSpaceId)
	require.NoError(t, err)
	_, err = h.db.Exec(
		`UPDATE DOCS_ImportChannelAttempt SET State='provisioned' WHERE JobId=$1`, jobID)
	require.NoError(t, err)
	_, err = h.db.Exec(
		`UPDATE DOCS_ImportJob SET State='terminalizing', TerminalIntent='failed', ErrorCode='execution_failed',
		   FinishedAt=0, InvalidationPending=FALSE, FinalSummary='{}'::jsonb WHERE Id=$1`, jobID)
	require.NoError(t, err)

	h.drainImportWorker(t)

	failed, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateFailed, failed.State)
	require.Contains(t, h.executionIssueCodes(t, jobID), importer.IssueChannelCompensated,
		"the orphaned channel's fate belongs in the report")

	attempts, err := h.store.GetImportChannelAttempts(jobID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, model.ImportChannelCompensated, attempts[0].State)
	require.Equal(t, channelID, attempts[0].ChannelId)
}

// mustProvisionedChannelID reads the channel a job provisioned for its new Space.
func mustProvisionedChannelID(t *testing.T, h *apiTestHarness, jobID string) string {
	t.Helper()
	var channelID string
	require.NoError(t, h.db.QueryRow(
		`SELECT ProvisionedChannelId FROM DOCS_ImportJob WHERE Id=$1`, jobID).Scan(&channelID))
	require.True(t, mmmodel.IsValidId(channelID))
	return channelID
}

// TestImportExecution_MissingTargetSpaceFailsWithoutStarvingTheWorker covers a confirmed job whose destination
// disappears. Work selection returns the first non-empty state in priority order, so a job left in importing
// without advancing is re-selected on every pass and blocks every job behind it: the failure has to be recorded
// rather than skipped, and the job behind it has to get its turn.
func TestImportExecution_MissingTargetSpaceFailsWithoutStarvingTheWorker(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	jobID, view := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id),
		importfixture.Options{Pages: 2}, newSourceSelection("Acme Confluence / DOCS"))
	confirm := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, confirmBody(t, view, false))
	require.Equal(t, http.StatusAccepted, confirm.Code, confirm.Body.String())

	// The target Space is deleted between confirmation and execution. There is nowhere to write, so the job
	// cannot proceed — and must not be left selectable.
	// Deleted at the store layer: the service's delete also archives the backing channel, which needs channel
	// stubs this test has no interest in.
	require.NoError(t, h.store.DeleteSpace(space.Id))

	// A second upload queues behind it and must still get its turn.
	laterID, _ := func() (string, *model.ImportJobView) {
		other := seedSpace(t, h.store, mmmodel.NewId())
		return h.uploadAndPreflight(t, actorID, existingTargetRequest(other.Id),
			importfixture.Options{Pages: 1}, newSourceSelection("Another source"))
	}()

	h.drainImportWorker(t)

	failed, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateFailed, failed.State,
		"a job that cannot proceed must be failed, not re-selected forever")
	// The pre-write authorization recheck is what notices: membership of a deleted Space cannot be confirmed.
	require.Equal(t, app.ImportErrorAuthorizationRevoked, failed.ErrorCode)
	require.Equal(t, 2, failed.FinalSummary.Actions.NotAttempted)
	require.Zero(t, failed.StagedBytes)

	later, err := h.store.GetImportJob(laterID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, later.State,
		"the blocked job must not starve later work")
}
