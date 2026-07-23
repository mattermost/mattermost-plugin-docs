// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/plugin"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// TestHandler_DraftLifecycle drives the draft routes end to end over the real router: create a
// new-page draft, autosave it, read it back, publish it (201, reserved id preserved), and confirm
// the draft is gone afterwards.
func TestHandler_DraftLifecycle(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "New Doc"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	pageID := draft.PageId
	require.True(t, mmmodel.IsValidId(pageID))

	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userID, map[string]any{
		"title": "New Doc",
		"body":  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "hello")

	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userID, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var page model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Equal(t, pageID, page.Id, "publish preserves the reserved draft id")

	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "draft is deleted on publish")
}

// TestHandler_ListSpaceDrafts covers the drafts listing: it is paginated like every other list
// endpoint, and it omits draft bodies so the listing does not ship whole documents.
func TestHandler_ListSpaceDrafts(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	for _, title := range []string{"First", "Second", "Third"} {
		rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": title})
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	var listed struct {
		Items   []map[string]any `json:"items"`
		Page    int              `json:"page"`
		PerPage int              `json:"per_page"`
		HasMore bool             `json:"has_more"`
	}

	rec := h.do(t, http.MethodGet, base+"/drafts?page=0&per_page=2", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 2)
	require.True(t, listed.HasMore, "a third draft remains")
	require.NotContains(t, listed.Items[0], "body", "the listing must not ship draft bodies")

	rec = h.do(t, http.MethodGet, base+"/drafts?page=1&per_page=2", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 1)
	require.False(t, listed.HasMore, "the last page reports no more")
}

// TestHandler_PublishMalformedBodyReturns400 is the regression for the unchecked-decode bug: a
// malformed publish body must be rejected with 400 and must NOT publish the draft.
func TestHandler_PublishMalformedBodyReturns400(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "Doc"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))

	// Raw, truncated JSON body — h.do would marshal a value, so issue the request directly.
	req := httptest.NewRequest(http.MethodPost, base+"/pages/"+draft.PageId+"/draft/publish", bytes.NewReader([]byte(`{"force":tru`)))
	req.Header.Set("Mattermost-User-ID", userID)
	malformed := httptest.NewRecorder()
	h.plugin.ServeHTTP(&plugin.Context{}, malformed, req)
	require.Equal(t, http.StatusBadRequest, malformed.Code)

	// The draft must still exist (publish did not run).
	rec = h.do(t, http.MethodGet, base+"/pages/"+draft.PageId+"/draft", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, "a malformed publish body must not publish or delete the draft")
}

