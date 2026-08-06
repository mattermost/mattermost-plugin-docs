// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/importfixture"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// importMockOptions configures the plugin API stubs the import paths depend on.
type importMockOptions struct {
	// teamMember controls whether the actor reads as an active member of the target team.
	teamMember bool
	// channelMember controls whether the actor reads as a member of an existing Space's backing channel.
	channelMember bool
	// canCreateChannel controls the PermissionCreatePublicChannel check used by new-Space targets.
	canCreateChannel bool
	// teamName is returned by GetTeam, and is compared against the bundle's advisory team.
	teamName string
	// deactivatedActor makes GetUser report the acting user as deleted, which the read paths treat as
	// lost entitlement.
	deactivatedActor bool
}

// newImportMockAPI builds a plugintest API with only the stubs the import flow needs, so a test can
// deny exactly one gate and assert the resulting status.
func newImportMockAPI(o importMockOptions) *plugintest.API {
	api := newEnabledMockAPI()

	if o.teamMember {
		api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	} else {
		api.On("GetTeamMember", mock.Anything, mock.Anything).
			Return(nil, mmmodel.NewAppError("GetTeamMember", "not_found", nil, "", http.StatusNotFound)).Maybe()
	}

	if o.channelMember {
		api.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil).Maybe()
	} else {
		api.On("GetChannelMember", mock.Anything, mock.Anything).
			Return(nil, mmmodel.NewAppError("GetChannelMember", "not_found", nil, "", http.StatusNotFound)).Maybe()
	}

	api.On("HasPermissionToTeam", mock.Anything, mock.Anything, mock.Anything).Return(o.canCreateChannel).Maybe()

	name := o.teamName
	if name == "" {
		name = "myteam"
	}
	api.On("GetTeam", mock.Anything).Return(&mmmodel.Team{Id: mmmodel.NewId(), Name: name}, nil).Maybe()

	// Read paths re-resolve the actor to confirm they are still a live, active user.
	actor := &mmmodel.User{Id: mmmodel.NewId()}
	if o.deactivatedActor {
		actor.DeleteAt = mmmodel.GetMillis()
	}
	api.On("GetUser", mock.Anything).Return(actor, nil).Maybe()
	// Preflight resolves each Confluence author's proposed username. Absent by default so a fixture's
	// proposals fall back to the importing actor; tests that pin a real mapping stub it themselves.
	api.On("GetUserByUsername", mock.Anything).
		Return(nil, mmmodel.NewAppError("GetUserByUsername", "not_found", nil, "", http.StatusNotFound)).Maybe()

	return api
}

// multipartImportBody builds the multipart body the upload endpoint expects. A nil requestJSON omits
// the request part; a nil bundle omits the bundle part; extraParts adds additional named parts.
func multipartImportBody(t *testing.T, requestJSON []byte, bundle []byte, extraParts map[string][]byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if requestJSON != nil {
		part, err := w.CreateFormField("request")
		require.NoError(t, err)
		_, err = part.Write(requestJSON)
		require.NoError(t, err)
	}
	if bundle != nil {
		part, err := w.CreateFormFile("bundle", "bundle.zip")
		require.NoError(t, err)
		_, err = part.Write(bundle)
		require.NoError(t, err)
	}
	for name, body := range extraParts {
		part, err := w.CreateFormField(name)
		require.NoError(t, err)
		_, err = part.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return w.FormDataContentType(), buf.Bytes()
}

// doUpload posts a multipart import upload as userID. An empty userID omits the auth header.
func (h *apiTestHarness) doUpload(t *testing.T, userID, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/imports/preflight", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if userID != "" {
		req.Header.Set("Mattermost-User-ID", userID)
	}
	rec := httptest.NewRecorder()
	h.plugin.ServeHTTP(&plugin.Context{}, rec, req)
	return rec
}

// uploadFixture builds a fixture bundle and uploads it with the given target request.
func (h *apiTestHarness) uploadFixture(t *testing.T, userID string, target string, opts importfixture.Options) *httptest.ResponseRecorder {
	t.Helper()
	bundle, err := importfixture.Build(opts)
	require.NoError(t, err)
	contentType, body := multipartImportBody(t, []byte(target), bundle.Zip, nil)
	return h.doUpload(t, userID, contentType, body)
}

func newTargetRequest(teamID string) string {
	return fmt.Sprintf(`{"target":{"kind":"new","team_id":%q}}`, teamID)
}

func existingTargetRequest(spaceID string) string {
	return fmt.Sprintf(`{"target":{"kind":"existing","space_id":%q}}`, spaceID)
}

func decodeJobView(t *testing.T, rec *httptest.ResponseRecorder) *model.ImportJobView {
	t.Helper()
	var view model.ImportJobView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	return &view
}

