// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/importfixture"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// drainImportWorker runs the worker until it reports no more work, exactly as the plugin's loop does.
// Tests drive it explicitly rather than starting the goroutine, so a preflight either has happened or has
// not when an assertion runs.
func (h *apiTestHarness) drainImportWorker(t *testing.T) {
	t.Helper()
	for range 20 {
		worked, err := h.plugin.service.RunImportWork()
		require.NoError(t, err)
		if !worked {
			return
		}
	}
	t.Fatalf("import worker did not settle after 20 passes")
}

// confirmBody builds a confirmation request body with every acknowledgement the job asks for.
func confirmBody(t *testing.T, view *model.ImportJobView, newSpace bool, overwrites ...string) json.RawMessage {
	t.Helper()
	acks := map[string]bool{}
	for _, key := range view.RequiredAcknowledgements {
		acks[key] = true
	}
	body := map[string]any{
		"preflight_revision": view.Preflight.Revision,
		"acknowledgements":   acks,
	}
	if newSpace {
		body["new_space"] = map[string]string{"title": "Imported Docs", "description": "From Confluence"}
	}
	if len(overwrites) > 0 {
		body["overwrite_conflicts"] = overwrites
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

// TestImportPreflight_NewSpaceEndToEnd walks a new-Space target from upload to queued_import: the worker
// picks the queued job up, classifies every page as a create, and confirmation queues the import.
func TestImportPreflight_NewSpaceEndToEnd(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 4, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// A new Space has one possible source identity, so the job goes straight to the preflight queue.
	require.Equal(t, model.ImportStateQueuedPreflight, decodeJobView(t, rec).State)

	h.drainImportWorker(t)

	got := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	require.Equal(t, http.StatusOK, got.Code)
	view := decodeJobView(t, got)
	require.Equal(t, model.ImportStateAwaitingConfirmation, view.State)
	require.Len(t, view.Preflight.Revision, 64)
	// Nothing exists locally yet, so every page is a create and nothing is a reimport.
	require.Equal(t, 4, view.Preflight.Counts.Actions[string(model.ImportActionCreate)])
	require.Zero(t, view.Preflight.Counts.Actions[string(model.ImportActionUpdate)])
	require.Zero(t, view.Preflight.Counts.Actions[string(model.ImportActionConflict)])
	require.NotContains(t, view.RequiredAcknowledgements, model.ImportAckReimportExisting)
	require.Contains(t, view.RequiredAcknowledgements, model.ImportAckNewSpaceMetadata)

	// The review rows carry one entry per page, each with the planned id execution must use.
	results := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/preflight-results?per_page=100", actorID, nil)
	require.Equal(t, http.StatusOK, results.Code)
	var page paginatedResponse[model.ImportPreflightResultView]
	require.NoError(t, json.Unmarshal(results.Body.Bytes(), &page))
	require.Len(t, page.Items, 4)
	for _, item := range page.Items {
		require.Equal(t, model.ImportActionCreate, item.PlannedAction)
		require.NotEmpty(t, item.ExternalId)
		require.True(t, mmmodel.IsValidId(item.LocalId), "a create must carry the planned page id")
		require.False(t, item.OverwriteEligible)
	}

	confirm := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, confirmBody(t, view, true))
	require.Equal(t, http.StatusAccepted, confirm.Code, confirm.Body.String())
	require.Equal(t, model.ImportStateQueuedImport, decodeJobView(t, confirm).State)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateQueuedImport, job.State)
	require.Equal(t, "Imported Docs", job.ConfirmedSpaceTitle)
	require.Equal(t, view.Preflight.Revision, job.Confirmation.PreflightRevision)
	require.True(t, job.Confirmation.Acknowledgements.ConfirmNewSpaceMetadata)
	require.Greater(t, job.ConfirmedAt, int64(0))

	// Execution is not implemented yet, so the worker leaves the job queued rather than spinning on it.
	h.drainImportWorker(t)
	job, err = h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateQueuedImport, job.State)
}

