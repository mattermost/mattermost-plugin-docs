// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/importfixture"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// decodeReport fetches a report and decodes it, asserting the download contract on the way.
func decodeReport(t *testing.T, h *apiTestHarness, jobID, actorID, stage string) (*model.ImportReport, *httptest.ResponseRecorder) {
	t.Helper()
	rec := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage="+stage, actorID, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="docs-import-`+jobID+`-`+stage+`.json"`,
		rec.Header().Get("Content-Disposition"))

	var report model.ImportReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report), "a report must be one valid JSON object")
	return &report, rec
}

// TestImportReport_PreflightAndFinal covers both stages of the download. The two reports answer different
// questions — what a plan would do, and what an import actually did — so a finished job must not present its
// historical classifications as outcomes.
func TestImportReport_PreflightAndFinal(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(teamID),
		importfixture.Options{Pages: 3, Chain: true, WithFindings: true}, "")

	// The final report does not exist until the job is terminal: the job is real and the report will exist, so
	// this is a conflict rather than a not-found.
	notReady := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage=final", actorID, nil)
	require.Equal(t, http.StatusConflict, notReady.Code, notReady.Body.String())

	preflight, _ := decodeReport(t, h, jobID, actorID, "preflight")
	require.Equal(t, model.ImportReportVersion, preflight.ReportVersion)
	require.Equal(t, "preflight", preflight.Stage)
	require.Equal(t, jobID, preflight.JobId)
	require.Equal(t, "DOCS", preflight.Source.SpaceKey)
	require.Equal(t, teamID, preflight.Target.TeamId)
	require.False(t, preflight.Fidelity.FullFidelity, "no import is ever full fidelity")
	require.Equal(t, model.FidelityScopePagesOnly, preflight.Fidelity.Scope)
	require.Len(t, preflight.Results, 3, "one reviewed result per staged page")
	for _, result := range preflight.Results {
		require.Equal(t, string(model.ImportStagePreflight), result.Stage)
		require.Equal(t, string(model.ImportActionCreate), result.PlannedAction)
		require.Empty(t, result.ActualAction, "a plan has no actual action yet")
	}
	// Inspection findings describe the bundle and belong to both reports; the fixture emits several.
	require.Contains(t, reportIssueCodes(preflight), importer.IssueCommentsNotImported)
	require.NotEmpty(t, preflight.Counts.IssuesBySeverity)

	job := h.confirmAndExecute(t, actorID, jobID, view, true)
	require.Equal(t, 3, job.FinalSummary.Actions.Create)

	final, _ := decodeReport(t, h, jobID, actorID, "final")
	require.Equal(t, "final", final.Stage)
	require.Equal(t, job.FinishedAt, final.GeneratedAt)
	require.Len(t, final.Results, 3)
	for _, result := range final.Results {
		require.Equal(t, string(model.ImportStageExecution), result.Stage,
			"a final report presents execution outcomes, never the historical plan")
		require.Equal(t, string(model.ImportOutcomeCreated), result.Outcome)
		require.Equal(t, string(model.ImportActionCreate), result.ActualAction)
		require.True(t, mmmodel.IsValidId(result.Entity.LocalId), "a created page names the page it created")
	}
	require.Equal(t, 3, final.Counts.Actions[string(model.ImportActionCreate)])
	require.Equal(t, 3, final.Counts.Outcomes[string(model.ImportOutcomeCreated)])
	// The same immutable inspection findings appear in both reports.
	require.Contains(t, reportIssueCodes(final), importer.IssueCommentsNotImported)

	// The preflight report stays downloadable after the import, so the plan and the outcome can be compared.
	stillThere, _ := decodeReport(t, h, jobID, actorID, "preflight")
	require.Len(t, stillThere.Results, 3)
	require.Equal(t, "preflight", stillThere.Stage)
}

// TestImportReport_DeclaresWhetherItDescribesOnePlan covers the report's own consistency claim.
//
// The rows are read by several queries in succession rather than under one snapshot, so a plan recomputed
// while a report is being written yields a document that parses, whose counts match its contents, and whose
// rows come from two different plans. Nothing about it looks wrong, which is exactly why it has to say so.
func TestImportReport_DeclaresWhetherItDescribesOnePlan(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 2}, "")

	// The ordinary case: nothing moved, and the report names the plan its rows came from.
	settled, _ := decodeReport(t, h, jobID, actorID, "preflight")
	require.True(t, settled.Integrity.Complete)
	require.Equal(t, view.Preflight.Revision, settled.Integrity.PlanRevision)
	require.Empty(t, settled.Integrity.Reason)

	// Now the race, made deterministic: the stream is authorized against one revision and the plan is
	// republished before its rows are written. Doing it between the two steps is what a concurrent
	// republication does to a download already in flight.
	stream, appErr := h.plugin.service.PrepareImportReport(jobID, actorID, "preflight")
	require.Nil(t, appErr)
	_, err := h.db.Exec(`UPDATE DOCS_ImportJob SET PreflightRevision=$1 WHERE Id=$2`, strings.Repeat("a", 64), jobID)
	require.NoError(t, err)

	var body strings.Builder
	require.NoError(t, stream.Stream(&body))

	var mixed model.ImportReport
	require.NoError(t, json.Unmarshal([]byte(body.String()), &mixed), "a report must stay valid JSON either way")
	require.False(t, mixed.Integrity.Complete,
		"a report whose plan was recomputed under it must not claim to describe one plan")
	require.Equal(t, model.ImportReportIncompletePlanRecomputed, mixed.Integrity.Reason)
	require.Equal(t, view.Preflight.Revision, mixed.Integrity.PlanRevision,
		"the revision named must be the one the rows were read against")
}