func TestHandleCreateImport_NewSpaceTarget(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 4, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	view := decodeJobView(t, rec)
	require.True(t, mmmodel.IsValidId(view.Id))
	// A new Space has exactly one possible source identity, so there is nothing to choose and the job
	// goes straight to preflight.
	require.Equal(t, model.ImportStateQueuedPreflight, view.State)
	require.Equal(t, model.ImportTargetNew, view.Target.Kind)
	require.Equal(t, teamID, view.Target.TeamId)
	require.False(t, view.Target.Existed)
	// The target Space id is pre-generated even though no Space exists yet.
	require.True(t, mmmodel.IsValidId(view.Target.SpaceId))
	require.NotNil(t, view.SelectedSource)
	require.Equal(t, model.ImportSourceModeNew, view.SelectedSource.Mode)

	require.Equal(t, 2, view.Bundle.Version)
	require.Equal(t, 4, view.Bundle.Counts.Pages)
	require.Equal(t, 1, view.Bundle.Counts.Comments)
	require.Equal(t, 1, view.Bundle.Counts.Attachments)
	require.Equal(t, 1, view.Bundle.Counts.RestrictedEmittedPages)
	require.Equal(t, 1, view.Bundle.Counts.RestrictedManifestOnly)
	require.Equal(t, "DOCS", view.Bundle.Source.SpaceKey)
	// New-Space metadata defaults come from the bundle and stay editable until confirmation.
	require.Equal(t, "Docs", view.Bundle.SpaceDefaults.Title)
	require.NotEmpty(t, view.Bundle.SpaceDefaults.Description)

	// Acknowledgements knowable from the bundle are demanded up front; reimport_existing_pages is
	// only added once preflight has compared against existing mappings.
	require.ElementsMatch(t,
		[]string{model.ImportAckNewSpaceMetadata, model.ImportAckPageOnlyPartial, model.ImportAckWidenRestricted},
		view.RequiredAcknowledgements)

	// Nothing was written to the page tree: this phase only stages input.
	pages, err := h.store.GetSpacePages(view.Target.SpaceId, 0, 10)
	require.NoError(t, err)
	require.Empty(t, pages)
}

func TestHandleCreateImport_ExistingSpaceTarget(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, channelMember: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	view := decodeJobView(t, rec)
	// An existing Space may already hold several sources, so the user must still pick one.
	require.Equal(t, model.ImportStateAwaitingSource, view.State)
	require.Equal(t, model.ImportTargetExisting, view.Target.Kind)
	require.Equal(t, space.Id, view.Target.SpaceId)
	require.Equal(t, space.TeamId, view.Target.TeamId, "the team must come from the Space, never the request")
	require.True(t, view.Target.Existed)
	require.Nil(t, view.SelectedSource)
	// No sources exist in this Space yet, so there is nothing to suggest.
	require.Empty(t, view.SourceCandidates)
}

func TestHandleCreateImport_RejectsBrokenBundles(t *testing.T) {
	cases := map[string]int{
		importfixture.CorruptCountMismatch: http.StatusBadRequest,
		importfixture.CorruptBadChecksum:   http.StatusBadRequest,
		importfixture.CorruptMissingParent: http.StatusBadRequest,
		importfixture.CorruptBadTipTap:     http.StatusBadRequest,
		// A structurally valid bundle whose hierarchy breaches the Docs depth limit is not
		// processable rather than malformed.
		importfixture.CorruptDeepHierarchy: http.StatusUnprocessableEntity,
	}
	for mode, wantStatus := range cases {
		t.Run(mode, func(t *testing.T) {
			api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
			h := openTestPlugin(t, api)
			actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

			rec := h.uploadFixture(t, actorID, newTargetRequest(teamID),
				importfixture.Options{Pages: 12, Corrupt: mode})
			require.Equal(t, wantStatus, rec.Code, rec.Body.String())

			// The rejection carries the importer's stable code and no internal detail.
			var appErr mmmodel.AppError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &appErr))
			require.Empty(t, appErr.DetailedError)

			// A rejected upload creates no job.
			list := h.do(t, http.MethodGet, "/api/v1/imports", actorID, nil)
			require.Equal(t, http.StatusOK, list.Code)
			require.Contains(t, list.Body.String(), `"items":[]`)
		})
	}
}