// TestImportSourceSelection_ExistingSpace covers the source step an existing-Space target must take before
// preflight can run. Selection is explicit: nothing is chosen automatically, because two Confluence
// instances can look identical while being different sources.
func TestImportSourceSelection_ExistingSpace(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	require.Equal(t, model.ImportStateAwaitingSource, decodeJobView(t, rec).State)

	// Until a source is chosen the worker has nothing to do: the job is waiting on a human, not on work.
	h.drainImportWorker(t)
	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingSource, job.State)

	// A malformed selection is refused before anything is persisted.
	bad := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID, json.RawMessage(`{"mode":"existing"}`))
	require.Equal(t, http.StatusBadRequest, bad.Code)
	mixed := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"new","display_name":"X","import_source_id":%q}`, mmmodel.NewId())))
	require.Equal(t, http.StatusBadRequest, mixed.Code)
	// An existing source that is not in this Space reads as absent rather than forbidden.
	missing := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, mmmodel.NewId())))
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())

	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(`{"mode":"new","display_name":"Acme Confluence / DOCS"}`))
	require.Equal(t, http.StatusAccepted, selected.Code, selected.Body.String())
	view := decodeJobView(t, selected)
	require.Equal(t, model.ImportStateQueuedPreflight, view.State)
	require.NotNil(t, view.SelectedSource)
	require.Equal(t, model.ImportSourceModeNew, view.SelectedSource.Mode)

	// A new source's id is reserved on the job but the durable row is not created until execution: an
	// unconfirmed job must not leave an identity behind for later jobs to match against.
	job, err = h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.True(t, mmmodel.IsValidId(job.SelectedImportSourceId))
	_, err = h.store.GetImportSource(job.SelectedImportSourceId)
	require.Error(t, err)

	// Selecting twice is a conflict, and the job now preflights.
	again := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(`{"mode":"new","display_name":"Acme"}`))
	require.Equal(t, http.StatusConflict, again.Code)

	h.drainImportWorker(t)
	job, err = h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State)
	require.Equal(t, 2, job.PreflightSummary.Actions.Create)
}

// seedImportSourceWithMapping creates a durable ImportSource in a Space plus one page mapping, standing in
// for a previous import. Returns the source and the local page the mapping points at.
func seedImportSourceWithMapping(
	t *testing.T,
	h *apiTestHarness,
	space *model.Space,
	externalID string,
	sourceContentHash, appliedContentHash string,
) (string, *model.Page) {
	t.Helper()
	page := seedPage(t, h.store, space.Id, space.ChannelId, "")
	sourceID := mmmodel.NewId()
	now := mmmodel.GetMillis()
	_, err := h.db.Exec(
		`INSERT INTO DOCS_ImportSource
		   (Id, SpaceId, SourceType, DisplayName, OrganizationId, ExternalSpaceKey, ExternalSpaceName,
		    CreatedBy, CreateAt, UpdateAt, LastImportAt, LastSuccessfulJobId, MappingRevision, Props)
		 VALUES ($1, $2, 'confluence', 'Acme / DOCS', '', 'DOCS', 'Docs', $3, $4, $4, $4, '', 1, '{}'::jsonb)`,
		sourceID, space.Id, mmmodel.NewId(), now)
	require.NoError(t, err)
	_, err = h.db.Exec(
		`INSERT INTO DOCS_ImportEntity
		   (ImportSourceId, EntityType, ExternalId, LocalId, LastSourceContentHash, LastAppliedContentHash,
		    LastAppliedParentId, LastSourceParentExternalId, LastSourceTitle, LastSourceOrdinal,
		    FirstJobId, LastSeenJobId, CreateAt, UpdateAt)
		 VALUES ($1, 'page', $2, $3, $4, $5, '', '', 'Previously imported', 0, $6, $6, $7, $7)`,
		sourceID, externalID, page.Id, sourceContentHash, appliedContentHash, mmmodel.NewId(), now)
	require.NoError(t, err)
	return sourceID, page
}

// TestImportPreflight_ReimportClassification is the decision table end to end. The same bundle is uploaded
// into a Space that already holds an imported page, and the baselines are rigged so the page classifies as
// a conflict: the source content differs from what was last imported, and so does the local content.
func TestImportPreflight_ReimportClassification(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	// Baselines that match neither the incoming bundle nor the current local page make both sides
	// "changed", which is the conflict row of the table.
	staleHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sourceID, mapped := seedImportSourceWithMapping(t, h, space, importfixture.RootExternalID, staleHash, staleHash)

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 3})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, selected.Code, selected.Body.String())

	h.drainImportWorker(t)

	got := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	view := decodeJobView(t, got)
	require.Equal(t, model.ImportStateAwaitingConfirmation, view.State)
	// The mapped root is a conflict; the two other pages are new.
	require.Equal(t, 1, view.Preflight.Counts.Actions[string(model.ImportActionConflict)])
	require.Equal(t, 2, view.Preflight.Counts.Actions[string(model.ImportActionCreate)])
	// A reimport acknowledgement is now demanded, which it was not before preflight ran.
	require.Contains(t, view.RequiredAcknowledgements, model.ImportAckReimportExisting)

	results := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/preflight-results?per_page=100", actorID, nil)
	var page paginatedResponse[model.ImportPreflightResultView]
	require.NoError(t, json.Unmarshal(results.Body.Bytes(), &page))
	var conflict *model.ImportPreflightResultView
	for i := range page.Items {
		if page.Items[i].PlannedAction == model.ImportActionConflict {
			conflict = &page.Items[i]
		}
	}
	require.NotNil(t, conflict, "expected a conflict row")
	require.Equal(t, importfixture.RootExternalID, conflict.ExternalId)
	require.Equal(t, mapped.Id, conflict.LocalId)
	require.True(t, conflict.OverwriteEligible, "a conflict must be approvable for overwrite")

	// The conflict's explanation is persisted as an issue, with remediation.
	issues := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues?stage=preflight&per_page=100", actorID, nil)
	require.Equal(t, http.StatusOK, issues.Code)
	var issuePage paginatedResponse[model.ImportIssue]
	require.NoError(t, json.Unmarshal(issues.Body.Bytes(), &issuePage))
	var sawConflictIssue bool
	for _, issue := range issuePage.Items {
		if issue.Code == importer.IssueSourceAndLocalConflict {
			sawConflictIssue = true
			require.NotEmpty(t, issue.Message)
			require.NotEmpty(t, issue.Remediation)
		}
	}
	require.True(t, sawConflictIssue, "the conflict must be explained in the report")

	// Approving a page that is not a conflict is refused rather than ignored.
	badApproval := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID,
		confirmBody(t, view, false, "does-not-exist"))
	require.Equal(t, http.StatusBadRequest, badApproval.Code)

	confirm := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID,
		confirmBody(t, view, false, importfixture.RootExternalID))
	require.Equal(t, http.StatusAccepted, confirm.Code, confirm.Body.String())

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateQueuedImport, job.State)
	require.Equal(t, []string{importfixture.RootExternalID}, job.Confirmation.OverwriteConflicts)
}

// TestImportPreflight_NoopWhenNothingChanged pins the other end of the table: a bundle whose content
// matches what was last imported, on a page nobody has touched, is a no-op rather than an update.
func TestImportPreflight_NoopWhenNothingChanged(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	// First import the bundle into a fresh source so the baselines are whatever preflight computes, then
	// copy those baselines into the mapping to stand in for "this is exactly what we applied last time".
	sourceID, mapped := seedImportSourceWithMapping(t, h, space, importfixture.RootExternalID, "", "")

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, selected.Code)
	h.drainImportWorker(t)

	// Read back the hashes preflight computed for the mapped page and adopt them as the baseline.
	var incoming, current string
	require.NoError(t, h.db.QueryRow(
		`SELECT IncomingSourceContentHash, PreflightCurrentContentHash FROM DOCS_ImportStagedPage
		   WHERE JobId = $1 AND ExternalId = $2`, jobID, importfixture.RootExternalID).Scan(&incoming, &current))
	require.Len(t, incoming, 64)
	require.Len(t, current, 64)
	_, err := h.db.Exec(
		`UPDATE DOCS_ImportEntity SET LastSourceContentHash = $1, LastAppliedContentHash = $2
		   WHERE ImportSourceId = $3 AND ExternalId = $4`,
		incoming, current, sourceID, importfixture.RootExternalID)
	require.NoError(t, err)

	// A second import of the same bundle against those baselines must find nothing to do.
	second := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	secondID := decodeJobView(t, second).Id
	pick := h.do(t, http.MethodPost, "/api/v1/imports/"+secondID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, pick.Code)
	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(secondID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State)
	require.Equal(t, 1, job.PreflightSummary.Actions.Noop, "unchanged source and untouched local page is a no-op")
	require.Zero(t, job.PreflightSummary.Actions.Update)
	require.Zero(t, job.PreflightSummary.Actions.Conflict)
	require.NotNil(t, mapped)
}

// TestImportConfirm_Rejections covers the preconditions confirmation enforces before the point of no
// return.
func TestImportConfirm_Rejections(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	h.drainImportWorker(t)

	got := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	view := decodeJobView(t, got)
	require.Equal(t, model.ImportStateAwaitingConfirmation, view.State)

	t.Run("a revision from a different plan is refused", func(t *testing.T) {
		body := confirmBody(t, view, true)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		parsed["preflight_revision"] = "1111111111111111111111111111111111111111111111111111111111111111"
		raw, err := json.Marshal(parsed)
		require.NoError(t, err)
		res := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, json.RawMessage(raw))
		require.Equal(t, http.StatusConflict, res.Code)
	})

	t.Run("a missing acknowledgement is refused", func(t *testing.T) {
		raw, err := json.Marshal(map[string]any{
			"preflight_revision": view.Preflight.Revision,
			"acknowledgements":   map[string]bool{model.ImportAckNewSpaceMetadata: true},
			"new_space":          map[string]string{"title": "T", "description": "D"},
		})
		require.NoError(t, err)
		res := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, json.RawMessage(raw))
		require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
	})

	t.Run("an unknown acknowledgement key is refused", func(t *testing.T) {
		raw, err := json.Marshal(map[string]any{
			"preflight_revision": view.Preflight.Revision,
			"acknowledgements":   map[string]bool{"i_accept_everything": true},
		})
		require.NoError(t, err)
		res := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, json.RawMessage(raw))
		require.Equal(t, http.StatusBadRequest, res.Code)
	})

	t.Run("a new-Space target must supply the Space metadata", func(t *testing.T) {
		res := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, confirmBody(t, view, false))
		require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
	})

	t.Run("another user cannot confirm, and cannot learn the job exists", func(t *testing.T) {
		res := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", mmmodel.NewId(), confirmBody(t, view, true))
		require.Equal(t, http.StatusNotFound, res.Code)
	})

	// The job is still confirmable after every refusal: none of them consumed the revision.
	ok := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, confirmBody(t, view, true))
	require.Equal(t, http.StatusAccepted, ok.Code, ok.Body.String())

	// Confirming twice is a conflict rather than a second queueing.
	twice := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, confirmBody(t, view, true))
	require.Equal(t, http.StatusConflict, twice.Code)
}

// TestImportConfirm_StalePreflightRecomputes covers the invalidation path: another job changed the same
// source's mappings after this plan was reviewed, so the confirmation is refused and the job returns to
// preflight instead of applying a plan whose inputs have moved.
func TestImportConfirm_StalePreflightRecomputes(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	sourceID, _ := seedImportSourceWithMapping(t, h, space, importfixture.RootExternalID, "", "")

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, selected.Code)
	h.drainImportWorker(t)

	got := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	view := decodeJobView(t, got)
	require.Equal(t, model.ImportStateAwaitingConfirmation, view.State)

	// Simulate another job committing a mapping change against the same source.
	_, err := h.db.Exec(`UPDATE DOCS_ImportSource SET MappingRevision = MappingRevision + 1 WHERE Id = $1`, sourceID)
	require.NoError(t, err)

	res := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/confirm", actorID, confirmBody(t, view, false))
	require.Equal(t, http.StatusConflict, res.Code, res.Body.String())
	// Every 409 in this repository carries the shared conflict envelope, so a client parses a stale
	// preflight exactly as it parses a page-edit conflict.
	var conflict struct {
		Error *mmmodel.AppError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &conflict))
	require.NotNil(t, conflict.Error)
	require.Contains(t, conflict.Error.Id, app.ImportErrorPreflightStale, res.Body.String())

	// The job is queued for recomputation with its confirmation-specific state cleared, so the old
	// revision cannot be confirmed again.
	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateQueuedPreflight, job.State)
	require.Empty(t, job.PreflightRevision)
	require.Empty(t, job.Confirmation.PreflightRevision)
	require.True(t, job.MappingInputsChanged)

	// The prior plan's rows are gone, so a reviewer cannot act on a discarded plan.
	var preflightRows int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'preflight'`, jobID).Scan(&preflightRows))
	require.Zero(t, preflightRows)

	// The next worker pass produces a fresh, confirmable revision against the new source revision.
	h.drainImportWorker(t)
	job, err = h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State)
	require.Len(t, job.PreflightRevision, 64)
	require.NotEqual(t, view.Preflight.Revision, job.PreflightRevision)
	require.Equal(t, int64(2), job.PreflightMappingRevision)
}