// TestImportReport_CountsDiscoveredLinks covers a figure both reports declared and neither measured.
//
// The summaries carried a links block from the start and nothing ever populated it, so every report stated zero
// same-source links, zero unresolved, zero file placeholders — for bundles full of them. A count nobody fills in
// is worse than an absent one: a reader cannot tell it apart from a real zero, and this one was being read as
// "this bundle has no cross-page links at all".
func TestImportReport_CountsDiscoveredLinks(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	// The fixture's pages after the first carry a braced page placeholder in a link plus one left in ordinary
	// text, so there is something real to count.
	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 4, WithFindings: true}, "")

	preflight, _ := decodeReport(t, h, jobID, actorID, "preflight")
	discovered := preflight.Counts.Links["same_source"] + preflight.Counts.Links["unresolved"]
	require.Greater(t, discovered, 0, "the bundle carries placeholder links, so the plan must count them")

	// The same figures reach the job view the wizard reads, not just the downloadable report.
	require.Equal(t, preflight.Counts.Links["same_source"], view.Preflight.Counts.Links["same_source"])

	// Executing an import does not change how many placeholders the bundle contained, so the final report
	// reports the same figures rather than recomputing or losing them.
	h.confirmAndExecute(t, actorID, jobID, view, true)
	final, _ := decodeReport(t, h, jobID, actorID, "final")
	require.Equal(t, preflight.Counts.Links, final.Counts.Links)

	// Categories the importer cannot resolve yet are absent rather than present and zero.
	require.NotContains(t, final.Counts.Links, "cross_source_unique")
	require.NotContains(t, final.Counts.Links, "ambiguous")
}

// reportIssueCodes collects the codes a report carries.
func reportIssueCodes(report *model.ImportReport) []string {
	codes := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

// TestImportReport_NeverLeaksBaselinesOrBodies pins the redaction contract. A report explains outcomes; the
// hashes and page bodies that make applying them safe stay server-side, and a leaked baseline would let a
// client reason about — or forge — an approval.
func TestImportReport_NeverLeaksBaselinesOrBodies(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	jobID, view := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id),
		importfixture.Options{Pages: 2, WithFindings: true}, newSourceSelection("Acme Confluence / DOCS"))
	h.confirmAndExecute(t, actorID, jobID, view, false)

	// Read the raw bytes, not the decoded shape: the point is that these strings are nowhere in the payload,
	// including inside a details map that a typed assertion would skip over.
	for _, stage := range []string{"preflight", "final"} {
		rec := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage="+stage, actorID, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()

		for _, forbidden := range []string{
			"canonical_body", "search_text", "CanonicalBody", "SearchText",
			"incoming_source_content_hash", "preflight_current_content_hash", "preflight_mapping_content_hash",
			"last_applied_content_hash", "last_source_content_hash",
			"bundle_sha256", "import.jsonl", "data/",
		} {
			require.NotContains(t, body, forbidden, "%s report must not carry %q", stage, forbidden)
		}
		// The bundle's actual page body text must not appear either, by any name.
		require.NotContains(t, body, "This page was generated for manual import verification.")
		// Nor the confirmation's own baselines, or the manifest user rows.
		require.NotContains(t, body, "mattermost_username")
	}
}

