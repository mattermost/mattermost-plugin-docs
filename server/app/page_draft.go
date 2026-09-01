// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"maps"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// UpdatePageDraft upserts the calling user's autosave draft for a page in a space. channelID is
// the space's backing channel, used to scope the presence broadcast.
//
// draft.PageId is the unified page id, allocated up front and stable across the draft → publish
// lifecycle. It is reserved before any published page row exists, so carrying a valid page id does
// not imply the page is published — a draft may exist first. The space must exist and be live. The
// caller owns the draft: draft.UserId is always sourced from the request, never the request body.
//
// An autosave may omit fields the editor didn't change, and an omitted field keeps its stored
// value. parentID, fileIDs, and props signal omission with a nil pointer; the draft struct's own
// fields signal it by being empty.
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

	// Normalize the draft body on the same content path as publish, so a stored draft never holds
	// unsanitized markup (normalization parses the body through the TipTap sanitizer). Defense-in-depth:
	// only the author can read a draft back today, but any future reader of Draft.Body inherits a
	// sanitized value.
	if draft.Body != "" {
		normalizedBody, contentErr := normalizeContentBody("UpdatePageDraft", draft.Body)
		if contentErr != nil {
			return nil, contentErr
		}
		draft.Body = normalizedBody
	}

	// AutosaveDraft enforces the autosave-path guards itself (see its godoc), so no separate
	// pre-check reads are needed here.
	saved, savedPageWasLive, err := s.store.AutosaveDraft(draft, parentID, fileIDs, props)
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
			// The page is not addressable in this space — deleted, snapshotted, moved away, held as
			// a draft in another space, or never reserved. All of these read as "no such page here",
			// so report 404 rather than a generic 400.
			return nil, mmmodel.NewAppError("UpdatePageDraft", "app.page_draft.update.page_not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		return nil, storeAppError("UpdatePageDraft", err)
	}

	// AutosaveDraft determined page liveness as part of the same call, so trust its result here.
	s.maybeBroadcastDraftPresence(savedPageWasLive, saved.PageId, saved.UserId, saved.SpaceId, channelID)

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
	// GetDraftSpaceID probes existence (space-checked below) without hauling the draft body.
	draftSpaceID, draftErr := s.store.GetDraftSpaceID(userID, parentID)
	if draftErr == nil && draftSpaceID == spaceID {
		return nil
	}
	if draftErr != nil && !store.IsErrNotFound(draftErr) {
		return storeAppError("validateDraftParent", draftErr)
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

// DeletePageDraft removes the calling user's draft for the given page — the discard path. (A
// publish deletes the draft inside the store publish transaction without calling here.)
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

	// Discard is unconditional. Autosave and discard are separate HTTP requests in separate
	// transactions, so an autosave request the same user dispatched just before the discard can
	// still be in flight when the discard commits and then re-insert (resurrect) this draft — the
	// server does not guarantee the autosave commits before the later-issued delete.
	//
	// UpsertDraft's staleness guard cannot catch this case: an unpublished new-page draft has no
	// page row for the guard to key on (a published page is protected; only this case is not).
	//
	// The resulting zombie draft is harmless — it is visible only to its owning user (drafts are
	// keyed by (UserId, PageId)), and that user removes it by discarding again — so a deletion
	// tombstone to permanently block re-insertion is not warranted.
	// A draft is keyed by (UserId, PageId) without SpaceId; DeleteDraftReparenting scopes its
	// delete by (UserId, SpaceId, PageId) and requires a live space, so a member of another space
	// cannot delete a draft here by passing this space's id with a foreign page id — no separate
	// pre-check read is needed.
	pageWasLive, delErr := s.store.DeleteDraftReparenting(userID, spaceID, pageID)
	if delErr != nil {
		// No matching draft — never existed, wrong space, dead space, or removed by a concurrent
		// publish/delete. All read as "nothing to discard", so report 404 rather than a 500 that
		// would also emit a spurious server-side error log.
		if store.IsErrNotFound(delErr) {
			return mmmodel.NewAppError("DeletePageDraft", "app.page_draft.delete.not_found.app_error", nil, "", http.StatusNotFound).Wrap(delErr)
		}
		return storeAppError("DeletePageDraft", delErr)
	}

	s.endDraftPresenceSession(pageWasLive, pageID, userID, spaceID, channelID)

	return nil
}