// TestImportPreflight_AuthorFallback covers author resolution. A Confluence author who maps to a live
// Mattermost user is attributed to them; one who does not falls back to the importing actor with a stable
// reason, because an import must never fail merely because a person no longer has an account.
func TestImportPreflight_AuthorFallback(t *testing.T) {
	authorID := mmmodel.NewId()
	api := newEnabledMockAPI()
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	api.On("GetTeam", mock.Anything).Return(&mmmodel.Team{Id: mmmodel.NewId(), Name: "myteam"}, nil).Maybe()
	api.On("HasPermissionToTeam", mock.Anything, mock.Anything, mock.Anything).Return(true).Maybe()
	api.On("GetUser", mock.Anything).Return(&mmmodel.User{Id: mmmodel.NewId()}, nil).Maybe()
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
	// The fixture proposes this username for its pages' author.
	api.On("GetUserByUsername", importfixture.AuthorUsername).
		Return(&mmmodel.User{Id: authorID, Username: importfixture.AuthorUsername}, nil).Maybe()
	api.On("GetUserByUsername", mock.Anything).
		Return(nil, mmmodel.NewAppError("GetUserByUsername", "not_found", nil, "", http.StatusNotFound)).Maybe()

	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()
	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State)
	require.Equal(t, 2, job.PreflightSummary.Authors.Mapped)
	require.Zero(t, job.PreflightSummary.Authors.FallbackToActor)

	var resolved, reason string
	require.NoError(t, h.db.QueryRow(
		`SELECT ResolvedUserId, AuthorFallbackReason FROM DOCS_ImportStagedPage
		   WHERE JobId = $1 ORDER BY Ordinal LIMIT 1`, jobID).Scan(&resolved, &reason))
	require.Equal(t, authorID, resolved, "a resolvable Confluence author keeps authorship")
	require.Empty(t, reason)
}