// TestHandler_UpdatePageDraftRequiresExistingDraft confirms the update-only guard: PUT on a page id
// that has no existing draft must return 404 rather than silently creating one.
func TestHandler_UpdatePageDraftRequiresExistingDraft(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()

	// No draft has been created for this page id — the PUT must be rejected.
	rec := h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+mmmodel.NewId()+"/draft", userID, map[string]any{
		"title": "ghost",
	})
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandler_UpdatePageDraftClearsParentWhenParentIdIsEmptyString covers the null-vs-empty
// distinction: sending parent_id: "" in the PUT body must clear an existing parent (set it to
// root), while omitting parent_id entirely must leave the parent unchanged.
func TestHandler_UpdatePageDraftClearsParentWhenParentIdIsEmptyString(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	// Create the parent draft.
	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "Parent"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var parentDraft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &parentDraft))

	// Create the child draft under the parent.
	rec = h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{
		"title":     "Child",
		"parent_id": parentDraft.PageId,
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var childDraft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &childDraft))
	require.Equal(t, parentDraft.PageId, childDraft.ParentId)

	// PUT with parent_id omitted must preserve the existing parent.
	rec = h.do(t, http.MethodPatch, base+"/pages/"+childDraft.PageId+"/draft", userID, map[string]any{
		"title": "Child updated",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var saved model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	require.Equal(t, parentDraft.PageId, saved.ParentId, "omitting parent_id must preserve the existing parent")

	// PUT with parent_id: "" must clear the parent to root.
	rec = h.do(t, http.MethodPatch, base+"/pages/"+childDraft.PageId+"/draft", userID, map[string]any{
		"parent_id": "",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	require.Empty(t, saved.ParentId, `parent_id: "" must clear the parent`)
}

// TestHandler_UpdatePageDraftCreatesForExistingPage drives the existing-page edit flow: publish a
// new-page draft to get a live page, then open an edit session by PATCH .../draft with the page's
// EditAt baseline in the top-level base_edit_at field. Verifies the draft is created, autosave
// updates it, publish succeeds (edit path → 200), and the draft is gone afterwards.
func TestHandler_UpdatePageDraftCreatesForExistingPage(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	// Step 1: create a new-page draft and publish it to get a live page.
	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "Original"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	pageID := draft.PageId

	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userID, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var page model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Equal(t, pageID, page.Id)

	// Step 2: open an edit session — first PUT creates the draft for an existing page.
	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userID, map[string]any{
		"title":        "Original",
		"base_edit_at": page.EditAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var editDraft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &editDraft))
	require.Equal(t, pageID, editDraft.PageId)
	require.Equal(t, page.EditAt, editDraft.BaseEditAt, "draft must carry the base_edit_at baseline so publish can detect conflicts")

	// GET must round-trip the same baseline.
	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var fetched model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &fetched))
	require.Equal(t, page.EditAt, fetched.BaseEditAt, "GET must return the base_edit_at baseline that was set via PATCH")

	// Step 3: autosave new content.
	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userID, map[string]any{
		"title": "Edited",
		"body":  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"updated"}]}]}`,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	// Step 4: publish the edit — edit path returns 200 (not 201).
	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code, "edit-path publish must return 200")
	var updated model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, pageID, updated.Id)
	require.Equal(t, "Edited", updated.Title)

	// Step 5: the draft must be gone after publish.
	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "draft must be deleted on publish")
}

// TestHandler_PublishConflict409 covers the stale-baseline 409: when user B publishes an edit
// that advances the page's EditAt, user A's publish against the original baseline must return 409.
func TestHandler_PublishConflict409(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	userA := mmmodel.NewId()
	userB := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	// Create a new-page draft and publish it to get a live page.
	rec := h.do(t, http.MethodPost, base+"/drafts", userA, map[string]any{"title": "Shared Doc"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	pageID := draft.PageId

	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userA, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var page model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	editAt := page.EditAt

	// User A and user B both open edit sessions against the same baseline.
	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userA, map[string]any{
		"title":        "Edit by A",
		"base_edit_at": editAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userB, map[string]any{
		"title":        "Edit by B",
		"base_edit_at": editAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	// User B publishes first, advancing the page's EditAt.
	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userB, nil)
	require.Equal(t, http.StatusOK, rec.Code, "user B edit-path publish must succeed")

	// User A publishes against the now-stale baseline — must get 409.
	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userA, nil)
	require.Equal(t, http.StatusConflict, rec.Code, "stale baseline must return 409 Conflict")

	// The 409 body carries the current server page so the client can diff and re-baseline without a
	// follow-up read: it reflects user B's winning edit and the advanced EditAt.
	var conflict struct {
		Error       *mmmodel.AppError `json:"error"`
		CurrentPage *model.Page       `json:"current_page"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.NotNil(t, conflict.Error, "conflict body must include the error")
	require.NotNil(t, conflict.CurrentPage, "conflict body must include the current server page")
	require.Equal(t, "Edit by B", conflict.CurrentPage.Title, "current page must reflect the winning edit")
	require.Greater(t, conflict.CurrentPage.EditAt, editAt, "current page must carry the advanced baseline")
}

// TestHandler_DeletePageDraft verifies the DELETE endpoint: 200 on success, draft is gone
// afterwards, and 404 when no draft exists.
func TestHandler_DeletePageDraft(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	// Create a new-page draft.
	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "To Delete"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	pageID := draft.PageId

	// DELETE the draft — must return 200.
	rec = h.do(t, http.MethodDelete, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// GET after delete must return 404.
	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "draft must be gone after delete")

	// DELETE again on a missing draft must return 404.
	rec = h.do(t, http.MethodDelete, base+"/pages/"+pageID+"/draft", userID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "deleting a non-existent draft must return 404")
}

