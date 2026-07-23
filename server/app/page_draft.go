// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"maps"
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
// draft.PageId is the unified page id, allocated up front and stable across the draft → publish
// lifecycle. It is reserved before any published page row exists, so carrying a valid page id does
// not imply the page is published — a draft may exist first. The space must exist and be live. The
// caller owns the draft: draft.UserId is always sourced from the request, never the request body.
//
// An autosave may omit fields the editor didn't change; omitted fields are preserved, so concurrent
// heartbeats cannot clobber each other's changes.
// parentID encodes the write intent for ParentId: nil preserves the stored value, a pointer to ""
// clears to root, and a pointer to a valid ID sets the parent.
// props encodes the write intent for Props: nil preserves the stored map, a non-nil pointer replaces
// it wholesale (an empty map clears all keys); its serialized size is validated here.
func (s *Service) UpdatePageDraft(draft *model.Draft, parentID *string, fileIDs *mmmodel.StringArray, props *mmmodel.StringInterface, channelID string) (*model.Draft, *mmmodel.AppError) {
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
		sanitizedBody, contentErr := sanitizeContentBody("UpdatePageDraft", draft.Body)
		if contentErr != nil {
			return nil, contentErr
		}
		draft.Body = sanitizedBody
	}

	// Validate the written Props size here: props is passed to the store separately (pointer intent),
	// so it is not the draft.Props field IsValid checks — without this the PagePropsMaxBytes bound
	// would be silently bypassed on the write path. Mirrors the fileIDs size guard below.
	if props != nil {
		if propsErr := model.ValidatePropsSize("UpdatePageDraft", "page_id="+draft.PageId, *props, model.PagePropsMaxBytes); propsErr != nil {
			return nil, propsErr
		}
	}

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

	existingDraft, existingDraftErr := s.store.GetDraft(draft.UserId, draft.PageId)
	switch {
	case existingDraftErr != nil && !store.IsErrNotFound(existingDraftErr):
		return nil, storeAppError("UpdatePageDraft", existingDraftErr)
	case existingDraftErr == nil && existingDraft.SpaceId != draft.SpaceId:
		// Existing draft belongs to a different space: reject to prevent cross-space drift.
		return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.page_not_found.app_error", nil, "", http.StatusNotFound)
	case store.IsErrNotFound(existingDraftErr):
		// No draft for THIS user+page. Allow only if the page is already published/live in the
		// space — PageExistsInSpace checks DOCS_Page, not drafts. A page id reserved via
		// CreateSpaceDraft (the id is allocated, but no DOCS_Page row exists yet) has only a
		// DOCS_Draft row, so its author reaches this method through the "existing draft" branch
		// above (user-scoped GetDraft), never here.
		// This prevents PATCH /spaces/X/pages/<random-id>/draft from ghost-drafting a non-existent page.
		pageIsLive, existsErr := s.store.PageExistsInSpace(draft.PageId, draft.SpaceId)
		if existsErr != nil {
			return nil, storeAppError("UpdatePageDraft", existsErr)
		}
		if !pageIsLive {
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.page_not_found.app_error", nil, "", http.StatusNotFound)
		}
	}

	saved, savedPageWasLive, err := s.store.UpsertDraft(draft, parentID, fileIDs, props)
	if err != nil {
		switch store.ConflictReason(err) {
		case store.ReasonConcurrentEdit:
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.edit_conflict.app_error",
				nil, "", http.StatusConflict).Wrap(err)
		case store.ReasonConcurrentAutosave:
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.draft_changed.app_error",
				nil, "", http.StatusConflict).Wrap(err)
		}
		if store.InvalidInputReason(err) == store.ReasonPageNotLive {
			// The target page was deleted, snapshotted, or moved out of this space between the
			// pre-check above and the store's locked read. That is a concurrent state change, not
			// bad input, so mirror the 404 the pre-check returns rather than a generic 400.
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.page_not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		return nil, storeAppError("UpdatePageDraft", err)
	}

	// New-page drafts (no published page row yet) must not broadcast presence to the space channel:
	// that would expose the reserved page ID and the author's identity to all space members before
	// the page exists. Send the event only to the author so their own UI can track the session.
	// UpsertDraft determined liveness as part of the same call, so trust its result here.
	if !savedPageWasLive {
		s.publishSelfPresence(saved)
		return saved, nil
	}

	// Existing published page: rate-limited channel-wide broadcast so other viewers see this user
	// in the active-editors indicator. The rate-limit bucket is keyed per (page, user) so each editor
	// gets an independent limit — one editor's broadcast can't rate-limit another editor on the same page.
	presenceKey := presenceBroadcastKey(saved.PageId, saved.UserId)
	now := mmmodel.GetMillis()
	s.sweepPresenceBroadcastTimes(now)
	existing, loaded := s.presenceBroadcastTimes.LoadOrStore(presenceKey, now)
	if loaded {
		lastTime, ok := existing.(int64)
		if !ok || now-lastTime < presenceBroadcastMinIntervalMs {
			return saved, nil
		}
		if !s.presenceBroadcastTimes.CompareAndSwap(presenceKey, existing, now) {
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
	saved, _, err := s.store.UpsertDraft(draft, parentPtr, nil, nil)
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

	// Discard is unconditional. Autosave and discard are separate HTTP requests in separate
	// transactions, so an autosave request the same user dispatched just before the discard can
	// still be in flight when the discard commits and then re-insert (resurrect) this draft — the
	// server does not guarantee the autosave commits before the later-issued delete. An unpublished
	// new-page draft has no page row for UpsertDraft's staleness guard to key on, so the guard
	// cannot tell that a discard happened (a published page is protected; only this case is not).
	// The resulting zombie draft is harmless — it is visible only to its owning user (drafts are
	// keyed by (UserId, PageId)), and that user removes it by discarding again — so a deletion
	// tombstone to permanently block re-insertion is not warranted.
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

	// The store already excludes drafts in a soft-deleted space, so this need not re-check liveness.
	offset, limit := paginationOffsetLimit(page, perPage)
	drafts, err := s.store.GetDraftsForSpace(userID, spaceID, offset, limit)
	if err != nil {
		return nil, false, storeAppError("GetPageDraftsForSpace", err)
	}
	drafts, hasMore := trimPage(drafts, limit)
	return drafts, hasMore, nil
}

// PublishPageDraft publishes the calling user's draft for pageID in spaceID as a page, creating the
// page on first publish or updating it if it already exists. pageID is the id reserved when editing
// began (see CreateSpaceDraft) and is stable across the draft → publish lifecycle, so its presence
// does not imply a published page yet: whether this is a create or an edit is re-derived from the
// database (no client trust). The draft is validated, and the page write + draft delete are
// committed in a single store transaction.
// Returns (page, wasCreated, appErr):
//   - wasCreated=true → a new page was inserted by this call (handler should return 201)
//   - wasCreated=false → an existing page was updated, or a concurrent create was adopted (return 200)
//   - appErr is a 409 edit conflict → page is the current server page (or nil if the re-read failed),
//     so the caller can surface a diff without a follow-up read; on every other error page is nil.
func (s *Service) PublishPageDraft(userID, spaceID, pageID string, force bool) (*model.Page, bool, *mmmodel.AppError) {
	if !mmmodel.IsValidId(userID) {
		return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(pageID) {
		return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.invalid_page_id.app_error", nil, "", http.StatusBadRequest)
	}
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

	// 4/5. Validate & normalise the draft body and build the *model.Page for the store call
	// (new-page vs edit-path field rules; see helper).
	pageForWrite, buildErr := s.buildPageForPublish(isNewPage, pageID, spaceID, userID, draft, force)
	if buildErr != nil {
		return nil, false, buildErr
	}

	// A "baseline-only" edit draft carries an optimistic-lock baseline but no populated field, so
	// there is no page change to write; discard it rather than bumping EditAt for nothing (see helper).
	if !isNewPage && pageForWrite.Title == "" && pageForWrite.Body == "" && len(pageForWrite.Props) == 0 {
		return s.discardBaselineOnlyDraft(userID, pageID, spaceID, existing, draft.UpdateAt)
	}

	// 6. Atomic write: page + draft-delete in one transaction. draft.UpdateAt is passed through so a
	// concurrent autosave rolls this publish back as a conflict rather than shipping older content —
	// see store.PublishDraft.
	page, storeErr := s.store.PublishDraft(isNewPage, pageForWrite, userID, spaceID, force, model.MaxPageDepth, draft.UpdateAt)
	if storeErr != nil {
		switch {
		// The draft moved under this publish: the caller's own editor autosaved after this call read it,
		// so the whole write was rolled back rather than committing the older content. Distinct from the
		// conflicts below — the client republishes to ship the newer draft, it does not re-baseline.
		case store.ConflictReason(storeErr) == store.ReasonConcurrentAutosave:
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.draft_changed.app_error",
				nil, "", http.StatusConflict).Wrap(storeErr)

		// Someone else edited the page since the baseline was captured. The client must re-read the page
		// and publish against a fresh baseline (or force). Return the current server page alongside the
		// conflict so the client can diff and re-baseline in one round-trip rather than a follow-up GET.
		// The pre-lock `existing` snapshot is stale by definition here, so re-read the live page.
		case store.ConflictReason(storeErr) == store.ReasonConcurrentEdit:
			editConflictErr := mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.edit_conflict.app_error",
				nil, "", http.StatusConflict).Wrap(storeErr)
			current, getErr := s.GetPage(pageID)
			if getErr != nil {
				// A concurrent delete can remove the page between the conflict and this re-read; fall
				// back to a bare conflict and let the client GET the page itself.
				s.log.Warn("failed to re-read page for edit-conflict body",
					"page_id", pageID, "user_id", userID, "err", getErr)
				return nil, false, editConflictErr
			}
			return current, false, editConflictErr

		case store.IsErrConflict(storeErr):
			// PK collision on the new-page path: a concurrent publish won this page id. Adopt the
			// winner and return 200 without broadcasting; a winner that is not this caller's to read
			// falls through to a plain conflict (see adoptPublishRaceWinner).
			if isNewPage {
				if raced, adopted := s.adoptPublishRaceWinner(userID, pageID, spaceID, draft.UpdateAt); adopted {
					return raced, false, nil
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

// buildPageForPublish normalises the draft body and constructs the *model.Page passed to
// store.PublishDraft. Props follow the same write intent as PagePatch.Props: a new page adopts the
// draft's props outright, while an edit replaces the live page's props only when the draft carries a
// non-empty map and preserves them otherwise. (A bare Draft.Props map cannot express "clear to empty"
// distinctly from "unset", so an empty draft map means preserve, consistent with how Title/Body are
// carried below.) No client sets page props through drafts today; this keeps the publish path ready
// for when one does. The caller detects a baseline-only edit (every field empty) after the build.
func (s *Service) buildPageForPublish(isNewPage bool, pageID, spaceID, userID string, draft *model.Draft, force bool) (*model.Page, *mmmodel.AppError) {
	body, searchText, contentErr := normalizePageContent("PublishPageDraft", draft.Body)
	if contentErr != nil {
		return nil, contentErr
	}

	if isNewPage {
		title, titleErr := validateTitle("PublishPageDraft", draft.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return nil, titleErr
		}
		// ChannelId is derived by the store from the space, matching CreatePage, so it is
		// intentionally left unset here.
		return &model.Page{
			Id:             pageID,
			SpaceId:        spaceID,
			ParentId:       draft.ParentId,
			Title:          title,
			Body:           body,
			SearchText:     searchText,
			Props:          maps.Clone(draft.Props),
			UserId:         userID,
			LastModifiedBy: userID,
		}, nil
	}

	// Edit path: require an optimistic-lock baseline unless force, so a client that never
	// captured the page's EditAt cannot silently overwrite a concurrent edit.
	baseEditAt := draft.BaseEditAt
	haveBaseline := baseEditAt != 0
	if !force && !haveBaseline {
		return nil, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.baseline_required.app_error",
			nil, "", http.StatusBadRequest)
	}
	// Build pageForWrite with only the fields this draft changed; leave every other field at its
	// zero value. The store treats a zero/empty field as "keep the live page's current value" and
	// writes just the non-empty ones. Omitted fields are deliberately NOT copied from the pre-lock
	// `existing` snapshot: that snapshot can be stale, so on a force-publish copying it back would
	// overwrite a field this draft never touched and revert a concurrent edit to it.
	// Body uses "" as its unset marker — a document the user cleared is stored as EmptyTipTapJSON,
	// never "" — so treating an empty ("") body as unset preserves the live content instead of wiping it.
	pageForWrite := &model.Page{
		Id:             pageID,
		SpaceId:        spaceID,
		LastModifiedBy: userID,
	}
	if draft.Title != "" {
		title, titleErr := validateTitle("PublishPageDraft", draft.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return nil, titleErr
		}
		pageForWrite.Title = title
	}
	if draft.Body != "" {
		pageForWrite.Body = body
		pageForWrite.SearchText = searchText
	}
	if len(draft.Props) > 0 {
		// No clone here: the store's edit path merges via model.Page.Patch, which clones Props
		// itself. (The new-page path above clones because it inserts pageForWrite directly.)
		pageForWrite.Props = draft.Props
	}
	if haveBaseline {
		pageForWrite.EditAt = baseEditAt
	}
	return pageForWrite, nil
}

// discardBaselineOnlyDraft handles an edit-path publish whose draft carries an optimistic-lock
// baseline but no content in any field — Title, Body, and Props were never populated (all empty).
// This is NOT a user who cleared the document: a cleared doc is EmptyTipTapJSON, a non-empty Body
// that publishes normally; empty here means "never sent". With every field empty there is no page
// change to write. Publishing would still bump EditAt and emit page_updated with no actual change,
// invalidating other editors' baselines for nothing. So delete the draft and return the page as-is.
func (s *Service) discardBaselineOnlyDraft(userID, pageID, spaceID string, existing *model.Page, draftUpdateAt int64) (*model.Page, bool, *mmmodel.AppError) {
	deleted, delErr := s.store.DeleteDraftVersion(userID, pageID, draftUpdateAt)
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

// adoptPublishRaceWinner handles the PK-collision case on the new-page publish path: a concurrent
// publish already created this page id. It returns the winner and true when this caller should adopt
// it (return 200 without broadcasting — the winner already broadcast wsEventPageCreated). The winner
// must be a live page in this space; a different-space or already-deleted winner is not this caller's
// to read, so it returns (nil, false) and the caller surfaces a plain conflict.
func (s *Service) adoptPublishRaceWinner(userID, pageID, spaceID string, draftUpdateAt int64) (*model.Page, bool) {
	raced, rErr := s.GetPageWithDeleted(pageID)
	if rErr == nil && raced != nil && raced.SpaceId == spaceID && raced.DeleteAt == 0 {
		// Discard this caller's now-orphaned draft so it does not linger pointing at a
		// published page — but only if it still holds the version this publish read. A fresh
		// autosave after the race winner committed bumps UpdateAt, so the CAS matches no row
		// and that newer draft is left intact rather than dropped.
		//
		// Best-effort: the page is already published by the winner, so a failure here is
		// logged (a stray draft the user can discard), never surfaced as a publish failure.
		if _, delErr := s.store.DeleteDraftVersion(userID, pageID, draftUpdateAt); delErr != nil {
			s.log.Warn("failed to delete orphaned draft after adopting race winner",
				"page_id", pageID, "user_id", userID, "err", delErr)
		}
		// The draft is consumed; clear the rate-limit entry and broadcast presence so
		// the active-editors indicator drops this user, matching the non-conflict path.
		s.clearThrottleAndBroadcastPagePresence(pageID, userID, spaceID, raced.ChannelId)
		return raced, true
	}
	if rErr != nil {
		// Log this: a real store failure here would otherwise look identical to losing the race.
		s.log.Warn("failed to read the page that won the publish race",
			"page_id", pageID, "user_id", userID, "err", rErr)
	}
	return nil, false
}