// TestImportPreflight_AuthorFallsBackToActor is the unresolvable half of author resolution.
func TestImportPreflight_AuthorFallsBackToActor(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, 2, job.PreflightSummary.Authors.FallbackToActor)
	require.Zero(t, job.PreflightSummary.Authors.Mapped)

	var resolved, reason string
	require.NoError(t, h.db.QueryRow(
		`SELECT ResolvedUserId, AuthorFallbackReason FROM DOCS_ImportStagedPage
		   WHERE JobId = $1 ORDER BY Ordinal LIMIT 1`, jobID).Scan(&resolved, &reason))
	require.Equal(t, actorID, resolved, "an unresolvable author is attributed to the importing user")
	require.Equal(t, model.ImportFallbackUserNotFound, reason)

	// The fallback is reported, not silent: attribution is visible in the page's history.
	issues := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues?stage=preflight&per_page=100", actorID, nil)
	require.Equal(t, http.StatusOK, issues.Code)
	require.Contains(t, issues.Body.String(), importer.IssueAuthorFallbackToActor)
}

// TestImportPreflight_RecomputesAfterInterruption covers restart recovery. A job found already
// preflighting was interrupted mid-computation, and since preflight publishes all-or-nothing there is
// nothing partial to salvage — it is requeued and recomputed.
func TestImportPreflight_RecomputesAfterInterruption(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// Stand in for a process killed mid-preflight.
	_, err := h.db.Exec(`UPDATE DOCS_ImportJob SET State = 'preflighting' WHERE Id = $1`, jobID)
	require.NoError(t, err)

	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State)
	require.Len(t, job.PreflightRevision, 64)
	require.Equal(t, 2, job.PreflightSummary.Actions.Create)
}