// TestImportReport_Authorization covers who may download a report. These rows name pages, titles, and local ids
// inside a Space, so anyone who cannot reach that Space gets a not-found — not a forbidden, which would confirm
// the import exists.
func TestImportReport_Authorization(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	jobID, _ := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 1}, "")

	t.Run("another user gets not found", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage=preflight", mmmodel.NewId(), nil)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("no auth header is rejected", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage=preflight", "", nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("an unknown stage is a bad request", func(t *testing.T) {
		for _, stage := range []string{"", "inspection", "bogus"} {
			rec := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage="+stage, actorID, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, "stage=%q", stage)
		}
	})

	t.Run("a missing job is not found", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/v1/imports/"+mmmodel.NewId()+"/report?stage=preflight", actorID, nil)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestImportReport_HiddenAfterEntitlementLoss covers losing access to the target after the fact. A report is a
// standing copy of Space content, so it must stop being readable when the Space does.
func TestImportReport_HiddenAfterEntitlementLoss(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	jobID, _ := h.uploadAndPreflight(t, actorID, existingTargetRequest(space.Id),
		importfixture.Options{Pages: 1}, newSourceSelection("Acme Confluence / DOCS"))
	ok := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage=preflight", actorID, nil)
	require.Equal(t, http.StatusOK, ok.Code)

	// Removing the Space removes the entitlement the download rests on.
	require.NoError(t, h.store.DeleteSpace(space.Id))

	gone := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage=preflight", actorID, nil)
	require.Equal(t, http.StatusNotFound, gone.Code,
		"a report naming pages in a Space must be hidden once that Space is unreachable")
}

// TestImportReport_StreamsEveryRowAcrossBatches covers the streaming itself. The reader pages in batches, so a
// job with more rows than one batch is where an off-by-one in the cursor would silently drop or repeat rows —
// and a report that quietly omits pages is worse than one that fails.
func TestImportReport_StreamsEveryRowAcrossBatches(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	// More pages than importReportBatch, so the results and issues both span several reads.
	const pages = 470
	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: pages}, "")
	job := h.confirmAndExecute(t, actorID, jobID, view, true)
	require.Equal(t, pages, job.FinalSummary.Actions.Create)

	final, _ := decodeReport(t, h, jobID, actorID, "final")
	require.Len(t, final.Results, pages, "every committed outcome must appear exactly once")

	// Every external id appears once and only once, which is what a cursor bug would break.
	seen := map[string]int{}
	for _, result := range final.Results {
		seen[result.Entity.ExternalId]++
	}
	require.Len(t, seen, pages)
	for externalID, count := range seen {
		require.Equal(t, 1, count, "external id %s appeared %d times", externalID, count)
	}

	// The row count matches what the database holds, so nothing was dropped between the two.
	var stored int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution'`, jobID).Scan(&stored))
	require.Equal(t, stored, len(final.Results))

	var storedIssues int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportIssue WHERE JobId = $1 AND Stage IN ('inspection', 'execution')`,
		jobID).Scan(&storedIssues))
	require.Equal(t, storedIssues, len(final.Issues))
}

// TestImportReport_CanceledJobReportsWhatItHeld covers the report a job that never ran still owes. A canceled
// import is exactly when its owner most needs to know which pages it was holding and that none were touched.
func TestImportReport_CanceledJobReportsWhatItHeld(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 3})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code)

	final, _ := decodeReport(t, h, jobID, actorID, "final")
	require.Len(t, final.Results, 3, "a canceled job still reports every page it held")
	for _, result := range final.Results {
		require.Equal(t, string(model.ImportActionNotAttempted), result.ActualAction)
		require.Equal(t, string(model.ImportOutcomeNotAttemptedCancel), result.Outcome)
	}
	require.Equal(t, 3, final.Counts.Actions[string(model.ImportActionNotAttempted)])
	require.Contains(t, reportIssueCodes(final), importer.IssueNotAttemptedCanceled)

	// Cancelling before any preflight ran means there is no plan to hand out.
	notReady := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/report?stage=preflight", actorID, nil)
	require.Equal(t, http.StatusConflict, notReady.Code)
}

// TestImportReport_IsValidJSONWithNoRows covers the empty case. Streaming a JSON document by hand is exactly
// where a zero-element array turns into a trailing comma, so the shape is checked with nothing in it.
func TestImportReport_IsValidJSONWithNoRows(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	jobID, view := h.uploadAndPreflight(t, actorID, newTargetRequest(mmmodel.NewId()),
		importfixture.Options{Pages: 1}, "")
	h.confirmAndExecute(t, actorID, jobID, view, true)

	// Strip every row the report would stream, leaving only its metadata.
	_, err := h.db.Exec(`DELETE FROM DOCS_ImportResult WHERE JobId = $1`, jobID)
	require.NoError(t, err)
	_, err = h.db.Exec(`DELETE FROM DOCS_ImportIssue WHERE JobId = $1`, jobID)
	require.NoError(t, err)

	report, rec := decodeReport(t, h, jobID, actorID, "final")
	require.Empty(t, report.Results)
	require.Empty(t, report.Issues)
	require.Equal(t, jobID, report.JobId)
	require.Contains(t, rec.Body.String(), `"results":[]`)
	require.Contains(t, rec.Body.String(), `"issues":[]`)
	require.True(t, strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{"))
	require.True(t, strings.HasSuffix(strings.TrimSpace(rec.Body.String()), "}"))
}