func TestHandleCreateImport_MultipartValidation(t *testing.T) {
	bundle, err := importfixture.Build(importfixture.Options{Pages: 1})
	require.NoError(t, err)
	teamID := mmmodel.NewId()
	validRequest := []byte(newTargetRequest(teamID))

	t.Run("missing request part", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true}))
		contentType, body := multipartImportBody(t, nil, bundle.Zip, nil)
		rec := h.doUpload(t, mmmodel.NewId(), contentType, body)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing bundle part", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true}))
		contentType, body := multipartImportBody(t, validRequest, nil, nil)
		rec := h.doUpload(t, mmmodel.NewId(), contentType, body)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unknown part", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true}))
		contentType, body := multipartImportBody(t, validRequest, bundle.Zip, map[string][]byte{"surprise": []byte("x")})
		rec := h.doUpload(t, mmmodel.NewId(), contentType, body)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid request json", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true}))
		contentType, body := multipartImportBody(t, []byte(`{"target":`), bundle.Zip, nil)
		rec := h.doUpload(t, mmmodel.NewId(), contentType, body)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("target kind mismatch", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true}))
		// A new-Space target must not carry a space_id.
		req := fmt.Sprintf(`{"target":{"kind":"new","team_id":%q,"space_id":%q}}`, teamID, mmmodel.NewId())
		contentType, body := multipartImportBody(t, []byte(req), bundle.Zip, nil)
		rec := h.doUpload(t, mmmodel.NewId(), contentType, body)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true}))
		contentType, body := multipartImportBody(t, validRequest, bundle.Zip, nil)
		rec := h.doUpload(t, "", contentType, body)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestHandleCreateImport_Authorization(t *testing.T) {
	t.Run("new space requires team membership", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: false, canCreateChannel: true}))
		rec := h.uploadFixture(t, mmmodel.NewId(), newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("new space requires create-channel permission", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: false}))
		rec := h.uploadFixture(t, mmmodel.NewId(), newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("existing space requires membership", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, channelMember: false}))
		space := seedSpace(t, h.store, mmmodel.NewId())
		rec := h.uploadFixture(t, mmmodel.NewId(), existingTargetRequest(space.Id), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("nonexistent space is forbidden not found", func(t *testing.T) {
		h := openTestPlugin(t, newImportMockAPI(importMockOptions{teamMember: true, channelMember: true}))
		rec := h.uploadFixture(t, mmmodel.NewId(), existingTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
		// A missing Space and a Space the caller cannot see are deliberately indistinguishable.
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}

func TestHandleGetImport_ActorOnlyVisibility(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, otherID, teamID := mmmodel.NewId(), mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	own := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	require.Equal(t, http.StatusOK, own.Code)
	require.Equal(t, jobID, decodeJobView(t, own).Id)

	// Another user's job reads as not found, not forbidden, so the route cannot be used to probe.
	other := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, otherID, nil)
	require.Equal(t, http.StatusNotFound, other.Code)

	unknown := h.do(t, http.MethodGet, "/api/v1/imports/"+mmmodel.NewId(), actorID, nil)
	require.Equal(t, http.StatusNotFound, unknown.Code)
}

func TestHandleGetImportIssues(t *testing.T) {
	// The fixture's advisory team differs from the resolved team name, so a team-mismatch warning is
	// recorded alongside the findings the bundle itself produces.
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true, teamName: "another-team"})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 3, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	issues := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues", actorID, nil)
	require.Equal(t, http.StatusOK, issues.Code)

	var page paginatedResponse[model.ImportIssue]
	require.NoError(t, json.Unmarshal(issues.Body.Bytes(), &page))
	require.NotEmpty(t, page.Items)

	codes := map[string]bool{}
	for _, i := range page.Items {
		require.Equal(t, string(model.ImportStageInspection), i.Stage)
		codes[i.Code] = true
	}
	require.True(t, codes["manifest_warning"], "expected the producer warning to be recorded")
	require.True(t, codes["attachments_not_imported"], "expected attachments to be reported as not imported")
	require.True(t, codes["bundle_team_mismatch"], "expected the advisory team mismatch to be reported")

	// Filters are validated rather than silently returning an empty page.
	badStage := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues?stage=nowhere", actorID, nil)
	require.Equal(t, http.StatusBadRequest, badStage.Code)

	// A stage with no rows yet is a valid, empty page.
	preflight := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues?stage=preflight", actorID, nil)
	require.Equal(t, http.StatusOK, preflight.Code)
	require.Contains(t, preflight.Body.String(), `"items":[]`)

	// Issues are actor-only, like the job itself.
	otherUser := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues", mmmodel.NewId(), nil)
	require.Equal(t, http.StatusNotFound, otherUser.Code)
}

func TestHandleListImports(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, otherID := mmmodel.NewId(), mmmodel.NewId()
	teamA, teamB := mmmodel.NewId(), mmmodel.NewId()

	for _, teamID := range []string{teamA, teamA, teamB} {
		rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}
	// Another user's job must not appear in the actor's listing.
	rec := h.uploadFixture(t, otherID, newTargetRequest(teamA), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, rec.Code)

	all := h.do(t, http.MethodGet, "/api/v1/imports", actorID, nil)
	require.Equal(t, http.StatusOK, all.Code)
	var allPage paginatedResponse[model.ImportJobView]
	require.NoError(t, json.Unmarshal(all.Body.Bytes(), &allPage))
	require.Len(t, allPage.Items, 3)

	scoped := h.do(t, http.MethodGet, "/api/v1/imports?team_id="+teamA, actorID, nil)
	require.Equal(t, http.StatusOK, scoped.Code)
	var scopedPage paginatedResponse[model.ImportJobView]
	require.NoError(t, json.Unmarshal(scoped.Body.Bytes(), &scopedPage))
	require.Len(t, scopedPage.Items, 2)
	for _, item := range scopedPage.Items {
		require.Equal(t, teamA, item.Target.TeamId)
	}
}