// TestImportPreflight_CancelDuringReviewRecordsOutcomes ties the new states into the cancellation
// guarantee: a job canceled while awaiting confirmation still records an outcome for every staged page.
func TestImportPreflight_CancelDuringReviewRecordsOutcomes(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 3})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	h.drainImportWorker(t)

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code, cancel.Body.String())

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateCanceled, job.State)
	require.Equal(t, app.ImportErrorCanceledByUser, job.ErrorCode)
	// Three pages, each with a durable not-attempted outcome, plus the preflight results that were
	// already published.
	require.Equal(t, 3, job.FinalSummary.Actions.NotAttempted)

	var executionRows int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution'`, jobID).Scan(&executionRows))
	require.Equal(t, 3, executionRows)
}

// TestImportWorker_DoesNotStarveOnUnadvanceableStates is the regression test for a worker wedge. Work
// selection returns the first non-empty state in priority order, so a state the worker cannot advance would
// be picked on every pass and starve everything below it — and neither queued_import nor terminalizing is
// expirable, so the wedge would outlive the jobs that caused it.
func TestImportWorker_DoesNotStarveOnUnadvanceableStates(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	// A confirmed job parks in queued_import, which this release cannot execute.
	blocker := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, blocker.Code, blocker.Body.String())
	blockerID := decodeJobView(t, blocker).Id
	h.drainImportWorker(t)
	blockerView := decodeJobView(t, h.do(t, http.MethodGet, "/api/v1/imports/"+blockerID, actorID, nil))
	require.Equal(t, model.ImportStateAwaitingConfirmation, blockerView.State)
	require.Equal(t, http.StatusAccepted,
		h.do(t, http.MethodPost, "/api/v1/imports/"+blockerID+"/confirm", actorID, confirmBody(t, blockerView, true)).Code)

	// A later upload must still preflight.
	later := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, later.Code, later.Body.String())
	laterID := decodeJobView(t, later).Id

	h.drainImportWorker(t)
	job, err := h.store.GetImportJob(laterID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State,
		"a queued_import job must not starve later preflight work")

	// A confirmed-but-unexecutable job is still cancelable, so it cannot hold its staged bytes and its
	// per-user slot until an operator intervenes.
	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+blockerID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code, cancel.Body.String())
	blocked, err := h.store.GetImportJob(blockerID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateCanceled, blocked.State)
}

