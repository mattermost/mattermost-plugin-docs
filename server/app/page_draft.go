// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

func presenceBroadcastKey(pageID, userID string) string {
	return pageID + ":" + userID
}

// UpdatePageDraft upserts the calling user's autosave draft for a page in a space. channelID is
// the space's backing channel, used to scope the presence broadcast.
//
// PageId is the unified page id stable across the draft → publish lifecycle, so a draft may exist
// before the page is published. The space must exist and be live. The caller owns the draft: userID
// is always sourced from the request, never the request body.
//
// An autosave may omit fields the editor didn't change; omitted fields are preserved, so concurrent
// heartbeats cannot clobber each other's changes.
// parentID encodes the write intent for ParentId: nil preserves the stored value, a pointer to ""
// clears to root, and a pointer to a valid ID sets the parent. See store.UpsertDraft for details.
func (s *Service) UpdatePageDraft(draft *model.Draft, parentID *string, fileIDs *mmmodel.StringArray, channelID string) (*model.Draft, *mmmodel.AppError) {
	if draft == nil {
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.nil_draft.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(draft.UserId) {
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(draft.SpaceId) {
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(draft.PageId) {
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.invalid_page_id.app_error", nil, "", http.StatusBadRequest)
	}
	if parentID != nil && *parentID != "" && !mmmodel.IsValidId(*parentID) {
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.invalid_parent_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Updating page draft", "space_id", draft.SpaceId, "page_id", draft.PageId, "user_id", draft.UserId)
	if draft.Title != "" {
		title, titleErr := validateTitle("UpdatePageDraft", draft.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return nil, titleErr
		}
		draft.Title = title
	}

	// Sanitize the draft body on the same content path as publish, so a stored draft never holds
	// unsanitized markup. Defense-in-depth: only the author can read a draft back today, but any
	// future reader of Draft.Body inherits a sanitized value.
	if draft.Body != "" {
		sanitizedBody, _, contentErr := normalizePageContent("UpdatePageDraft", draft.Body)
		if contentErr != nil {
			return nil, contentErr
		}
		draft.Body = sanitizedBody
	}

	// Only the optimistic-lock baseline is a recognized prop; drop anything else the client sent so
	// it cannot accumulate in the stored map, which the store merges into rather than replaces.
	draft.SanitizeProps()

	// Validate fileIDs size here because fileIDs is passed to the store separately and is not placed
	// into draft.FileIds before IsValid runs — the store's UpsertDraft merges it in SQL.
	if fileIDs != nil && len(*fileIDs) > 0 {
		if utf8.RuneCountInString(mmmodel.ArrayToJSON([]string(*fileIDs))) > model.DraftFileIdsMaxRunes {
			return nil, mmmodel.NewAppError("UpdatePageDraft", "model.draft.is_valid.file_ids.app_error", nil, "", http.StatusBadRequest)
		}
		for _, fileID := range *fileIDs {
			// Reject "" too: an empty slice clears the list, but an empty entry is malformed and
			// would otherwise be merged verbatim into FileIds.
			if !mmmodel.IsValidId(fileID) {
				return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.invalid_file_id.app_error", nil, "", http.StatusBadRequest)
			}
		}
	}

	// pageIsLiveResolved tracks whether we already know the live-page answer from the
	// not-found branch below (so we don't issue a second PageExistsInSpace after the upsert).
	pageIsLive := false
	pageIsLiveResolved := false

	existingDraft, existingDraftErr := s.store.GetDraft(draft.UserId, draft.PageId)
	switch {
	case existingDraftErr != nil && !store.IsErrNotFound(existingDraftErr):
		return nil, storeAppError("UpdatePageDraft", existingDraftErr)
	case existingDraftErr == nil && existingDraft.SpaceId != draft.SpaceId:
		// Existing draft belongs to a different space: reject to prevent cross-space drift.
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.page_not_found.app_error", nil, "", http.StatusNotFound)
	case store.IsErrNotFound(existingDraftErr):
		// No draft for this user+page. Allow only if the page ID is "known" — either another
		// user already reserved it via CreateSpaceDraft, or it is a published page in this space.
		// This prevents PATCH /spaces/X/pages/<random-id>/draft from ghost-drafting a non-existent page.
		var existsErr error
		pageIsLive, existsErr = s.store.PageExistsInSpace(draft.PageId, draft.SpaceId)
		if existsErr != nil {
			return nil, storeAppError("UpdatePageDraft", existsErr)
		}
		pageIsLiveResolved = true
		if !pageIsLive {
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.page_not_found.app_error", nil, "", http.StatusNotFound)
		}
	}

	saved, savedPageWasLive, err := s.store.UpsertDraft(draft, parentID, fileIDs)
	if err != nil {
		switch store.ConflictReason(err) {
		case store.ReasonConcurrentEdit:
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.edit_conflict.app_error",
				nil, "", http.StatusConflict).Wrap(err)
		case store.ReasonConcurrentAutosave:
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.draft_changed.app_error",
				nil, "", http.StatusConflict).Wrap(err)
		}
		return nil, storeAppError("UpdatePageDraft", err)
	}

	// New-page drafts (no published page row yet) must not broadcast presence to the space channel:
	// that would expose the reserved page ID and the author's identity to all space members before
	// the page exists. Send the event only to the author so their own UI can track the session.
	// UpsertDraft already determined liveness under its page-row lock, so reuse its result here;
	// only the no-existing-draft branch above resolved it independently.
	if !pageIsLiveResolved {
		pageIsLive = savedPageWasLive
	}
	if !pageIsLive {
		s.publishSelfPresence(saved)
		return saved, nil
	}

	// Existing published page: rate-limited channel-wide broadcast so other viewers see this user
	// in the active-editors indicator. Key by page+user so concurrent editors don't suppress each
	// other's first broadcast.
	presenceKey := presenceBroadcastKey(saved.PageId, saved.UserId)
	now := mmmodel.GetMillis()
	s.sweepPresenceBroadcastLast(now)
	existing, loaded := s.presenceBroadcastLast.LoadOrStore(presenceKey, now)
	if loaded {
		lastTime, ok := existing.(int64)
		if !ok || now-lastTime < presenceBroadcastMinIntervalMs {
			return saved, nil
		}
		if !s.presenceBroadcastLast.CompareAndSwap(presenceKey, existing, now) {
			return saved, nil
		}
	}
	s.broadcastPagePresence(saved.PageId, saved.SpaceId, channelID)

	return saved, nil
}

// CreateSpaceDraft creates a new-page draft with a server-generated, reserved page id, so a new
// page has a stable link before it is published. title is required; pageParentID, when set, must be
// a published page in the space or an existing draft of the caller.
// The per-user-per-space draft quota is enforced atomically inside the store.
func (s *Service) CreateSpaceDraft(userID, spaceID, title, pageParentID string) (*model.Draft, *mmmodel.AppError) {
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("CreateSpaceDraft", "app.page_draft.create.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("CreateSpaceDraft", "app.page_draft.create.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Creating space draft", "space_id", spaceID, "parent_id", pageParentID, "user_id", userID)

	title, titleErr := validateTitle("CreateSpaceDraft", title, model.PageTitleMaxRunes)
	if titleErr != nil {
		return nil, titleErr
	}

	if pageParentID != "" {
		if parentErr := s.validateDraftParent(userID, spaceID, pageParentID); parentErr != nil {
			return nil, parentErr
		}
	}

	draft := &model.Draft{
		UserId:  userID,
		SpaceId: spaceID,
		PageId:  mmmodel.NewId(),
		Title:   title,
		Body:    model.EmptyTipTapJSON,
	}
	var parentPtr *string
	if pageParentID != "" {
		parentPtr = &pageParentID
	}

	// Use UpsertDraft directly: the page row does not exist yet (new-page draft), so
	// UpdatePageDraft's guard — which rejects drafts for non-existent pages on the autosave
	// path — would incorrectly block this call. The page is never live here, so the liveness
	// flag is discarded.
	saved, _, err := s.store.UpsertDraft(draft, parentPtr, nil)
	if err != nil {
		// Translate hierarchy errors with create-specific keys so the client receives an
		// appropriate message. invalidInputAppError maps these to update.* keys, which don't
		// apply to a create operation.
		var invErr *store.ErrInvalidInput
		if errors.As(err, &invErr) {
			switch invErr.Reason {
			case store.ReasonParentNotLive:
				// The parent validated in validateDraftParent can disappear before UpsertDraft's
				// locked check; surface the create-specific key rather than storeAppError's page.* key.
				return nil, mmmodel.NewAppError("CreateSpaceDraft", "app.page_draft.create.invalid_parent.app_error", nil, "", http.StatusBadRequest).Wrap(err)
			case store.ReasonDraftCycle:
				return nil, mmmodel.NewAppError("CreateSpaceDraft", "app.page_draft.create.parent_cycle.app_error", nil, "", http.StatusBadRequest).Wrap(err)
			case store.ReasonDraftTooDeep:
				return nil, mmmodel.NewAppError("CreateSpaceDraft", "app.page_draft.create.parent_too_deep.app_error", nil, "", http.StatusBadRequest).Wrap(err)
			}
		}
		return nil, storeAppError("CreateSpaceDraft", err)
	}

	// Broadcast only to the author: the page is not yet published, so broadcasting channel-wide
	// would expose the reserved page ID and the author's identity to all space members.
	s.publishSelfPresence(saved)
	return saved, nil
}

// validateDraftParent accepts a parent that is either a published page in spaceID or an existing
// draft of the caller in spaceID (which allows child drafts under not-yet-published parents).
func (s *Service) validateDraftParent(userID, spaceID, parentID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(parentID) {
		return mmmodel.NewAppError("validateDraftParent", "app.page_draft.create.invalid_parent_id.app_error", nil, "", http.StatusBadRequest)
	}
	// Probe the parent space-scoped and collapse "not found" and "exists in another space" into one
	// error, so a caller cannot use a distinct response to probe page ids in spaces it cannot read
	// (matches validateParentExists).
	pageExists, existsErr := s.store.PageExistsInSpace(parentID, spaceID)
	if existsErr != nil {
		return storeAppError("validateDraftParent", existsErr)
	}
	if pageExists {
		return nil
	}
	// Not a published page in this space — accept only the caller's own draft in this space.
	_, draftErr := s.GetPageDraft(userID, spaceID, parentID)
	if draftErr == nil {
		return nil
	}
	if draftErr.StatusCode != http.StatusNotFound {
		return draftErr
	}
	return mmmodel.NewAppError("validateDraftParent", "app.page_draft.create.invalid_parent.app_error", nil, "", http.StatusBadRequest)
}

// GetPageDraft returns the calling user's draft for the given page in the given space. Returns not-found
// when no draft exists for that user.
func (s *Service) GetPageDraft(userID, spaceID, pageID string) (*model.Draft, *mmmodel.AppError) {
	if !mmmodel.IsValidId(userID) {
		return nil, mmmodel.NewAppError("GetPageDraft", "app.page_draft.get.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetPageDraft", "app.page_draft.get.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageDraft", "app.page_draft.get.invalid_page_id.app_error", nil, "", http.StatusBadRequest)
	}

	draft, err := s.store.GetDraft(userID, pageID)
	if err != nil {
		if store.IsErrNotFound(err) {
			return nil, mmmodel.NewAppError("GetPageDraft", "app.page_draft.get.not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		return nil, storeAppError("GetPageDraft", err)
	}

	// Defend against a draft key collision across spaces: a draft is keyed by (UserId, PageId),
	// so confirm it belongs to the space named in the request.
	if draft.SpaceId != spaceID {
		return nil, mmmodel.NewAppError("GetPageDraft", "app.page_draft.get.not_found.app_error", nil, "", http.StatusNotFound)
	}

	return draft, nil
}

// DeletePageDraft removes the calling user's draft for the given page (on publish or discard).
// Returns not-found when no draft exists. channelID is the space's backing channel, used to scope
// the presence broadcast.
func (s *Service) DeletePageDraft(userID, spaceID, pageID, channelID string) *mmmodel.AppError {
	if !mmmodel.IsValidId(userID) {
		return mmmodel.NewAppError("DeletePageDraft", "app.page_draft.delete.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return mmmodel.NewAppError("DeletePageDraft", "app.page_draft.delete.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError("DeletePageDraft", "app.page_draft.delete.invalid_page_id.app_error", nil, "", http.StatusBadRequest)
	}
	s.log.Debug("Deleting page draft", "space_id", spaceID, "page_id", pageID, "user_id", userID)

	// A draft is keyed by (UserId, PageId) without SpaceId, so confirm it belongs to the space
	// named in the request before deleting — otherwise a member of another space could delete a
	// draft here by passing this space's id with a foreign page id.
	if _, appErr := s.GetPageDraft(userID, spaceID, pageID); appErr != nil {
		// GetPageDraft returns its own get.* not-found key; translate it to the delete operation's
		// key so a discard reports a delete-appropriate message.
		if appErr.StatusCode == http.StatusNotFound {
			return mmmodel.NewAppError("DeletePageDraft", "app.page_draft.delete.not_found.app_error", nil, "", http.StatusNotFound).Wrap(appErr)
		}
		return appErr
	}

	// Discard is unconditional: the user wants the draft gone regardless of its current version. An
	// autosave already in flight when the discard commits can still re-insert the draft afterward
	// (an unpublished new-page draft has no page row for UpsertDraft's staleness guard to key on), so
	// a discarded draft can briefly reappear. It is per-user and cleared by discarding again; fully
	// preventing it would need a soft-delete tombstone, which is not warranted for this window.
	//
	pageWasLive, delErr := s.store.DeleteDraftReparenting(userID, spaceID, pageID)
	if delErr != nil {
		// A concurrent publish/delete may have removed the draft between the check above and here;
		// treat that benign race as a 404, matching the not-found path of the initial check, rather
		// than a 500 that would also emit a spurious server-side error log.
		if store.IsErrNotFound(delErr) {
			return mmmodel.NewAppError("DeletePageDraft", "app.page_draft.delete.not_found.app_error", nil, "", http.StatusNotFound).Wrap(delErr)
		}
		return storeAppError("DeletePageDraft", delErr)
	}

	// Presence cleanup: only broadcast channel-wide if the page is published. A new-page draft
	// discard was never visible to the channel (no channel broadcast on create), so no cleanup
	// broadcast is needed.
	if !pageWasLive {
		return nil
	}
	s.clearThrottleAndBroadcastPagePresence(pageID, userID, spaceID, channelID)

	return nil
}

// GetPageDraftsForSpace returns a page of the calling user's unpublished drafts for a space, newest
// first. Draft bodies are omitted from the listing.
func (s *Service) GetPageDraftsForSpace(userID, spaceID string, page, perPage int) ([]*model.DraftSummary, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(userID) {
		return nil, false, mmmodel.NewAppError("GetPageDraftsForSpace", "app.page_draft.list.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, false, mmmodel.NewAppError("GetPageDraftsForSpace", "app.page_draft.list.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}

	// The store's liveness join excludes a soft-deleted space, so this need not re-check liveness.
	offset, limit := paginationOffsetLimit(page, perPage)
	drafts, err := s.store.GetDraftsForSpace(userID, spaceID, offset, limit)
	if err != nil {
		return nil, false, storeAppError("GetPageDraftsForSpace", err)
	}
	drafts, hasMore := trimPage(drafts, limit)
	return drafts, hasMore, nil
}

// PublishPageDraft publishes the calling user's draft for pageID in spaceID as a page. The draft
// is validated, new-vs-existing state is re-derived from the database (no client trust), and the
// page write + draft delete are committed in a single store transaction.
// Returns (page, wasCreated, appErr):
//   - wasCreated=true → a new page was inserted by this call (handler should return 201)
//   - wasCreated=false → an existing page was updated, or a concurrent create was adopted (return 200)
func (s *Service) PublishPageDraft(userID, spaceID, pageID string, force bool) (*model.Page, bool, *mmmodel.AppError) {
	s.log.Debug("Publishing page draft", "space_id", spaceID, "page_id", pageID, "user_id", userID, "force", force)
	// 1. Fetch draft (idempotency guard: 404 = draft already published or discarded).
	draft, appErr := s.GetPageDraft(userID, spaceID, pageID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.draft_not_found.app_error",
				nil, "", http.StatusNotFound).Wrap(appErr)
		}
		return nil, false, appErr
	}

	// 2. Derive isNewPage from the database; never trust client state.
	existing, existingErr := s.GetPageWithDeleted(pageID)
	isNewPage := false
	switch {
	case existingErr != nil && existingErr.StatusCode == http.StatusNotFound:
		isNewPage = true
	case existingErr != nil:
		return nil, false, existingErr
	case existing.SpaceId != spaceID:
		// The page id resolves to a page in another space (GetPageWithDeleted is not space-scoped).
		// The caller is only authorized for spaceID, so this id is not publishable here.
		return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.conflict.app_error",
			nil, "", http.StatusConflict)
	case existing.DeleteAt != 0:
		return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.page_deleted.app_error",
			nil, "", http.StatusConflict)
		// default: live page in this space → edit path
	}

	// 3. Parent guard (new-page path only): a new page's parent must be a published live page; a
	// draft-only or non-live parent returns 409. The edit path never reparents, so a stale ParentId
	// carried on the draft must not block a content-only edit-publish.
	if isNewPage && draft.ParentId != "" {
		// Existence-only probe, space-scoped: collapse "not a live page" and "lives in another space"
		// into one error so the response can't be used to probe page ids in spaces the caller cannot
		// read (matches validateDraftParent / validateParentExists).
		parentExists, parentErr := s.store.PageExistsInSpace(draft.ParentId, spaceID)
		if parentErr != nil {
			return nil, false, storeAppError("PublishPageDraft", parentErr)
		}
		if !parentExists {
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.parent_unpublished.app_error",
				nil, "", http.StatusConflict)
		}
	}

	// 4. Validate and normalise draft body for the page write.
	body, searchText, contentErr := normalizePageContent("PublishPageDraft", draft.Body)
	if contentErr != nil {
		return nil, false, contentErr
	}

	// 5. Build the *model.Page for the store call.
	var pageForWrite *model.Page
	if isNewPage {
		title, titleErr := validateTitle("PublishPageDraft", draft.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return nil, false, titleErr
		}
		// ChannelId is derived by the store from the space, matching CreatePage, so it is
		// intentionally left unset here.
		pageForWrite = &model.Page{
			Id:             pageID,
			SpaceId:        spaceID,
			ParentId:       draft.ParentId,
			Title:          title,
			Body:           body,
			SearchText:     searchText,
			UserId:         userID,
			LastModifiedBy: userID,
		}
	} else {
		// Edit path: require an optimistic-lock baseline unless force, so a client that never
		// captured the page's EditAt cannot silently overwrite a concurrent edit.
		baseEditAt, haveBaseline := draft.EditBaseline()
		if !force && !haveBaseline {
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.baseline_required.app_error",
				nil, "", http.StatusBadRequest)
		}
		// Carry only the fields the draft actually set; leave the rest empty. The store preserves the
		// live page's current value for any empty field, so an omitted field is never sourced from the
		// pre-lock `existing` snapshot — otherwise a force-publish could revert a concurrent edit to a
		// field this draft never touched.
		// An empty draft body means "unset" (a cleared document is EmptyTipTapJSON, not ""), so an
		// empty body leaves the live page's content intact rather than wiping it.
		pageForWrite = &model.Page{
			Id:             pageID,
			SpaceId:        spaceID,
			LastModifiedBy: userID,
		}
		if draft.Title != "" {
			title, titleErr := validateTitle("PublishPageDraft", draft.Title, model.PageTitleMaxRunes)
			if titleErr != nil {
				return nil, false, titleErr
			}
			pageForWrite.Title = title
		}
		if draft.Body != "" {
			pageForWrite.Body = body
			pageForWrite.SearchText = searchText
		}
		if haveBaseline {
			pageForWrite.EditAt = baseEditAt
		}

		// A draft that carries only an optimistic-lock baseline — no Title, no Body — has no page
		// content to write. Publishing it would bump EditAt and emit page_updated with no actual
		// change, invalidating other editors' baselines for nothing. Treat it as a discard instead:
		// delete the draft and return the page as-is.
		if pageForWrite.Title == "" && pageForWrite.Body == "" {
			deleted, delErr := s.store.DeleteDraftVersion(userID, pageID, draft.UpdateAt)
			if delErr != nil {
				return nil, false, storeAppError("PublishPageDraft", delErr)
			}
			if !deleted {
				// A concurrent autosave advanced the draft version between the read and the delete.
				// The newer draft may have real content — the client must re-read and re-publish.
				return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.draft_changed.app_error",
					nil, "", http.StatusConflict)
			}
			s.clearThrottleAndBroadcastPagePresence(pageID, userID, spaceID, existing.ChannelId)
			return existing, false, nil
		}
	}

	// 6. Atomic write: page + draft-delete in one transaction. draft.UpdateAt is passed through so a
	// concurrent autosave rolls this publish back as a conflict rather than shipping older content —
	// see store.PublishDraft.
	page, storeErr := s.store.PublishDraft(isNewPage, pageForWrite, userID, spaceID, force, MaxPageDepth, draft.UpdateAt)
	if storeErr != nil {
		switch {
		// The draft moved under this publish: the caller's own editor autosaved after this call read it,
		// so the whole write was rolled back rather than committing the older content. Distinct from the
		// conflicts below — the client republishes to ship the newer draft, it does not re-baseline.
		case store.ConflictReason(storeErr) == store.ReasonConcurrentAutosave:
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.draft_changed.app_error",
				nil, "", http.StatusConflict).Wrap(storeErr)

		// Someone else edited the page since the baseline was captured. The client must re-read the page
		// and publish against a fresh baseline (or force).
		case store.ConflictReason(storeErr) == store.ReasonConcurrentEdit:
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.edit_conflict.app_error",
				nil, "", http.StatusConflict).Wrap(storeErr)

		case store.IsErrConflict(storeErr):
			if isNewPage {
				// PK collision: a concurrent publish won this page id. Adopt the winner's page and
				// return 200 without broadcasting (the winner already broadcast wsEventPageCreated).
				// The winner must be a live page in this space — a different-space or already-deleted
				// winner is not this caller's to read, so fall through to a plain conflict.
				raced, rErr := s.GetPageWithDeleted(pageID)
				if rErr == nil && raced != nil && raced.SpaceId == spaceID && raced.DeleteAt == 0 {
					// Discard this caller's now-orphaned draft so it does not linger pointing at a
					// published page — but only if it still holds the version this publish read. A fresh
					// autosave landing after the race winner committed bumps UpdateAt, so the CAS matches
					// no row and that newer draft is left intact rather than silently dropped. Cleanup is
					// best-effort: the page is already published by the winner, so a failure here is logged
					// (a stray draft the user can discard), never surfaced as a publish failure.
					if _, delErr := s.store.DeleteDraftVersion(userID, pageID, draft.UpdateAt); delErr != nil {
						s.log.Warn("PublishPageDraft: failed to delete orphaned draft after adopting race winner",
							"page_id", pageID, "user_id", userID, "err", delErr)
					}
					// The draft is consumed; clear the rate-limit entry and broadcast presence so
					// the active-editors indicator drops this user, matching the non-conflict path.
					s.clearThrottleAndBroadcastPagePresence(pageID, userID, spaceID, raced.ChannelId)
					return raced, false, nil
				}
				if rErr != nil {
					// A real store failure here is not the same as losing the race; it would otherwise
					// be indistinguishable from it in the response, so leave a trace.
					s.log.Warn("PublishPageDraft: failed to read the page that won the publish race",
						"page_id", pageID, "user_id", userID, "err", rErr)
				}
			}
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.conflict.app_error",
				nil, "", http.StatusConflict).Wrap(storeErr)
		case store.IsErrNotFound(storeErr):
			// A concurrent delete removed the page or its parent between the pre-checks and the lock.
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.page_deleted.app_error",
				nil, "", http.StatusConflict).Wrap(storeErr)
		default:
			return nil, false, storeAppError("PublishPageDraft", storeErr)
		}
	}

	// 7. Broadcast the write with the same shape and channel scope as the direct-CRUD page events.
	// A publish-via-draft edit reuses page_updated — a client handles it identically to a direct PATCH.
	// page_created includes parent_id so clients can place the new node in the tree without a fetch.
	if isNewPage {
		s.publishToChannels(wsEventPageCreated, map[string]any{
			"page_id":   page.Id,
			"space_id":  page.SpaceId,
			"parent_id": page.ParentId,
		}, page.ChannelId)
	} else {
		s.publishToChannels(wsEventPageUpdated, map[string]any{
			"page_id":  page.Id,
			"space_id": page.SpaceId,
		}, page.ChannelId)
	}
	// The publish deleted the draft inside PublishDraft (bypassing the app-level DeletePageDraft
	// that normally broadcasts presence), so broadcast presence now so the active-editors indicator
	// clears on other clients. Delete the rate-limit entry first so the broadcast is not suppressed.
	s.clearThrottleAndBroadcastPagePresence(pageID, userID, spaceID, page.ChannelId)

	return page, isNewPage, nil
}