func TestHandleCreateImport_StagesPagesForWorker(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	const pageCount = 6
	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: pageCount})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// Every page is staged in PostgreSQL, so no later step depends on the uploading node's disk.
	staged, err := h.store.CountImportStagedPages(jobID)
	require.NoError(t, err)
	require.Equal(t, pageCount, staged)

	// Manifest users are persisted at upload so author resolution never depends on request memory.
	users, err := h.store.GetImportManifestUsers(jobID)
	require.NoError(t, err)
	require.NotEmpty(t, users)

	// Admission reserved this job's staged bytes on the shared capacity row.
	capacity, err := h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Greater(t, capacity.ReservedStagedBytes, int64(0))
	require.Greater(t, capacity.ReservedRetainedBytes, int64(0))

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, int64(pageCount), job.ProgressTotal)
	require.Len(t, job.BundleSha256, 64)
	// The confirmation is empty until the user confirms.
	require.Empty(t, job.Confirmation.PreflightRevision)
	require.Empty(t, job.Confirmation.OverwriteConflicts)
	// Retention gives the user a review window rather than expiring immediately.
	require.Greater(t, job.RetainUntil, job.CreateAt)
}

func TestHandleCancelImport_ReleasesAdmissionCapacity(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 3})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	before, err := h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Greater(t, before.ReservedStagedBytes, int64(0))
	require.Greater(t, before.ReservedRetainedBytes, int64(0))

	staged, err := h.store.CountImportStagedPages(jobID)
	require.NoError(t, err)
	require.Equal(t, 3, staged)

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code, cancel.Body.String())
	require.Equal(t, model.ImportStateCanceled, decodeJobView(t, cancel).State)

	// The staged reservation is given back, so an abandoned upload cannot hold admission budget.
	after, err := h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Equal(t, int64(0), after.ReservedStagedBytes)

	// The bodies are gone but the job and its issues survive for the report.
	staged, err = h.store.CountImportStagedPages(jobID)
	require.NoError(t, err)
	require.Zero(t, staged)
	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateCanceled, job.State)
	require.Equal(t, model.ImportIntentCanceled, job.TerminalIntent)
	require.Zero(t, job.StagedBytes)

	// Every staged page has a durable not-attempted outcome recorded before its identity was deleted:
	// without this the report of a canceled job could not say which pages the bundle contained.
	rows, err := h.db.Query(
		`SELECT Ordinal, ExternalId, Title, ActualAction, Outcome FROM DOCS_ImportResult
		   WHERE JobId = $1 AND Stage = 'execution' ORDER BY Ordinal`, jobID)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	seen := 0
	for rows.Next() {
		var ordinal int
		var externalID, title, actualAction, outcome string
		require.NoError(t, rows.Scan(&ordinal, &externalID, &title, &actualAction, &outcome))
		require.Equal(t, seen, ordinal)
		require.NotEmpty(t, externalID)
		require.NotEmpty(t, title)
		require.Equal(t, string(model.ImportActionNotAttempted), actualAction)
		require.Equal(t, string(model.ImportOutcomeNotAttemptedCancel), outcome)
		seen++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 3, seen)
	require.Equal(t, 3, job.FinalSummary.Actions.NotAttempted)
	require.Equal(t, 3, job.FinalSummary.Outcomes[string(model.ImportOutcomeNotAttemptedCancel)])

	// The retained reservation is trued up to what the job actually holds. Leaving the full
	// execution-sized reservation in place would keep the per-user budget occupied for the whole
	// ninety-day retention window even though the job can never write another row.
	require.Equal(t, job.RetainedBytes, job.RetainedReservedBytes)
	require.Less(t, job.RetainedReservedBytes, before.ReservedRetainedBytes/10)
	require.Equal(t, job.RetainedBytes, after.ReservedRetainedBytes)

	// Cancelling twice is a conflict, not a second release.
	again := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusConflict, again.Code)

	// Another user cannot cancel someone else's job, and cannot learn it exists.
	other := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", mmmodel.NewId(), nil)
	require.Equal(t, http.StatusNotFound, other.Code)
}

func TestHandleCreateImport_AdmissionLimitsConcurrentJobs(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	// The per-actor nonterminal job cap is 3; each upload uses a distinct team so the per-target cap
	// is not what trips first.
	var jobIDs []string
	for range 3 {
		rec := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		jobIDs = append(jobIDs, decodeJobView(t, rec).Id)
	}
	blocked := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusTooManyRequests, blocked.Code, blocked.Body.String())
	// A capacity refusal tells the client when to come back rather than looking like a client error.
	require.NotEmpty(t, blocked.Header().Get("Retry-After"))

	// Cancelling one frees a slot, so the user is never permanently locked out.
	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobIDs[0]+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code)
	retry := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, retry.Code, retry.Body.String())
}