// TestImportWorker_TerminalizesFailedPreflight covers the other wedge and the terminalization step itself. A
// preflight failure records a terminal intent and hands off to the worker; the worker must finish the job
// rather than leaving it parked in the highest-priority state forever.
func TestImportWorker_TerminalizesFailedPreflight(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	sourceID, _ := seedImportSourceWithMapping(t, h, space, importfixture.RootExternalID, "", "")

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, selected.Code)

	// Delete the selected source so preflight cannot load its mappings; classifying against a missing
	// source would silently reclassify every mapped page as a create, so it must fail instead.
	_, err := h.db.Exec(`DELETE FROM DOCS_ImportSource WHERE Id = $1`, sourceID)
	require.NoError(t, err)

	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateFailed, job.State, "a failed preflight must reach a terminal state")
	require.Equal(t, app.ImportErrorSourceMissing, job.ErrorCode)
	// Terminalization wrote the durable report: every staged page carries a not-attempted-failed outcome.
	require.Equal(t, 2, job.FinalSummary.Actions.NotAttempted)
	require.Equal(t, 2, job.FinalSummary.Outcomes[string(model.ImportOutcomeNotAttemptedFailure)])
	require.Zero(t, job.StagedBytes)

	var executionRows int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution' AND Outcome = $2`,
		jobID, string(model.ImportOutcomeNotAttemptedFailure)).Scan(&executionRows))
	require.Equal(t, 2, executionRows)

	// The final summary is projected through the API, which is the only way a user sees the outcome.
	view := decodeJobView(t, h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil))
	require.NotNil(t, view.Final, "a terminal job must report its final summary")
	require.Equal(t, 2, view.Final.Counts.Outcomes[string(model.ImportOutcomeNotAttemptedFailure)])
	require.NotNil(t, view.Error)
	require.Equal(t, app.ImportErrorSourceMissing, view.Error.Code)
}

// TestImportTerminalization_IsIdempotent covers restart safety: a crash mid-terminalization must resume
// rather than collide with the outcomes it already wrote.
func TestImportTerminalization_IsIdempotent(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 3})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	h.drainImportWorker(t)

	// Write an outcome for one page, then park the job in terminalizing as an interrupted run would.
	_, err := h.db.Exec(
		`INSERT INTO DOCS_ImportResult
		   (JobId, Stage, Ordinal, EntityType, ExternalId, Title, ActualAction, Outcome, CreateAt, UpdateAt)
		 VALUES ($1, 'execution', 0, 'page', $2, 'Already recorded', 'not_attempted', $3, 1, 1)`,
		jobID, importfixture.RootExternalID, string(model.ImportOutcomeNotAttemptedFailure))
	require.NoError(t, err)
	_, err = h.db.Exec(
		`UPDATE DOCS_ImportJob SET State='terminalizing', TerminalIntent='failed', ErrorCode='preflight_failed'
		   WHERE Id=$1`, jobID)
	require.NoError(t, err)

	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateFailed, job.State)
	// Three pages, three outcomes: the pre-existing row was kept, not duplicated or overwritten.
	var rows int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution'`, jobID).Scan(&rows))
	require.Equal(t, 3, rows)
	var keptTitle string
	require.NoError(t, h.db.QueryRow(
		`SELECT Title FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution' AND Ordinal = 0`,
		jobID).Scan(&keptTitle))
	require.Equal(t, "Already recorded", keptTitle, "an existing execution outcome must not be overwritten")
}

