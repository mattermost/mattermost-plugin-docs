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