// TestHandler_ActiveEditorsWrongSpaceReturns404 is the regression for the cross-space presence
// leak: a page in one space must not be reachable through another space's active-editors route.
func TestHandler_ActiveEditorsWrongSpaceReturns404(t *testing.T) {
	h := openTestPlugin(t, nil)
	spaceA := seedSpace(t, h.store, mmmodel.NewId())
	spaceB := seedSpace(t, h.store, mmmodel.NewId())
	pageInB := seedPage(t, h.store, spaceB.Id, spaceB.ChannelId, "")
	userID := mmmodel.NewId()

	rec := h.do(t, http.MethodGet, "/api/v1/spaces/"+spaceA.Id+"/pages/"+pageInB.Id+"/active-editors", userID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "a page must not resolve through another space's route")

	rec = h.do(t, http.MethodGet, "/api/v1/spaces/"+spaceB.Id+"/pages/"+pageInB.Id+"/active-editors", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHandler_ActiveEditorsResponseBody verifies the JSON payload of the active-editors endpoint:
// an empty list when no draft is open, and the editor's user ID when they have an active draft.
func TestHandler_ActiveEditorsResponseBody(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	// Publish a new-page draft to get a live page.
	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "Presence Test"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	pageID := draft.PageId

	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userID, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var page model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))

	// No edit draft open yet — active_editors must be an empty list, not null. The response also
	// carries snapshot_at and active_timeout_ms, mirroring the page_presence_updated WS payload so a client
	// resyncing over REST can reason about snapshot staleness the same way.
	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/active-editors", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		ActiveEditors   []string `json:"active_editors"`
		SnapshotAt      int64    `json:"snapshot_at"`
		ActiveTimeoutMs int64    `json:"active_timeout_ms"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.ActiveEditors)
	require.Empty(t, resp.ActiveEditors)
	require.Positive(t, resp.SnapshotAt, "response must carry the snapshot timestamp")
	require.Equal(t, int64(5*60*1000), resp.ActiveTimeoutMs, "response must carry the active-editor window")

	// Open an edit draft — the user must now appear as an active editor.
	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userID, map[string]any{
		"title":        "Presence Test",
		"base_edit_at": page.EditAt,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.do(t, http.MethodGet, base+"/pages/"+pageID+"/active-editors", userID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.ActiveEditors, userID, "user with an open edit draft must appear as an active editor")
}

// TestHandler_UpdatePageDraftPropsPointerIntent covers the props pointer-intent semantics over the
// HTTP boundary: an absent props field preserves the stored map, an explicit null behaves the same
// as absent, an empty object clears all keys, and a populated object replaces the whole map.
func TestHandler_UpdatePageDraftPropsPointerIntent(t *testing.T) {
	h := openTestPlugin(t, nil)
	space := seedSpace(t, h.store, mmmodel.NewId())
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "Props Test"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	draftPath := base + "/pages/" + draft.PageId + "/draft"

	// Populate props.
	rec = h.do(t, http.MethodPatch, draftPath, userID, map[string]any{
		"title": "Props Test",
		"props": map[string]any{"color": "red"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var populated model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &populated))
	require.Equal(t, "red", populated.Props["color"])

	// props absent must preserve the existing map. Each step below unmarshals into a fresh Draft
	// value: reusing one across json.Unmarshal calls would merge into the existing non-nil map
	// instead of replacing it, masking whether the server actually cleared/replaced the props.
	rec = h.do(t, http.MethodPatch, draftPath, userID, map[string]any{
		"title": "Props Test Updated",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var afterAbsent model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &afterAbsent))
	require.Equal(t, "red", afterAbsent.Props["color"], "omitting props must preserve the existing map")

	// props: null must also preserve the existing map — it unmarshals to a nil pointer, same as
	// omitting the field entirely.
	rec = h.do(t, http.MethodPatch, draftPath, userID, map[string]any{
		"props": nil,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var afterNull model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &afterNull))
	require.Equal(t, "red", afterNull.Props["color"], "props: null must preserve the existing map, same as omitting it")

	// props: {} must clear all keys.
	rec = h.do(t, http.MethodPatch, draftPath, userID, map[string]any{
		"props": map[string]any{},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var afterClear model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &afterClear))
	require.Empty(t, afterClear.Props, "props: {} must clear all keys")

	// props populated again must replace the (now-empty) map.
	rec = h.do(t, http.MethodPatch, draftPath, userID, map[string]any{
		"props": map[string]any{"size": "large"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var afterReplace model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &afterReplace))
	require.Equal(t, "large", afterReplace.Props["size"])
	require.NotContains(t, afterReplace.Props, "color", "props replace must drop keys not present in the new map")
}

// TestHandler_UpdatePageDraftPropsBaselineNoLongerHonored covers the removal of the old
// props.original_page_edit_at baseline convention: placing the value only under that props key must
// NOT establish an optimistic-lock baseline. Since a baseline-less edit-open against a live page is
// itself rejected (the store refuses to create a baseline-less edit draft for an existing page — see
// TestPublishRejectsNoBaselineOnExistingPageEdit in server/app/page_draft_test.go), the PATCH must
// fail with the same conflict the client would get if it had supplied no baseline at all.
func TestHandler_UpdatePageDraftPropsBaselineNoLongerHonored(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	userID := mmmodel.NewId()
	base := "/api/v1/spaces/" + space.Id

	rec := h.do(t, http.MethodPost, base+"/drafts", userID, map[string]any{"title": "Doc"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var draft model.Draft
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	pageID := draft.PageId

	rec = h.do(t, http.MethodPost, base+"/pages/"+pageID+"/draft/publish", userID, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var page model.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))

	// Open an edit session but put the baseline only under the old props key, not the top-level
	// base_edit_at field. This must be rejected with 409, exactly as if base_edit_at had been
	// omitted entirely (proving the props key carries no special meaning any more).
	rec = h.do(t, http.MethodPatch, base+"/pages/"+pageID+"/draft", userID, map[string]any{
		"title": "Edited",
		"props": map[string]any{"original_page_edit_at": page.EditAt},
	})
	require.Equal(t, http.StatusConflict, rec.Code, "props.original_page_edit_at must not be honored as a baseline")
}