// TestImportPreflight_ChargesRetainedRows covers the accounting hole: preflight rows are retained for the
// job's whole life, so publishing them without charging lets repeated preflight/cancel cycles accumulate
// storage against a figure that barely moves.
func TestImportPreflight_ChargesRetainedRows(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 4, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	before, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	h.drainImportWorker(t)
	after, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)

	require.Greater(t, after.PreflightRetainedBytes, int64(0), "a published plan must be charged")
	require.Greater(t, after.RetainedBytes, before.RetainedBytes)
	require.GreaterOrEqual(t, after.RetainedBytes-before.RetainedBytes, after.PreflightRetainedBytes)
	require.Greater(t, after.PreflightRetainedIssueBytes, int64(0))
	require.Greater(t, after.RetainedIssueBytes, before.RetainedIssueBytes)

	// Recomputing replaces the charge instead of accumulating it: the rows the previous plan wrote are gone.
	_, err = h.db.Exec(`UPDATE DOCS_ImportJob SET State='queued_preflight' WHERE Id=$1`, jobID)
	require.NoError(t, err)
	h.drainImportWorker(t)
	recomputed, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, after.PreflightRetainedBytes, recomputed.PreflightRetainedBytes)
	require.Equal(t, after.RetainedBytes, recomputed.RetainedBytes,
		"a recomputed plan must not double-charge the plan it replaced")

	// The true-up at cancellation now reflects the preflight rows that remain retained.
	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code)
	canceled, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, canceled.RetainedBytes, canceled.RetainedReservedBytes)
	require.Greater(t, canceled.RetainedReservedBytes, recomputed.PreflightRetainedBytes,
		"the trued-up reservation must still cover the retained preflight rows")
}

// seedLocalChain creates a chain of live pages depth levels deep and returns the deepest one.
func seedLocalChain(t *testing.T, h *apiTestHarness, space *model.Space, depth int) *model.Page {
	t.Helper()
	var page *model.Page
	parentID := ""
	for range depth {
		page = seedPage(t, h.store, space.Id, space.ChannelId, parentID)
		parentID = page.Id
	}
	return page
}

// mapExternalIDToPage points an existing source's mapping for externalID at a local page.
func mapExternalIDToPage(t *testing.T, h *apiTestHarness, sourceID, externalID string, page *model.Page) {
	t.Helper()
	_, err := h.db.Exec(
		`UPDATE DOCS_ImportEntity SET LocalId = $1, LastAppliedParentId = COALESCE($2, '')
		   WHERE ImportSourceId = $3 AND ExternalId = $4`,
		page.Id, page.ParentId, sourceID, externalID)
	require.NoError(t, err)
}

// TestImportPreflight_ProjectsDepthAcrossStagedTree covers a tree that is legal in the bundle and legal
// against the database, but illegal once the two are combined. The chain's depth only exists in the plan —
// no row reveals it — so a projection that queries only existing parents approves a page tree the target
// cannot hold, and execution discovers it far too late.
func TestImportPreflight_ProjectsDepthAcrossStagedTree(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	sourceID, _ := seedImportSourceWithMapping(t, h, space, importfixture.RootExternalID, "", "")

	// Put the mapped root deep enough that only two more levels fit beneath it.
	deep := seedLocalChain(t, h, space, model.MaxPageDepth-1)
	mapExternalIDToPage(t, h, sourceID, importfixture.RootExternalID, deep)

	// A four-page chain: 100 (mapped, at depth 9) -> 101 -> 102 -> 103.
	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id),
		importfixture.Options{Pages: 4, Chain: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, selected.Code, selected.Body.String())

	h.drainImportWorker(t)

	results := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/preflight-results?per_page=100", actorID, nil)
	require.Equal(t, http.StatusOK, results.Code)
	var page paginatedResponse[model.ImportPreflightResultView]
	require.NoError(t, json.Unmarshal(results.Body.Bytes(), &page))
	byExternal := map[string]model.ImportPreflightResultView{}
	for _, item := range page.Items {
		byExternal[item.ExternalId] = item
	}

	// 101 is the last level that fits; 102 breaches the depth limit and 103 follows it.
	require.Equal(t, model.ImportActionCreate, byExternal["101"].PlannedAction)
	require.Equal(t, model.ImportActionBlocked, byExternal["102"].PlannedAction,
		"a page projected past the depth limit must be blocked")
	require.Equal(t, model.ImportActionBlocked, byExternal["103"].PlannedAction,
		"a descendant of a blocked page must be blocked, not left pointing at a page that will never exist")
	require.Empty(t, byExternal["103"].LocalId, "a blocked create must not advertise a planned page id")

	issues := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues?stage=preflight&per_page=100", actorID, nil)
	require.Equal(t, http.StatusOK, issues.Code)
	body := issues.Body.String()
	require.Contains(t, body, importer.IssueTargetDepthExceeded)
	require.Contains(t, body, importer.IssueParentBlocked)

	// The plan the store persisted agrees: a blocked page carries no planned id for execution to use.
	var plannedID string
	require.NoError(t, h.db.QueryRow(
		`SELECT PlannedPageId FROM DOCS_ImportStagedPage WHERE JobId = $1 AND ExternalId = '103'`,
		jobID).Scan(&plannedID))
	require.Empty(t, plannedID)
}