// TestHandleCancelImport_RepeatedCyclesDoNotExhaustRetainedBudget pins the failure mode that made
// cancellation a slower version of the leak it was meant to fix: canceled jobs stay terminal for ninety
// days and the per-actor retained sum has no state filter, so a reservation left at its admitted size
// would lock the user out after roughly twenty upload/cancel cycles despite holding no live job at all.
// Thirty cycles is comfortably past that threshold.
func TestHandleCancelImport_RepeatedCyclesDoNotExhaustRetainedBudget(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()

	for cycle := range 30 {
		rec := h.uploadFixture(t, actorID, newTargetRequest(mmmodel.NewId()), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusCreated, rec.Code, "cycle %d: %s", cycle, rec.Body.String())
		jobID := decodeJobView(t, rec).Id
		cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
		require.Equal(t, http.StatusAccepted, cancel.Code, "cycle %d: %s", cycle, cancel.Body.String())
	}

	// Thirty canceled jobs together hold less than a single one used to reserve, and staged capacity is
	// back to zero.
	capacity, err := h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Zero(t, capacity.ReservedStagedBytes)
	require.Less(t, capacity.ReservedRetainedBytes, int64(1024*1024))
}

func TestImportMaintenance_ExpiresAndReleases(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// Nothing is expired while the review window is open.
	counts, err := h.plugin.service.RunImportMaintenance()
	require.NoError(t, err)
	require.Zero(t, counts.ExpiredJobs)

	// Backdate the deadline to simulate an abandoned upload. This job is a new-Space target, so it sits
	// in queued_preflight rather than waiting on the user — expiry has to reclaim that state too, or a
	// job with no worker to advance it would hold its admission budget forever.
	_, err = h.db.Exec(`UPDATE DOCS_ImportJob SET RetainUntil = 1 WHERE Id = $1`, jobID)
	require.NoError(t, err)

	counts, err = h.plugin.service.RunImportMaintenance()
	require.NoError(t, err)
	require.Equal(t, 1, counts.ExpiredJobs)
	require.Greater(t, counts.ReleasedStagedBytes, int64(0))
	// Expiry inherits cancellation's retained true-up, so an abandoned upload does not keep an
	// execution-sized reservation alive for the terminal retention window.
	require.Greater(t, counts.ReleasedRetainedBytes, int64(0))

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateCanceled, job.State)
	require.Zero(t, job.StagedBytes)
	// Expiry records the same durable outcomes an explicit cancel does.
	require.Equal(t, 2, job.FinalSummary.Actions.NotAttempted)

	capacity, err := h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Zero(t, capacity.ReservedStagedBytes)

	// Past the 90-day terminal retention the job itself is deleted and its retained reservation freed.
	_, err = h.db.Exec(`UPDATE DOCS_ImportJob SET RetainUntil = 1 WHERE Id = $1`, jobID)
	require.NoError(t, err)
	counts, err = h.plugin.service.RunImportMaintenance()
	require.NoError(t, err)
	require.Equal(t, 1, counts.DeletedJobs)
	_, err = h.store.GetImportJob(jobID)
	require.Error(t, err)

	capacity, err = h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Zero(t, capacity.ReservedRetainedBytes)
}

func TestHandleGetImport_EntitlementLoss(t *testing.T) {
	// The actor owns the job but has lost access to the existing target Space: the job stays visible
	// as a minimal record, while its issues become invisible.
	api := newEnabledMockAPI()
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	api.On("GetTeam", mock.Anything).Return(&mmmodel.Team{Id: mmmodel.NewId(), Name: "myteam"}, nil).Maybe()
	api.On("HasPermissionToTeam", mock.Anything, mock.Anything, mock.Anything).Return(true).Maybe()
	api.On("GetUser", mock.Anything).Return(&mmmodel.User{Id: mmmodel.NewId()}, nil).Maybe()
	// Channel membership is granted for the upload and revoked afterwards.
	membership := api.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 2, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// Revoke Space access.
	membership.Unset()
	api.On("GetChannelMember", mock.Anything, mock.Anything).
		Return(nil, mmmodel.NewAppError("GetChannelMember", "not_found", nil, "", http.StatusNotFound)).Maybe()

	got := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	require.Equal(t, http.StatusOK, got.Code)
	view := decodeJobView(t, got)
	require.Equal(t, jobID, view.Id)
	// Nothing target- or source-identifying survives the downgrade.
	require.Empty(t, view.Target.SpaceId)
	require.Empty(t, view.Target.TeamId)
	require.Zero(t, view.Bundle.Counts.Pages)
	require.Empty(t, view.Bundle.Source.SpaceKey)

	// Sensitive per-page detail is hidden outright.
	issues := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID+"/issues", actorID, nil)
	require.Equal(t, http.StatusNotFound, issues.Code)

	// And the job drops out of the listing rather than appearing as a stub.
	list := h.do(t, http.MethodGet, "/api/v1/imports", actorID, nil)
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), `"items":[]`)
}