// GetPageDraftsForSpace lists the user's unpublished drafts in a space, most-recently-updated
// first, one pagination page at a time; the second return reports whether further pages exist.
// Draft bodies are omitted from the listing.
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
// database (no client trust). The draft is validated, and the page write and the draft's removal
// either both take effect or neither does.
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
	isNewPage, targetErr := derivePublishTarget(existing, existingErr, spaceID)
	if targetErr != nil {
		return nil, false, targetErr
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

	// 4/5/6. Validate & normalise the draft, build the store write (new-page insert vs edit patch;
	// see helpers), and hand it to the store. draft.UpdateAt is passed through so a concurrent
	// autosave rolls this publish back as a conflict rather than shipping older content — see
	// store.deletePublishedDraftTx.
	var page *model.Page
	var storeErr error
	// A publish that carried no body cannot have moved an anchor, so it skips the orphan sweep.
	bodyPublished := false
	if isNewPage {
		pageForWrite, buildErr := s.buildNewPageForPublish(pageID, spaceID, userID, draft)
		if buildErr != nil {
			return nil, false, buildErr
		}
		page, storeErr = s.store.PublishNewPageDraft(pageForWrite, userID, spaceID, model.MaxPageDepth, draft.UpdateAt)
	} else {
		patch, buildErr := s.buildEditPatchForPublish(draft, force)
		if buildErr != nil {
			return nil, false, buildErr
		}
		// A "baseline-only" edit draft carries an optimistic-lock baseline but no populated field,
		// so there is no page change to write; discard it rather than bumping EditAt for nothing
		// (see helper).
		if patch.Title == nil && patch.Body == nil && patch.Props == nil {
			return s.discardBaselineOnlyDraft(userID, pageID, spaceID, draft.UpdateAt)
		}
		bodyPublished = patch.Body != nil
		page, storeErr = s.store.PublishPageEditDraft(pageID, spaceID, patch, draft.BaseEditAt, force, userID, draft.UpdateAt)
	}
	if storeErr != nil {
		return s.translatePublishStoreError(storeErr, isNewPage, userID, spaceID, pageID, draft.UpdateAt)
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
		s.sweepOrphanedComments(page, bodyPublished)
		s.publishToChannels(wsEventPageUpdated, map[string]any{
			"page_id":  page.Id,
			"space_id": page.SpaceId,
		}, page.ChannelId)
	}
	// The publish deleted the draft inside the store publish transaction (bypassing the app-level DeletePageDraft
	// that normally broadcasts presence), so end the presence session now so the active-editors
	// indicator clears on other clients. The page is live by definition at this point.
	s.endDraftPresenceSession(true, pageID, userID, spaceID, page.ChannelId)

	return page, isNewPage, nil
}

// translatePublishStoreError maps a failed publish write to the caller-facing result, so
// PublishPageDraft stays a sequence of steps and every publish-failure condition lands in one place.
// It returns the same triple as PublishPageDraft: the adopted page with a nil error when a concurrent
// publish already created this page, the current server page alongside a 409 on an edit conflict, and
// a nil page otherwise.
func (s *Service) translatePublishStoreError(storeErr error, isNewPage bool, userID, spaceID, pageID string, draftUpdateAt int64) (*model.Page, bool, *mmmodel.AppError) {
	switch {
	// The draft moved under this publish: the caller's own editor autosaved after this call read it,
	// so the whole write was rolled back rather than committing the older content. Distinct from the
	// conflicts below — the client republishes to ship the newer draft, it does not re-baseline.
	case store.ConflictReason(storeErr) == store.ReasonConcurrentAutosave:
		return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.draft_changed.app_error",
			nil, "", http.StatusConflict).Wrap(storeErr)

	// Someone else edited the page since the baseline was captured. The draft's baseline is
	// write-once (see store.UpsertDraft), so the client cannot re-baseline the existing draft:
	// it recovers by publishing with force, or by discarding the draft and reopening the edit
	// session against the current page. Return the current server page alongside the conflict so
	// the client can diff and choose in one round-trip rather than a follow-up GET.
	// The pre-lock page snapshot is stale by definition here, so re-read the live page.
	case store.ConflictReason(storeErr) == store.ReasonConcurrentEdit:
		editConflictErr := mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.edit_conflict.app_error",
			nil, "", http.StatusConflict).Wrap(storeErr)
		current, getErr := s.GetPageInSpace("PublishPageDraft", pageID, spaceID, false)
		if getErr != nil {
			// A concurrent delete or cross-space move can make the page unreadable here; fall
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
			if raced, adopted := s.adoptPublishRaceWinner(userID, pageID, spaceID, draftUpdateAt); adopted {
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

// derivePublishTarget classifies a publish target from the GetPageWithDeleted read: no page means
// a new-page publish, a live page in the caller's space means an edit-publish. A page in another
// space reports 404 rather than confirming the id exists elsewhere. A deleted page reports 409:
// the draft outlived its page and cannot be published.
func derivePublishTarget(existing *model.Page, existingErr *mmmodel.AppError, spaceID string) (isNewPage bool, appErr *mmmodel.AppError) {
	// The cross-space and deleted cases below are reachable only when the page moved or was deleted
	// between the draft read and this classification — the draft read's liveness filter excludes
	// drafts in either steady state.
	switch {
	case existingErr != nil && existingErr.StatusCode == http.StatusNotFound:
		return true, nil
	case existingErr != nil:
		return false, existingErr
	case existing == nil:
		// GetPageWithDeleted never returns (nil, nil) today; classify it like not-found rather
		// than dereferencing nil, matching the guard adoptPublishRaceWinner applies to the same read.
		return true, nil
	case existing.SpaceId != spaceID:
		// GetPageWithDeleted is not space-scoped and the caller is only authorized for spaceID, so
		// collapse to the same 404 the space-scoped reads return.
		return false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.page_not_found.app_error",
			nil, "", http.StatusNotFound)
	case existing.DeleteAt != 0:
		return false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.page_deleted.app_error",
			nil, "", http.StatusConflict)
	default: // live page in this space → edit path
		return false, nil
	}
}