// TestImportPreflight_MappingCapacityCountsOnlyNewPages covers the double-count: existing mappings are held
// in one map and every mapped page seen in the bundle was also recorded in another, so adding the two
// lengths counted those pages twice — blocking valid creates and making the result depend on bundle order.
func TestImportPreflight_MappingCapacityCountsOnlyNewPages(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())
	sourceID, mapped := seedImportSourceWithMapping(t, h, space, importfixture.RootExternalID, "", "")
	require.NotNil(t, mapped)

	// Sit the source just under its cap, with the bundle's root among the mappings. Before the fix, the
	// mapped root was counted twice and the two new pages were blocked as over capacity.
	_, err := h.db.Exec(
		`INSERT INTO DOCS_ImportEntity
		   (ImportSourceId, EntityType, ExternalId, LocalId, LastSourceContentHash, LastAppliedContentHash,
		    LastAppliedParentId, LastSourceParentExternalId, LastSourceTitle, LastSourceOrdinal,
		    FirstJobId, LastSeenJobId, CreateAt, UpdateAt)
		 SELECT $1, 'page', 'filler-' || g, 'flr' || lpad(g::text, 23, '0'), '', '', '', '', 'Filler', 0,
		        $2, $2, 1, 1
		   FROM generate_series(1, $3) AS g`,
		sourceID, mmmodel.NewId(), model.ImportMaxMappingsPerSource-3)
	require.NoError(t, err)

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 3})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id
	selected := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/source", actorID,
		json.RawMessage(fmt.Sprintf(`{"mode":"existing","import_source_id":%q}`, sourceID)))
	require.Equal(t, http.StatusAccepted, selected.Code, selected.Body.String())

	h.drainImportWorker(t)

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateAwaitingConfirmation, job.State)
	// 4998 existing mappings + 2 new pages = 5000, exactly the cap, so nothing is blocked.
	require.Equal(t, 2, job.PreflightSummary.Actions.Create,
		"a mapped page present in the bundle must not be counted twice against the mapping cap")
	require.Zero(t, job.PreflightSummary.Actions.Blocked)
}

// TestImportPreflight_HashesTheEffectiveAuthorProposal covers the hash/resolution split. Inspection and
// preflight must hash the same proposal author resolution will use, or changing a page's author leaves the
// source hash untouched and the change is classified as "nothing to do".
func TestImportPreflight_HashesTheEffectiveAuthorProposal(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	// Two bundles differing only in the page-level author proposal. The fixture maps only the root page's
	// account in its manifest, so pages beyond the root rely on the page fallback — exactly the case where a
	// manifest-only hash is blind to the change.
	first := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	firstID := decodeJobView(t, first).Id
	h.drainImportWorker(t)

	second := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 2, AuthorUsernameOverride: "someone-else"})
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	secondID := decodeJobView(t, second).Id
	h.drainImportWorker(t)

	readHash := func(jobID, externalID string) string {
		var hash string
		require.NoError(t, h.db.QueryRow(
			`SELECT IncomingSourceContentHash FROM DOCS_ImportStagedPage WHERE JobId = $1 AND ExternalId = $2`,
			jobID, externalID).Scan(&hash))
		require.Len(t, hash, 64)
		return hash
	}
	require.NotEqual(t, readHash(firstID, "101"), readHash(secondID, "101"),
		"changing the author a page resolves through must change its source-content hash")
}