// TestHandleCancelImport_RedactsAfterEntitlementLoss pins that cancelling is not a way around the read
// path's redaction. Owning a job authorizes cancelling it, but not reading its target: without this,
// an actor who had lost Space access could cancel and receive the target and selected-source projection
// that GET deliberately withholds.
func TestHandleCancelImport_RedactsAfterEntitlementLoss(t *testing.T) {
	api := newEnabledMockAPI()
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	api.On("GetTeam", mock.Anything).Return(&mmmodel.Team{Id: mmmodel.NewId(), Name: "myteam"}, nil).Maybe()
	api.On("HasPermissionToTeam", mock.Anything, mock.Anything, mock.Anything).Return(true).Maybe()
	api.On("GetUser", mock.Anything).Return(&mmmodel.User{Id: mmmodel.NewId()}, nil).Maybe()
	membership := api.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, api)
	actorID := mmmodel.NewId()
	space := seedSpace(t, h.store, mmmodel.NewId())

	rec := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// Revoke Space access, then cancel.
	membership.Unset()
	api.On("GetChannelMember", mock.Anything, mock.Anything).
		Return(nil, mmmodel.NewAppError("GetChannelMember", "not_found", nil, "", http.StatusNotFound)).Maybe()

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code, cancel.Body.String())
	view := decodeJobView(t, cancel)

	// The cancel still happened, and its outcome is still reported.
	require.Equal(t, model.ImportStateCanceled, view.State)
	require.NotNil(t, view.Error)
	require.Equal(t, "canceled_by_user", view.Error.Code)

	// But nothing target- or source-identifying comes back.
	require.Empty(t, view.Target.SpaceId)
	require.Empty(t, view.Target.TeamId)
	require.Nil(t, view.SelectedSource)
	require.Zero(t, view.Bundle.Counts.Pages)
	require.Empty(t, view.Bundle.Source.SpaceKey)

	// The cancel is durable regardless of what the response disclosed.
	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, model.ImportStateCanceled, job.State)
}

// TestHandleCancelImport_PersistsErrorCode covers the job-level error code. It is what
// ImportJobView.Error carries, including in the redacted projection, so leaving it unwritten made every
// canceled job read back as though it had stopped for no stated reason.
func TestHandleCancelImport_PersistsErrorCode(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code)

	// Present on the immediate response...
	view := decodeJobView(t, cancel)
	require.NotNil(t, view.Error)
	require.Equal(t, "canceled_by_user", view.Error.Code)
	// ...and on a subsequent read, which is what makes it durable rather than cosmetic.
	got := h.do(t, http.MethodGet, "/api/v1/imports/"+jobID, actorID, nil)
	require.Equal(t, http.StatusOK, got.Code)
	require.NotNil(t, decodeJobView(t, got).Error)
	require.Equal(t, "canceled_by_user", decodeJobView(t, got).Error.Code)

	// The returned job mirrors every column the cancel wrote, rather than leaving some stale.
	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, "canceled_by_user", job.ErrorCode)
	require.Greater(t, job.CancelRequestedAt, int64(0))
	require.Greater(t, job.RetainUntil, job.CreateAt)
	require.Equal(t, job.FinishedAt, job.CancelRequestedAt)
	require.NotZero(t, view.FinishedAt)
	require.Equal(t, job.FinishedAt, view.FinishedAt)
	require.Equal(t, job.UpdateAt, view.UpdateAt)
}