// buildNewPageForPublish normalises the draft body and constructs the *model.Page inserted by
// store.PublishNewPageDraft. The page adopts the draft's title, body, and props outright.
func (s *Service) buildNewPageForPublish(pageID, spaceID, userID string, draft *model.Draft) (*model.Page, *mmmodel.AppError) {
	body, searchText, contentErr := normalizePageContent("PublishPageDraft", draft.Body)
	if contentErr != nil {
		return nil, contentErr
	}
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

// buildEditPatchForPublish normalises the draft body and translates the draft into the
// *model.PagePatch applied by store.PublishPageEditDraft, carrying only the fields this draft
// changed. A Draft has no per-field presence markers, so empty is its unset marker: an omitted
// field stays nil in the patch and keeps the live page's current value, which means a partial
// draft never wipes an untouched field and a force-publish cannot revert a concurrent edit to a
// field this draft did not change. Body's "" reading is safe because a document the user cleared
// is stored as EmptyTipTapJSON, never "". Props follow the same rule: a bare map cannot express
// "clear to empty" distinctly from "unset", so an empty draft map means preserve.
// The caller detects a baseline-only draft (every patch field nil) after the build.
func (s *Service) buildEditPatchForPublish(draft *model.Draft, force bool) (*model.PagePatch, *mmmodel.AppError) {
	body, searchText, contentErr := normalizePageContent("PublishPageDraft", draft.Body)
	if contentErr != nil {
		return nil, contentErr
	}
	// Require an optimistic-lock baseline unless force, so a client that never captured the
	// page's EditAt cannot silently overwrite a concurrent edit.
	if !force && draft.BaseEditAt == 0 {
		return nil, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.baseline_required.app_error",
			nil, "", http.StatusBadRequest)
	}
	patch := &model.PagePatch{}
	if draft.Title != "" {
		title, titleErr := validateTitle("PublishPageDraft", draft.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return nil, titleErr
		}
		patch.Title = &title
	}
	if draft.Body != "" {
		patch.Body = &body
		patch.SearchText = &searchText
	}
	if len(draft.Props) > 0 {
		// No clone here: the store merges via model.Page.Patch, which clones Props itself.
		patch.Props = &draft.Props
	}
	return patch, nil
}

// discardBaselineOnlyDraft handles an edit-path publish whose draft carries an optimistic-lock
// baseline but no content in any field — Title, Body, and Props were never populated (all empty).
// This is NOT a user who cleared the document: a cleared doc is EmptyTipTapJSON, a non-empty Body
// that publishes normally; empty here means "never sent". With every field empty there is no page
// change to write. Publishing would still bump EditAt and emit page_updated with no actual change,
// invalidating other editors' baselines for nothing. So delete the draft and return the page from a
// fresh read — not the caller's pre-lock snapshot, which a concurrent edit may have outdated.
func (s *Service) discardBaselineOnlyDraft(userID, pageID, spaceID string, draftUpdateAt int64) (*model.Page, bool, *mmmodel.AppError) {
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
	current, getErr := s.GetPageInSpace("PublishPageDraft", pageID, spaceID, false)
	if getErr != nil {
		// The draft is already discarded, so this publish's mutation succeeded; only the re-read
		// failed. Mirror the edit-conflict branch: log and translate rather than forwarding the
		// re-read error as a publish failure. Clear the rate-limit entry so a later session starts
		// unthrottled — with no readable page there is no channel to broadcast the session end to.
		s.log.Warn("failed to re-read page after discarding baseline-only draft",
			"page_id", pageID, "user_id", userID, "err", getErr)
		s.clearPresenceThrottle(pageID, userID)
		if getErr.StatusCode == http.StatusNotFound {
			// A concurrent delete or cross-space move made the page unreadable: the draft outlived
			// its page, which is the defined page_deleted conflict.
			return nil, false, mmmodel.NewAppError("PublishPageDraft", "app.page_draft.publish.page_deleted.app_error",
				nil, "", http.StatusConflict).Wrap(getErr)
		}
		return nil, false, getErr
	}
	s.endDraftPresenceSession(true, pageID, userID, spaceID, current.ChannelId)
	return current, false, nil
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
		// The draft is consumed; end the presence session so the active-editors indicator drops
		// this user, matching the non-conflict path. The adopted winner is live by definition.
		s.endDraftPresenceSession(true, pageID, userID, spaceID, raced.ChannelId)
		return raced, true
	}
	if rErr != nil {
		// Log this: a real store failure here would otherwise look identical to losing the race.
		s.log.Warn("failed to read the page that won the publish race",
			"page_id", pageID, "user_id", userID, "err", rErr)
	}
	return nil, false
}
