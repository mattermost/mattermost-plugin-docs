// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// docWith returns a minimal TipTap document whose paragraph contains text.
func docWith(text string) string {
	return fmt.Sprintf(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}`, text)
}

// publishNewPage creates a new-page draft, autosaves the given body, and publishes it, returning
// the live page. It asserts the reserved draft id is preserved through publish.
func publishNewPage(t *testing.T, h *testHarness, space *model.Space, userID, title, bodyText string) *model.Page {
	t.Helper()
	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, title, "")
	require.Nil(t, appErr)
	reservedID := draft.PageId

	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: reservedID, Title: title, Body: docWith(bodyText)}, nil, nil, nil, "")
	require.Nil(t, appErr)

	page, wasCreated, appErr := h.svc.PublishPageDraft(space, userID, reservedID, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.True(t, wasCreated, "publishing a brand-new page must report wasCreated=true")
	require.Equal(t, reservedID, page.Id, "publish must preserve the reserved draft id")
	require.Contains(t, page.Body, bodyText)
	return page
}

func TestUpdatePageDraftPreservesBodyOnTitleOnlyAutosave(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)
	pageID := draft.PageId

	// Autosave real content.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc", Body: docWith("keep me")}, nil, nil, nil, "")
	require.Nil(t, appErr)

	// A heartbeat that sends only the title (empty body) must not wipe the stored draft body.
	saved, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc"}, nil, nil, nil, "")
	require.Nil(t, appErr)
	require.Contains(t, saved.Body, "keep me", "a title-only autosave must not clear the draft body")
}

func TestPublishEmptyDraftBodyDoesNotWipePage(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Important", "ORIGINAL")

	// Start an edit session whose first (and only) autosave carries the title but no body, exactly
	// the heartbeat case that previously wiped the page on publish.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Important",
		BaseEditAt: page.EditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	republished, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Contains(t, republished.Body, "ORIGINAL", "publishing a title-only edit must preserve the page body")
}

// TestPublishNoOpDraftDiscardsAndReturnsExistingPage verifies that publishing a draft that carries
// no content change — only the optimistic-lock baseline, no Title, no Body — is treated as a
// discard rather than a no-op page write: the draft is deleted, the existing page comes back
// unchanged with its EditAt intact, no page_updated event fires, and wasCreated is false.
func TestPublishNoOpDraftDiscardsAndReturnsExistingPage(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "original")

	// Start an edit session whose only autosave carries the baseline — no Title, no Body.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id,
		BaseEditAt: page.EditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	result, wasCreated, appErr := h.svc.PublishPageDraft(space, userID, page.Id, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.False(t, wasCreated, "a no-content publish must not report a creation")
	require.Equal(t, page.Title, result.Title, "the page's title must be unchanged")
	require.Contains(t, result.Body, "original", "the page's body must be unchanged")
	require.Equal(t, page.EditAt, result.EditAt, "a no-content publish must not bump EditAt")
	mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "page_updated", mock.Anything, mock.Anything)

	// The draft was consumed: it was converted into a discard rather than left in place.
	_, appErr = h.svc.GetPageDraft(userID, space.Id, page.Id)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
}

func TestPublishRejectsMissingBaselineOnEdit(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "v1")

	// UpsertDraft guards the first edit-draft save: attempting to create a draft for an existing
	// page without a base_edit_at baseline is rejected at the store layer, so the client must send
	// the baseline on the very first autosave (the response tells it to reload and set it).
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("v2")}, nil, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	// With a proper baseline the draft is created; force=true publishes regardless of baseline.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("v2"),
		BaseEditAt: page.EditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	forced, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, true, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Contains(t, forced.Body, "v2")
}

// TestPublishRejectsNoBaselineOnExistingPageEdit covers the app-level baseline_required guard in
// PublishPageDraft: a stored edit-draft with BaseEditAt still 0 (no baseline was ever captured) must
// be rejected with 400 unless force=true. The store's UpsertDraft guard normally blocks a
// baseline-less draft from ever being created against a live page (see
// TestPublishRejectsMissingBaselineOnEdit), so this simulates the race where the draft's first
// autosave lands as a new-page draft and the underlying page becomes live afterward without the
// draft ever acquiring a baseline — reached here by inserting the draft row directly.
func TestPublishRejectsNoBaselineOnExistingPageEdit(t *testing.T) {
	h := openTestService(t)
	channelID := mmmodel.NewId()
	space := mustCreateSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	page := mustCreatePage(t, h.store, space.Id, channelID, userID, "")

	now := mmmodel.GetMillis()
	_, err := h.db.Exec(
		`INSERT INTO docs_draft (userid,spaceid,pageid,parentid,title,body,fileids,props,createat,updateat,lastactiveat,baseeditat)
		 VALUES ($1,$2,$3,'',$4,$5,'[]','{}', $6,$6,$6,0)`,
		userID, space.Id, page.Id, "Edited", docWith("v2"), now,
	)
	require.NoError(t, err)

	// Without a baseline, a non-force publish of an edit must be rejected.
	_, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, false, app.ReadViaMember)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "app.page_draft.publish.baseline_required.app_error", appErr.Id)

	// force=true bypasses the missing-baseline guard and publishes the edit.
	forced, wasCreated, appErr := h.svc.PublishPageDraft(space, userID, page.Id, true, app.ReadViaMember)
	require.Nil(t, appErr)
	require.False(t, wasCreated)
	require.Equal(t, "Edited", forced.Title)
	require.Contains(t, forced.Body, "v2")
}

func TestPublishStaleBaselineConflicts(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "v1")
	staleEditAt := page.EditAt

	// Start an edit session with a draft baselined at the current EditAt. The draft persists (it is
	// not consumed by any publish), so the stale-baseline conflict surfaces at publish time.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("v3"),
		BaseEditAt: staleEditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	// A concurrent direct edit advances the page's EditAt out from under that baseline, without
	// touching the draft.
	concurrent := docWith("concurrent")
	_, appErr = h.svc.UpdatePage(page.Id, space.Id, &model.PagePatch{Body: &concurrent}, new(staleEditAt), false, userID)
	require.Nil(t, appErr)

	// Publishing the draft against the now-stale baseline must 409, and return the current server
	// page (the concurrent edit's content + advanced baseline) so the caller can diff without a
	// follow-up read.
	current, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, false, app.ReadViaMember)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
	require.NotNil(t, current, "edit conflict must return the current server page")
	require.Contains(t, current.Body, "concurrent", "current page must reflect the concurrent edit")
	require.Greater(t, current.EditAt, staleEditAt, "current page must carry the advanced baseline")
}

// TestPublishAfterPageDeleteReturns404 verifies that deleting a page cascade-deletes its drafts,
// so a later publish finds no draft (404) rather than writing to a tombstone. The page-deleted 409
// path in PublishPageDraft guards only the concurrent-delete race, which this flow cannot produce.
func TestPublishAfterPageDeleteReturns404(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doomed", "x")
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doomed", Body: docWith("y"),
		BaseEditAt: page.EditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)
	requireStoreDeletePage(t, h.store, page.Id, space.Id, userID)

	_, _, appErr = h.svc.PublishPageDraft(space, userID, page.Id, false, app.ReadViaMember)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
}

func TestDeletePageDraftRejectsWrongSpace(t *testing.T) {
	h := openTestService(t)
	spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
	spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, spaceA.Id, "In A", "")
	require.Nil(t, appErr)

	// Deleting the draft while naming the wrong space must 404, and must not delete it.
	delErr := h.svc.DeletePageDraft(userID, spaceB.Id, draft.PageId, "")
	require.NotNil(t, delErr)
	require.Equal(t, http.StatusNotFound, delErr.StatusCode)

	got, appErr := h.svc.GetPageDraft(userID, spaceA.Id, draft.PageId)
	require.Nil(t, appErr, "draft must survive a delete attempt through the wrong space")
	require.Equal(t, draft.PageId, got.PageId)
}

// TestPublishPageDraftConcurrentPublishesConverge races two publishes of the same new-page draft
// (a double-clicked publish). The winner is not fixed, so the assertions are outcome invariants:
// exactly one publish creates the page; the other either adopts the winner (success without
// wasCreated — see adoptPublishRaceWinner) or reports a benign already-published outcome (404
// draft gone / 409 conflict). Either way the page ends up live and the draft consumed.
func TestPublishPageDraftConcurrentPublishesConverge(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: draft.PageId, Title: "Doc", Body: docWith("race")}, nil, nil, nil, "")
	require.Nil(t, appErr)

	type result struct {
		page    *model.Page
		created bool
		appErr  *mmmodel.AppError
	}
	results := make([]result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			<-start
			p, created, pubErr := h.svc.PublishPageDraft(space, userID, draft.PageId, false, app.ReadViaMember)
			results[i] = result{page: p, created: created, appErr: pubErr}
		})
	}
	close(start)
	wg.Wait()

	var created, benign int
	for _, r := range results {
		switch {
		case r.appErr == nil && r.created:
			created++
			require.Equal(t, draft.PageId, r.page.Id)
		case r.appErr == nil:
			// Adopted the winner: same live page, without wasCreated.
			require.Equal(t, draft.PageId, r.page.Id)
		default:
			require.Contains(t, []int{http.StatusNotFound, http.StatusConflict}, r.appErr.StatusCode,
				"the losing publish may only fail benignly, got %v", r.appErr)
			benign++
		}
	}
	require.Equal(t, 1, created, "exactly one concurrent publish must create the page, got %+v", results)
	require.LessOrEqual(t, benign, 1)

	// Converged state: the draft is consumed and the page is live in the space.
	_, appErr = h.svc.GetPageDraft(userID, space.Id, draft.PageId)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode, "the draft must be consumed by the publish")
	snapshot, appErr := h.svc.GetPageActiveEditors(draft.PageId, space.Id)
	require.Nil(t, appErr, "the published page must resolve in its space")
	require.Empty(t, snapshot.ActiveEditors)
}

// TestGetPageActiveEditorsUnknownPageReturns404 pins the plain not-found case: a page id with no
// row at all (as opposed to a page living in another space, covered below).
func TestGetPageActiveEditorsUnknownPageReturns404(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.GetPageActiveEditors(mmmodel.NewId(), space.Id)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
}

func TestGetPageActiveEditorsRejectsWrongSpace(t *testing.T) {
	h := openTestService(t)
	spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
	spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, spaceA, userID, "Doc", "x")

	// The page lives in space A; querying its editors through space B must not resolve it.
	_, appErr := h.svc.GetPageActiveEditors(page.Id, spaceB.Id)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)

	// Through its own space it resolves (empty set — no active drafts).
	snapshot, appErr := h.svc.GetPageActiveEditors(page.Id, spaceA.Id)
	require.Nil(t, appErr)
	require.Empty(t, snapshot.ActiveEditors)
}

func TestActiveEditorsSurfacesHeartbeat(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "x")

	// An autosave is the heartbeat; the editor must then appear as active.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("editing"),
		BaseEditAt: page.EditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	snapshot, appErr := h.svc.GetPageActiveEditors(page.Id, space.Id)
	require.Nil(t, appErr)
	require.Contains(t, snapshot.ActiveEditors, userID)
	require.Positive(t, snapshot.SnapshotAt)
	require.Equal(t, app.ActiveEditorTimeoutMs, snapshot.ActiveTimeoutMs)
}

func TestPublishSetsLastModifiedBy(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "x")
	require.Equal(t, userID, page.LastModifiedBy, "a page published via draft must record its author as last modifier")
}

func TestPublishForceOverridesStaleBaseline(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "v1")
	staleEditAt := page.EditAt

	// Start an edit session with a draft baselined at the current EditAt; the draft persists.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("v3"),
		BaseEditAt: staleEditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	// A concurrent direct edit advances the page's EditAt, making the draft's baseline stale.
	concurrent := docWith("concurrent")
	concurrentPage, appErr := h.svc.UpdatePage(page.Id, space.Id, &model.PagePatch{Body: &concurrent}, new(staleEditAt), false, userID)
	require.Nil(t, appErr)

	// force=true must override the stale-baseline CAS and win with the draft's content.
	forced, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, true, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Contains(t, forced.Body, "v3", "force must override a stale baseline")
	require.Greater(t, forced.EditAt, concurrentPage.EditAt, "a force-publish must still advance the page's EditAt")
}

func TestPublishForceDoesNotRevertUntouchedField(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Original title", "original body")
	baseEditAt := page.EditAt

	// A title-only edit: the draft carries a new title but no body, baselined at the current EditAt.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "New title",
		BaseEditAt: baseEditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	// A concurrent edit changes the BODY — a field the draft never touched — and advances EditAt.
	concurrent := docWith("concurrent body")
	_, appErr = h.svc.UpdatePage(page.Id, space.Id, &model.PagePatch{Body: &concurrent}, new(baseEditAt), false, userID)
	require.Nil(t, appErr)

	// Force-publish the title-only draft: force overrides the stale baseline, but only the title may
	// be applied. The concurrent body edit must survive rather than be reverted to the pre-lock value.
	forced, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, true, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Equal(t, "New title", forced.Title, "the draft's title must be applied")
	require.Contains(t, forced.Body, "concurrent body", "a field the draft never set must not be reverted")
}

func TestUpdatePageDraftRejectsStaleBaselineAfterPublish(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Doc", "v1")
	baseEditAt := page.EditAt

	// Edit and publish: the draft is consumed and the page's EditAt advances.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("v2"),
		BaseEditAt: baseEditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)
	_, _, appErr = h.svc.PublishPageDraft(space, userID, page.Id, false, app.ReadViaMember)
	require.Nil(t, appErr)

	// A late autosave carrying the pre-publish baseline must be rejected, not resurrect a phantom
	// draft on the now-published page.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Doc", Body: docWith("late"),
		BaseEditAt: baseEditAt,
	}, nil, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	// No phantom draft was created.
	_, appErr = h.svc.GetPageDraft(userID, space.Id, page.Id)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
}

func TestUpdatePageDraftPreservesPropsOnOmit(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)
	pageID := draft.PageId

	// First autosave writes a props map (non-nil pointer → whole-value replace).
	saved, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc", Body: docWith("v1"),
	}, nil, nil, &mmmodel.StringInterface{"custom": "v"}, "")
	require.Nil(t, appErr)
	require.Equal(t, "v", saved.Props["custom"])

	// A later autosave that omits props (nil pointer) must preserve the stored map.
	saved, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc", Body: docWith("v2"),
	}, nil, nil, nil, "")
	require.Nil(t, appErr)
	require.Equal(t, "v", saved.Props["custom"], "omitted props must preserve the stored map")

	// An explicit empty map (non-nil pointer) must clear the stored props.
	saved, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc", Body: docWith("v3"),
	}, nil, nil, &mmmodel.StringInterface{}, "")
	require.Nil(t, appErr)
	require.Empty(t, saved.Props, "an explicit empty props map must clear the stored props")
}

// A draft autosave whose (valid TipTap) body exceeds PageBodyMaxBytes must be rejected with 400 —
// the body is well-formed and small in node count, so it clears content normalization and is caught
// by Draft.IsValid's size guard in the store, surfaced through the app layer.
func TestUpdatePageDraftRejectsOversizedBody(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)

	// One paragraph, one text node whose text alone exceeds PageBodyMaxBytes — clears the node/
	// paragraph limits but overflows the serialized-body size cap.
	oversized := docWith(strings.Repeat("x", model.PageBodyMaxBytes))
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: draft.PageId, Title: "Doc", Body: oversized,
	}, nil, nil, nil, "")
	require.NotNil(t, appErr, "an oversized draft body must be rejected")
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

// Props follow PagePatch.Props write intent on publish: a new page adopts the draft's props, an
// edit whose draft carries a non-empty props map replaces the live page's props, and an edit that
// carries no props preserves them.
func TestPublishCarriesDraftProps(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// New-page publish adopts the draft's props.
	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)
	pageID := draft.PageId
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc", Body: docWith("v1"),
	}, nil, nil, &mmmodel.StringInterface{"color": "blue"}, "")
	require.Nil(t, appErr)

	page, wasCreated, appErr := h.svc.PublishPageDraft(space, userID, pageID, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.True(t, wasCreated)
	require.Equal(t, "blue", page.Props["color"], "a new page must adopt the draft's props on publish")

	// An edit whose draft carries a non-empty props map replaces the page's props.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Doc", BaseEditAt: page.EditAt,
	}, nil, nil, &mmmodel.StringInterface{"color": "red"}, "")
	require.Nil(t, appErr)
	edited, _, appErr := h.svc.PublishPageDraft(space, userID, pageID, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Equal(t, "red", edited.Props["color"], "an edit with non-empty draft props must replace the page's props")

	// An edit that carries content but no props preserves the live page's props.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Body: docWith("v2"), BaseEditAt: edited.EditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)
	preserved, _, appErr := h.svc.PublishPageDraft(space, userID, pageID, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Equal(t, "red", preserved.Props["color"], "an edit carrying no props must preserve the page's props")
	require.Contains(t, preserved.Body, "v2")
}

func TestPublishRejectsForeignSpacePage(t *testing.T) {
	h := openTestService(t)
	spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
	spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())
	userA, userB := mmmodel.NewId(), mmmodel.NewId()

	// userA reserves a page id in space A.
	draftA, appErr := h.svc.CreateSpaceDraft(userA, spaceA.Id, "A doc", "")
	require.Nil(t, appErr)
	pageID := draftA.PageId

	// userB cannot reserve a draft in space B against userA's not-yet-live page id —
	// the cross-space reservation is now correctly rejected at the app layer.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userB, SpaceId: spaceB.Id, PageId: pageID, Title: "B doc", Body: docWith("b content")}, nil, nil, nil, "")
	require.NotNil(t, appErr, "cross-space draft reservation must be rejected")
	require.Equal(t, http.StatusNotFound, appErr.StatusCode, "cross-space draft reservation returns 404")

	// userA autosaves content and publishes, so the page becomes live in space A.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userA, SpaceId: spaceA.Id, PageId: pageID, Title: "A doc", Body: docWith("a content")}, nil, nil, nil, "")
	require.Nil(t, appErr)
	pageA, wasCreated, appErr := h.svc.PublishPageDraft(spaceA, userA, pageID, false, app.ReadViaMember)
	require.Nil(t, appErr)
	require.True(t, wasCreated)

	// userB has no draft for pageID (the cross-space reservation was rejected above), so publishing
	// from space B must fail with 404 (draft not found). force cannot bypass this.
	for _, force := range []bool{false, true} {
		_, _, appErr = h.svc.PublishPageDraft(spaceB, userB, pageID, force, app.ReadViaMember)
		require.NotNil(t, appErr, "cross-space publish (force=%v) must fail", force)
		require.Equal(t, http.StatusNotFound, appErr.StatusCode,
			"cross-space publish (force=%v) must be rejected", force)
	}

	// Space A's page is unchanged.
	stillA, appErr := h.svc.GetPageInSpace("test", pageID, spaceA.Id, false)
	require.Nil(t, appErr)
	require.Contains(t, stillA.Body, "a content")
	require.Equal(t, pageA.EditAt, stillA.EditAt, "cross-space publish must not have modified space A's page")
}

func TestUpdatePageDraftPreservesTitleOnBodyOnlyAutosave(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Keep Title", "")
	require.Nil(t, appErr)
	pageID := draft.PageId

	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: pageID, Title: "Keep Title", Body: docWith("v1")}, nil, nil, nil, "")
	require.Nil(t, appErr)

	// A heartbeat that sends only the body (empty title) must not wipe the stored title.
	saved, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: pageID, Body: docWith("v2")}, nil, nil, nil, "")
	require.Nil(t, appErr)
	require.Equal(t, "Keep Title", saved.Title, "a body-only autosave must not clear the draft title")
}

func TestUpdatePageDraftSanitizesBody(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)

	// The autosave path must sanitize the body on the same content path as publish.
	saved, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: draft.PageId, Title: "Doc",
		Body: `{"type":"doc","content":[{"type":"image","attrs":{"src":"x","onerror":"alert(document.cookie)"}}]}`,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)
	require.NotContains(t, saved.Body, "onerror", "autosave must sanitize the draft body")
}

// TestPublishEditIgnoresStaleParentGuard reproduces the edit-path parent-guard bug: a live child
// page whose in-progress draft still carries a parent id that has since become non-live must still
// publish a content-only edit, because the edit path never reparents.
func TestPublishEditIgnoresStaleParentGuard(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	parent := publishNewPage(t, h, space, userID, "Parent", "p")

	// Publish a child under the (live) parent.
	childDraft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Child", parent.Id)
	require.Nil(t, appErr)
	childID := childDraft.PageId
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: childID, Title: "Child", Body: docWith("c1")}, nil, nil, nil, "")
	require.Nil(t, appErr)
	child, _, appErr := h.svc.PublishPageDraft(space, userID, childID, false, app.ReadViaMember)
	require.Nil(t, appErr)

	// Start an edit session whose draft still carries the (currently live) parent id.
	parentID := parent.Id
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: childID, Title: "Child", Body: docWith("c2"),
		BaseEditAt: child.EditAt,
	}, &parentID, nil, nil, "")
	require.Nil(t, appErr)

	// The parent is deleted mid-edit (its children are promoted), leaving the draft's parent id stale.
	requireStoreDeletePage(t, h.store, parent.Id, space.Id, userID)

	// A content-only edit-publish must still succeed: the edit path does not reparent.
	republished, wasCreated, appErr := h.svc.PublishPageDraft(space, userID, childID, false, app.ReadViaMember)
	require.Nil(t, appErr, "edit-publish must not be blocked by a stale parent id")
	require.False(t, wasCreated)
	require.Contains(t, republished.Body, "c2")
}

func TestUpdatePageDraftRejectsInvalidPageID(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: "not-a-valid-id", Title: "x"}, nil, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestCreateSpaceDraftRejectsForeignDraftParent(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userA, userB := mmmodel.NewId(), mmmodel.NewId()

	parent, appErr := h.svc.CreateSpaceDraft(userA, space.Id, "Parent", "")
	require.Nil(t, appErr)

	// userB cannot parent a draft under userA's draft.
	_, appErr = h.svc.CreateSpaceDraft(userB, space.Id, "Child", parent.PageId)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)

	// userA can, and the child then publishes only after the parent does.
	child, appErr := h.svc.CreateSpaceDraft(userA, space.Id, "Child", parent.PageId)
	require.Nil(t, appErr)
	_, _, appErr = h.svc.PublishPageDraft(space, userA, child.PageId, false, app.ReadViaMember)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode, "child cannot publish under an unpublished parent")
}

func TestUpdatePageDraftRejectsInvalidUserID(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: "bad-id", SpaceId: space.Id, PageId: mmmodel.NewId()}, nil, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestUpdatePageDraftRejectsInvalidSpaceID(t *testing.T) {
	h := openTestService(t)

	_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: mmmodel.NewId(), SpaceId: "bad-id", PageId: mmmodel.NewId()}, nil, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestCreateSpaceDraftRejectsEmptyTitle(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.CreateSpaceDraft(mmmodel.NewId(), space.Id, "", "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestCreateSpaceDraftRejectsTitleOverCap(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	longTitle := strings.Repeat("x", model.PageTitleMaxRunes+1)

	_, appErr := h.svc.CreateSpaceDraft(mmmodel.NewId(), space.Id, longTitle, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestCreateSpaceDraftRejectsMalformedParentID(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.CreateSpaceDraft(mmmodel.NewId(), space.Id, "Doc", "not-a-valid-id")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

// TestUpdatePageDraftRejectsResurrectionAfterNewPagePublish covers the else-if-!ok branch of the
// resurrection guard: a late autosave with no edit baseline (new-page path) must be rejected after
// the draft has already been consumed by publish, rather than recreating a phantom draft.
func TestUpdatePageDraftRejectsResurrectionAfterNewPagePublish(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Doc", "")
	require.Nil(t, appErr)
	pageID := draft.PageId

	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: pageID, Body: docWith("v1")}, nil, nil, nil, "")
	require.Nil(t, appErr)
	_, _, appErr = h.svc.PublishPageDraft(space, userID, pageID, false, app.ReadViaMember)
	require.Nil(t, appErr)

	// Late autosave with no baseline: the draft is gone, so this must be rejected.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: pageID, Body: docWith("late"),
	}, nil, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	// No phantom draft must have been created.
	_, appErr = h.svc.GetPageDraft(userID, space.Id, pageID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
}

// TestUpdatePageDraftRejectsDraftParentCycle covers the cycle-detection guard: setting a draft's
// parent to a page that already (transitively) points back to the draft must be rejected.
func TestUpdatePageDraftRejectsDraftParentCycle(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draftA, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "A", "")
	require.Nil(t, appErr)
	draftB, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "B", "")
	require.Nil(t, appErr)

	// B → A is valid (A has no parent).
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: draftB.PageId,
	}, &draftA.PageId, nil, nil, "")
	require.Nil(t, appErr)

	// A → B would create A → B → A: a cycle. This must be rejected.
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: draftA.PageId,
	}, &draftB.PageId, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestUpdatePageDraftRejectsDraftHierarchyTooDeep(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// Build a chain of model.MaxPageDepth+1 drafts. The first draft is the root
	// (no parent). Each subsequent draft sets its parent to the previous one.
	// A chain of model.MaxPageDepth drafts fills the limit, so adding one more
	// child is the first rejection.
	const chainLen = model.MaxPageDepth
	drafts := make([]*model.Draft, chainLen)
	for i := range chainLen {
		d, appErr := h.svc.CreateSpaceDraft(userID, space.Id, fmt.Sprintf("D%d", i), "")
		require.Nil(t, appErr)
		drafts[i] = d
	}
	for i := 1; i < chainLen; i++ {
		_, appErr := h.svc.UpdatePageDraft(&model.Draft{
			UserId: userID, SpaceId: space.Id, PageId: drafts[i].PageId,
		}, &drafts[i-1].PageId, nil, nil, "")
		require.Nil(t, appErr, "chaining draft %d under draft %d must succeed", i, i-1)
	}

	// Adding one more level (depth 11) must be rejected.
	extra, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Extra", "")
	require.Nil(t, appErr)
	leaf := drafts[chainLen-1].PageId
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: extra.PageId,
	}, &leaf, nil, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestDeletePageDraftReparentsChildren(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// Create a parent draft P and a child draft C that points to P.
	draftP, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Parent", "")
	require.Nil(t, appErr)

	draftC, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Child", "")
	require.Nil(t, appErr)

	_, appErr = h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: draftC.PageId,
	}, &draftP.PageId, nil, nil, "")
	require.Nil(t, appErr)

	// Discard the parent draft.
	appErr = h.svc.DeletePageDraft(userID, space.Id, draftP.PageId, "")
	require.Nil(t, appErr)

	// The parent must be gone.
	_, appErr = h.svc.GetPageDraft(userID, space.Id, draftP.PageId)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)

	// The child must still exist, reparented to the root (ParentId = "").
	got, appErr := h.svc.GetPageDraft(userID, space.Id, draftC.PageId)
	require.Nil(t, appErr)
	require.Equal(t, "", got.ParentId, "child must be reparented to root after parent draft is discarded")
}

func TestGetPageDraftCrossSpaceReturns404(t *testing.T) {
	h := openTestService(t)
	spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
	spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	draft, appErr := h.svc.CreateSpaceDraft(userID, spaceA.Id, "Doc", "")
	require.Nil(t, appErr)

	// GetPageDraft through spaceB must return 404, not expose the draft.
	_, appErr = h.svc.GetPageDraft(userID, spaceB.Id, draft.PageId)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusNotFound, appErr.StatusCode)
}

func TestDeletePageDraftRejectsInvalidUserID(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	appErr := h.svc.DeletePageDraft("bad-id", space.Id, mmmodel.NewId(), "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestDeletePageDraftRejectsInvalidPageID(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	appErr := h.svc.DeletePageDraft(mmmodel.NewId(), space.Id, "bad-id", "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestPublishNewPageRejectsCrossSpaceParent(t *testing.T) {
	h := openTestService(t)
	spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
	spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// Publish a live parent in spaceA.
	parentInA := publishNewPage(t, h, spaceA, userID, "Parent", "p")

	// The store rejects a draft whose ParentId points at a page in another space.
	// To simulate the race (parent was in the same space when editing started, then moved),
	// inject the draft row directly, bypassing the app-layer parent check.
	pageID := mmmodel.NewId()
	now := mmmodel.GetMillis()
	_, err := h.db.Exec(
		`INSERT INTO docs_draft (userid,spaceid,pageid,parentid,title,body,fileids,props,createat,updateat,lastactiveat)
		 VALUES ($1,$2,$3,$4,$5,$6,'[]','{}', $7,$7,$7)`,
		userID, spaceB.Id, pageID, parentInA.Id, "Child", docWith("body"), now,
	)
	require.NoError(t, err)

	// Publishing must reject: the parent lives in a different space than the draft's space. The guard
	// collapses "cross-space" and "not a live page" into a single parent_unpublished 409 so the
	// response can't be used to probe page ids in spaces the caller cannot read.
	_, _, appErr := h.svc.PublishPageDraft(spaceB, userID, pageID, false, app.ReadViaMember)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode, "cross-space parent must be rejected: %v", appErr)
	require.Equal(t, "app.page_draft.publish.parent_unpublished.app_error", appErr.Id)
}

func TestGetPageActiveEditorsRejectsInvalidPageID(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, appErr := h.svc.GetPageActiveEditors("not-valid", space.Id)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestCreateSpaceDraftEnforcesQuota(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// Fill the user's quota by inserting rows directly; calling CreateSpaceDraft 100 times
	// would be slow and the store enforcement path (inside UpsertDraft's transaction) is what
	// we're testing.
	now := mmmodel.GetMillis()
	for i := range model.MaxDraftsPerUserPerSpace {
		pageID := mmmodel.NewId()
		title := fmt.Sprintf("draft-%d", i)
		_, err := h.db.Exec(
			`INSERT INTO docs_draft (userid,spaceid,pageid,parentid,title,body,fileids,props,createat,updateat,lastactiveat)
			 VALUES ($1,$2,$3,'',$4,'[]','[]','{}', $5,$5,$5)`,
			userID, space.Id, pageID, title, now,
		)
		require.NoError(t, err)
	}

	_, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "One Too Many", "")
	require.NotNil(t, appErr)
	// 422, not 429: the cap is a standing per-space quota, so retrying cannot clear it.
	require.Equal(t, http.StatusUnprocessableEntity, appErr.StatusCode)
	require.Equal(t, "app.page_draft.quota_exceeded.app_error", appErr.Id)
}

// TestGetPageDraftRejectsInvalidIDs exercises GetPageDraft's three input-validation branches at
// the service layer, independent of HTTP routing.
func TestGetPageDraftRejectsInvalidIDs(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID, pageID := mmmodel.NewId(), mmmodel.NewId()

	cases := []struct {
		name                    string
		userID, spaceID, pageID string
	}{
		{"invalid user id", "not-valid", space.Id, pageID},
		{"invalid space id", userID, "not-valid", pageID},
		{"invalid page id", userID, space.Id, "not-valid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, appErr := h.svc.GetPageDraft(tc.userID, tc.spaceID, tc.pageID)
			require.NotNil(t, appErr)
			require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
		})
	}
}

// TestGetPageDraftsForSpaceRejectsInvalidIDs exercises the list endpoint's service-level input
// validation, which handler tests reach only through valid routes.
func TestGetPageDraftsForSpaceRejectsInvalidIDs(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())

	_, _, appErr := h.svc.GetPageDraftsForSpace("not-valid", space.Id, 0, 10)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)

	_, _, appErr = h.svc.GetPageDraftsForSpace(mmmodel.NewId(), "not-valid", 0, 10)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

func TestGetPageActiveEditorsRejectsInvalidSpaceID(t *testing.T) {
	h := openTestService(t)

	_, appErr := h.svc.GetPageActiveEditors(mmmodel.NewId(), "not-valid")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

// TestPublishForceAppliesDraftFieldOverConcurrentEdit covers the force-publish interaction where
// the concurrent edit touched BOTH a field the draft changed and one it did not: the draft's
// value must win for the field it carries, while the concurrent value survives for the field the
// draft left unset.
func TestPublishForceAppliesDraftFieldOverConcurrentEdit(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	page := publishNewPage(t, h, space, userID, "Original title", "original body")
	baseEditAt := page.EditAt

	// A title-only edit draft baselined at the current EditAt.
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{
		UserId: userID, SpaceId: space.Id, PageId: page.Id, Title: "Draft title",
		BaseEditAt: baseEditAt,
	}, nil, nil, nil, "")
	require.Nil(t, appErr)

	// A concurrent edit changes the title AND the body, advancing EditAt past the baseline.
	concurrentTitle := "Concurrent title"
	concurrentBody := docWith("concurrent body")
	_, appErr = h.svc.UpdatePage(page.Id, space.Id,
		&model.PagePatch{Title: &concurrentTitle, Body: &concurrentBody}, new(baseEditAt), false, userID)
	require.Nil(t, appErr)

	forced, _, appErr := h.svc.PublishPageDraft(space, userID, page.Id, true, app.ReadViaMember)
	require.Nil(t, appErr)
	require.Equal(t, "Draft title", forced.Title, "the field the draft carries must win under force")
	require.Contains(t, forced.Body, "concurrent body", "a field the draft never set must keep the concurrent value")
}

// TestUpdatePageDraftRejectsOversizedFileIds covers the inline fileIDs size guard: fileIDs travels
// to the store as a separate write-intent pointer, so Draft.IsValid never sees it and this guard
// is the only bound on the write path.
func TestUpdatePageDraftRejectsOversizedFileIds(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// 12 valid ids serialize to ~349 runes, over the DraftFileIdsMaxRunes=300 cap.
	ids := make(mmmodel.StringArray, 0, 12)
	for range 12 {
		ids = append(ids, mmmodel.NewId())
	}

	_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: mmmodel.NewId(), Title: "x"}, nil, &ids, nil, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "model.draft.is_valid.file_ids.app_error", appErr.Id)
}

// TestUpdatePageDraftRejectsOversizedProps covers the inline props size guard: props travels to the
// store as a separate write-intent pointer, so Draft.IsValid checks the (empty) draft.Props field
// and this guard is the only bound on the written value.
func TestUpdatePageDraftRejectsOversizedProps(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	props := mmmodel.StringInterface{"k": strings.Repeat("x", model.PagePropsMaxBytes)}
	_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: mmmodel.NewId(), Title: "x"}, nil, nil, &props, "")
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, "model.shared.props_too_large.app_error", appErr.Id)
}

// TestUpdatePageDraftRejectsInvalidFileId covers the per-entry id check on the fileIDs write
// intent, including the empty entry an empty-slice clear must not admit.
func TestUpdatePageDraftRejectsInvalidFileId(t *testing.T) {
	h := openTestService(t)
	space := mustCreateSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	for name, entry := range map[string]string{"malformed id": "not-a-valid-id", "empty entry": ""} {
		t.Run(name, func(t *testing.T) {
			ids := mmmodel.StringArray{entry}
			_, appErr := h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: mmmodel.NewId(), Title: "x"}, nil, &ids, nil, "")
			require.NotNil(t, appErr)
			require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
			require.Equal(t, "model.draft.is_valid.file_id.app_error", appErr.Id)
		})
	}
}

// TestPublishPageDraftUndoesAutoJoinOnLaterRejection covers the deferred rollback inside
// PublishPageDraft: the auto-join pre-step admits a fall-through caller by creating a membership,
// the write gate then passes, and a rejection landing after the gate (here the parent guard's 409
// on a no-longer-live parent) must remove that membership rather than leave it behind.
func TestPublishPageDraftUndoesAutoJoinOnLaterRejection(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Registered ahead of the harness's catch-all channel grants: only read_page is withheld, so
	// the pre-step's in-lock re-validation still resolves the open-space fall-through while the
	// write gate's create_page re-check after the join passes as a member.
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionReadPage).Return(false).Maybe()
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)
	mockAPI.On("DeleteChannelMember", space.ChannelId, userID).Return(nil)

	// A draft whose parent is live at draft time but not at publish: the parent guard rejects the
	// publish only after the gate has run, which is what routes the failure through the deferred
	// rollback rather than the gate's own error path. The parent row is soft-deleted directly —
	// store.DeletePage would reparent the pending draft as part of its cascade — reproducing the
	// stale-ParentId race the guard exists for.
	parent := mustCreatePage(t, h.store, space.Id, space.ChannelId, mmmodel.NewId(), "")
	draft, appErr := h.svc.CreateSpaceDraft(userID, space.Id, "Rolled back", parent.Id)
	require.Nil(t, appErr)
	_, appErr = h.svc.UpdatePageDraft(&model.Draft{UserId: userID, SpaceId: space.Id, PageId: draft.PageId, Title: "Rolled back", Body: docWith("kept private")}, nil, nil, nil, "")
	require.Nil(t, appErr)
	_, err := h.db.Exec(`UPDATE docs_page SET deleteat = $1 WHERE id = $2`, mmmodel.GetMillis(), parent.Id)
	require.NoError(t, err)

	page, wasCreated, appErr := h.svc.PublishPageDraft(space, userID, draft.PageId, false, app.ReadViaOpenFallthrough)
	require.Nil(t, page)
	require.False(t, wasCreated)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)

	mockAPI.AssertCalled(t, "AddChannelMember", space.ChannelId, userID)
	// The membership the auto-join pre-step created must be removed when the publish is rejected
	// after the gate.
	mockAPI.AssertCalled(t, "DeleteChannelMember", space.ChannelId, userID)
}