// TestHandleListImports_FiltersBeforePaginating pins page density and has_more accuracy. Filtering
// entitlement after the page was cut produced sparse pages and a has_more computed from rows the caller
// never saw: with unreachable jobs interleaved, a request for 2 could return 0 alongside has_more=false
// while entitled jobs sat just past the offset.
func TestHandleListImports_FiltersBeforePaginating(t *testing.T) {
	api := newEnabledMockAPI()
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	api.On("GetTeam", mock.Anything).Return(&mmmodel.Team{Id: mmmodel.NewId(), Name: "myteam"}, nil).Maybe()
	api.On("HasPermissionToTeam", mock.Anything, mock.Anything, mock.Anything).Return(true).Maybe()
	api.On("GetUser", mock.Anything).Return(&mmmodel.User{Id: mmmodel.NewId()}, nil).Maybe()
	// Space membership is granted for the uploads and revoked afterwards, which makes the
	// existing-Space jobs unreachable while the new-Space jobs stay visible.
	membership := api.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	// Interleave reachable (new-Space) and soon-unreachable (existing-Space) jobs, cancelling each so the
	// per-actor concurrency cap is not what limits the fixture.
	var reachable []string
	for range 3 {
		space := seedSpace(t, h.store, mmmodel.NewId())
		hidden := h.uploadFixture(t, actorID, existingTargetRequest(space.Id), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusCreated, hidden.Code, hidden.Body.String())
		h.do(t, http.MethodPost, "/api/v1/imports/"+decodeJobView(t, hidden).Id+"/cancel", actorID, nil)

		visible := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 1})
		require.Equal(t, http.StatusCreated, visible.Code, visible.Body.String())
		id := decodeJobView(t, visible).Id
		reachable = append(reachable, id)
		h.do(t, http.MethodPost, "/api/v1/imports/"+id+"/cancel", actorID, nil)
	}

	membership.Unset()
	api.On("GetChannelMember", mock.Anything, mock.Anything).
		Return(nil, mmmodel.NewAppError("GetChannelMember", "not_found", nil, "", http.StatusNotFound)).Maybe()

	// Page 0 of 2 must be full, and must report that more remain.
	first := h.do(t, http.MethodGet, "/api/v1/imports?per_page=2&page=0", actorID, nil)
	require.Equal(t, http.StatusOK, first.Code)
	var firstPage paginatedResponse[model.ImportJobView]
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Items, 2, "a page must be filled with entitled jobs, not left sparse")
	require.True(t, firstPage.HasMore)

	// Page 1 holds the remaining entitled job and correctly reports the end of the list.
	second := h.do(t, http.MethodGet, "/api/v1/imports?per_page=2&page=1", actorID, nil)
	require.Equal(t, http.StatusOK, second.Code)
	var secondPage paginatedResponse[model.ImportJobView]
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondPage))
	require.Len(t, secondPage.Items, 1)
	require.False(t, secondPage.HasMore)

	// Every entitled job appears exactly once across the two pages, and no unreachable one does.
	seen := map[string]int{}
	for _, item := range append(firstPage.Items, secondPage.Items...) {
		seen[item.Id]++
	}
	require.Len(t, seen, 3)
	for _, id := range reachable {
		require.Equal(t, 1, seen[id], "job %s should appear exactly once", id)
	}
}

// TestImportMaintenance_KeepsJobsAwaitingCompensation pins the retention exemption. A
// pending_compensation attempt is the record of a Mattermost channel this import created and must still
// clean up, and DOCS_ImportChannelAttempt cascades on job delete — so deleting the job at retention
// would destroy the only pointer to an orphaned channel. The schema even carries a partial index over
// exactly these rows, i.e. a work-list the cascade would silently empty.
func TestImportMaintenance_KeepsJobsAwaitingCompensation(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 1})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code)

	// A channel was provisioned and still needs compensating.
	_, err := h.db.Exec(
		`INSERT INTO DOCS_ImportChannelAttempt (JobId, AttemptId, ChannelName, ChannelId, State, CreateAt, UpdateAt)
		 VALUES ($1, $2, 'docs-import', $3, 'pending_compensation', 1, 1)`,
		jobID, mmmodel.NewId(), mmmodel.NewId())
	require.NoError(t, err)

	// Push the job past its terminal retention.
	_, err = h.db.Exec(`UPDATE DOCS_ImportJob SET RetainUntil = 1 WHERE Id = $1`, jobID)
	require.NoError(t, err)

	counts, err := h.plugin.service.RunImportMaintenance()
	require.NoError(t, err)
	require.Zero(t, counts.DeletedJobs, "a job whose channel still needs compensation must not be deleted")
	require.Equal(t, 1, counts.KeptForCompensationJobs)

	// The job and its attempt both survive, so compensation still has something to act on.
	_, err = h.store.GetImportJob(jobID)
	require.NoError(t, err)
	var attempts int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportChannelAttempt WHERE JobId = $1 AND State = 'pending_compensation'`,
		jobID).Scan(&attempts))
	require.Equal(t, 1, attempts)

	// Once compensation resolves the attempt, the next sweep deletes the job as usual.
	_, err = h.db.Exec(`UPDATE DOCS_ImportChannelAttempt SET State = 'compensated' WHERE JobId = $1`, jobID)
	require.NoError(t, err)
	counts, err = h.plugin.service.RunImportMaintenance()
	require.NoError(t, err)
	require.Equal(t, 1, counts.DeletedJobs)
	require.Zero(t, counts.KeptForCompensationJobs)
	_, err = h.store.GetImportJob(jobID)
	require.Error(t, err)

	capacity, err := h.store.GetImportCapacity()
	require.NoError(t, err)
	require.Zero(t, capacity.ReservedRetainedBytes)
	require.Zero(t, capacity.ReservedStagedBytes)
}

// TestHandleCreateImport_SeparatesRetainedPools covers the two-pool accounting. A single "unspent
// reservation" figure let issue writers consume the capacity held for mandatory outcomes, which is
// exactly the shortfall the reservation exists to prevent, so the issue spend is tracked on its own.
func TestHandleCreateImport_SeparatesRetainedPools(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID),
		importfixture.Options{Pages: 4, WithFindings: true})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	job, err := h.store.GetImportJob(decodeJobView(t, rec).Id)
	require.NoError(t, err)

	// Inspection emitted issues, so the discretionary pool is non-empty and is a strict part of the total.
	require.Greater(t, job.RetainedIssueBytes, int64(0))
	require.Greater(t, job.RetainedBytes, job.RetainedIssueBytes)

	// The issue allowance is measured against itself, never against the (much larger) unspent reservation.
	require.Equal(t, int64(model.ImportRetainedIssueBudgetBytes)-job.RetainedIssueBytes, job.IssueBudgetRemaining())
	require.LessOrEqual(t, job.IssueBudgetRemaining(), int64(model.ImportRetainedIssueBudgetBytes))
	require.Less(t, job.IssueBudgetRemaining(), job.RetainedReservedBytes-job.RetainedBytes,
		"the issue allowance must be smaller than the whole unspent reservation, or the pools are not separated")

	// The reservation still covers the mandatory outcomes for every staged page plus a full mapping set.
	mandatory := int64(4+model.ImportMaxMappingsPerSource) * 2 * model.ImportRetainedResultRowMaxBytes
	require.GreaterOrEqual(t, job.RetainedReservedBytes, mandatory+int64(model.ImportRetainedIssueBudgetBytes))

	// Cancelling charges the final summary before trueing up, so the trued-up figure is not short of the
	// durable JSONB the job is actually holding.
	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+job.Id+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code)
	after, err := h.store.GetImportJob(job.Id)
	require.NoError(t, err)
	require.Equal(t, after.RetainedBytes, after.RetainedReservedBytes)
	summaryBytes, err := after.FinalSummary.Value()
	require.NoError(t, err)
	summaryLen := int64(len(summaryBytes.(string)))
	require.Greater(t, summaryLen, int64(0))
	require.GreaterOrEqual(t, after.RetainedBytes, job.RetainedBytes+summaryLen,
		"the final summary must be charged, not written for free")
}

// TestHandleCancelImport_RecordsOutcomesForPreflightOnlyEntities covers the entities that have a plan but
// no staged page — chiefly the stale mappings preflight classifies, whose ordinals start above the page
// range. Deriving outcomes only from staged pages would let those reach a terminal state with a recorded
// plan and no recorded result.
func TestHandleCancelImport_RecordsOutcomesForPreflightOnlyEntities(t *testing.T) {
	api := newImportMockAPI(importMockOptions{teamMember: true, canCreateChannel: true})
	h := openTestPlugin(t, api)
	actorID, teamID := mmmodel.NewId(), mmmodel.NewId()

	rec := h.uploadFixture(t, actorID, newTargetRequest(teamID), importfixture.Options{Pages: 2})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	jobID := decodeJobView(t, rec).Id

	// Stand in for what preflight will write: a stale entry with no staged page behind it.
	staleOrdinal := model.ImportStaleOrdinalBase
	_, err := h.db.Exec(
		`INSERT INTO DOCS_ImportResult
		   (JobId, Stage, Ordinal, EntityType, ExternalId, Title, PlannedAction, Outcome, CreateAt, UpdateAt)
		 VALUES ($1, 'preflight', $2, 'page', '9001', 'Gone from the bundle', 'stale', 'stale', 1, 1)`,
		jobID, staleOrdinal)
	require.NoError(t, err)

	cancel := h.do(t, http.MethodPost, "/api/v1/imports/"+jobID+"/cancel", actorID, nil)
	require.Equal(t, http.StatusAccepted, cancel.Code, cancel.Body.String())

	// Two pages plus the stale entry all carry an execution outcome.
	var executionRows int
	require.NoError(t, h.db.QueryRow(
		`SELECT COUNT(*) FROM DOCS_ImportResult WHERE JobId = $1 AND Stage = 'execution'`, jobID).Scan(&executionRows))
	require.Equal(t, 3, executionRows)

	var staleOutcome, staleExternalID string
	require.NoError(t, h.db.QueryRow(
		`SELECT Outcome, ExternalId FROM DOCS_ImportResult
		   WHERE JobId = $1 AND Stage = 'execution' AND Ordinal = $2`, jobID, staleOrdinal).
		Scan(&staleOutcome, &staleExternalID))
	require.Equal(t, string(model.ImportOutcomeNotAttemptedCancel), staleOutcome)
	require.Equal(t, "9001", staleExternalID, "the stale entry's identity must carry across from preflight")

	job, err := h.store.GetImportJob(jobID)
	require.NoError(t, err)
	require.Equal(t, 3, job.FinalSummary.Actions.NotAttempted)
	require.Equal(t, 1, job.FinalSummary.Actions.Stale)
}
