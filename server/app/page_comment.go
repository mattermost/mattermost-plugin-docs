// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"maps"
	"net/http"

	"github.com/jmoiron/sqlx"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// A page comment is a core Posts row of type custom_page_comment in the space's backing channel,
// tied to its page by the page_id prop — not a plugin-table row. Reads are direct SQL through
// page_comment_store.go; writes go through the plugin API's post methods so the platform pipeline
// (threading, cascade, notifications) stays authoritative.
//
// Every write runs inside store.WithPageCommentLock: the page row lock serializes the write
// against a concurrent MovePageToSpace, so the space the caller was authorized against is the
// space the write lands in — a post-write repair could not retract the notification fan-out that
// CreatePost performs before returning. The advisory root lock serializes reply-creates against
// root deletes, so a reply cannot commit under a root whose cascade already ran.
//
// A plugin-API post write can fail after its store mutation has committed (e.g. CreatePost's
// pending-post-id cache write), and the returned error carries no committed flag — so each write
// path decides what actually happened by reading its own committed state back by id, on the lock's
// transaction, never by inspecting the error. That verdict decides whether the handler's
// page_comment_* event is published: the platform's own post events never reach Docs clients, so
// the plugin event is the only signal a comment changed. The request still returns its error
// either way: the caller must see the failure while the rest of the space converges on the state
// that exists.
//
// Space membership is the only authorization gate wired today, enforced by the handlers via
// requireSpaceMembership like every other Docs route; the per-permission gates (read_page,
// comment_page, delete_page_comment) land with the RBAC epic.

// PageCommentCursor is the decoded keyset boundary of the roots listing: the (CreateAt, Id) pair
// of the last row the client saw. The pair is a pure value comparison in SQL, so a cursor naming
// a row that has since been deleted still resumes from the nearest greater row.
type PageCommentCursor struct {
	CreateAt int64
	Id       string
}

// pageCommentEventPayload is the ids-only payload all three page_comment_* events carry.
func pageCommentEventPayload(commentID, rootID, pageID, spaceID string) map[string]any {
	return map[string]any{
		"id":       commentID,
		"root_id":  rootID,
		"page_id":  pageID,
		"space_id": spaceID,
	}
}

// requirePageCommentArgs validates the identifiers every comment operation starts from.
func requirePageCommentArgs(where string, space *model.Space, pageID string) *mmmodel.AppError {
	if space == nil {
		return mmmodel.NewAppError(where, "app.page.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(pageID) {
		return mmmodel.NewAppError(where, "app.page_comment.invalid_page_id.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// requirePageInSpace 404s unless pageID is a live page of space. Existence-only probe: comment
// reads never need the page body, and a missing page and a wrong-space page read identically so
// the error cannot be used to probe page ids in other spaces.
func (s *Service) requirePageInSpace(where string, space *model.Space, pageID string) *mmmodel.AppError {
	exists, existsErr := s.store.PageExistsInSpace(pageID, space.Id)
	if existsErr != nil {
		return storeAppError(where, existsErr)
	}
	if !exists {
		return mmmodel.NewAppError(where, "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return nil
}

// requireLockedPageInSpace re-verifies, inside the page lock, that the page still sits in the
// space the caller was authorized against. Taking the lock only makes the read authoritative; it
// does not make it match — a move that committed between the gate and the lock changes SpaceId,
// and acting then would mutate (or publish into) a space the request was never authorized
// against.
func requireLockedPageInSpace(where string, locked store.LockedPage, space *model.Space) *mmmodel.AppError {
	if locked.SpaceId != space.Id {
		return mmmodel.NewAppError(where, "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}
	return nil
}

// resolvePageComment resolves commentID through the full identity predicate (id, backing channel,
// page) and maps a miss to 404 — deliberately not 403, so a gate pass cannot probe existence in
// other spaces.
func (s *Service) resolvePageComment(where string, space *model.Space, pageID, commentID string, includeDeleted bool) (*mmmodel.Post, *mmmodel.AppError) {
	post, err := s.store.GetPageComment(commentID, space.ChannelId, pageID, includeDeleted)
	if err != nil {
		if store.IsErrNotFound(err) {
			return nil, mmmodel.NewAppError(where, "app.page_comment.not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		return nil, storeAppError(where, err)
	}
	return post, nil
}

// resolvePageCommentTx is resolvePageComment on the lock's transaction.
func (s *Service) resolvePageCommentTx(where string, tx *sqlx.Tx, locked store.LockedPage, pageID, commentID string, includeDeleted bool) (*mmmodel.Post, *mmmodel.AppError) {
	post, err := s.store.GetPageCommentTx(tx, commentID, locked.ChannelId, pageID, includeDeleted)
	if err != nil {
		if store.IsErrNotFound(err) {
			return nil, mmmodel.NewAppError(where, "app.page_comment.not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
		}
		return nil, storeAppError(where, err)
	}
	return post, nil
}

// GetPageComments returns one cursor window of a page's root comments. resolved nil returns all
// roots; commentType "" both kinds. The filters are applied in SQL, never client-side, so hasMore
// is computed over the same filtered set the window renders. nextAfter is non-nil exactly when
// hasMore.
func (s *Service) GetPageComments(space *model.Space, pageID string, resolved *bool, commentType string, after *PageCommentCursor, perPage int) ([]*model.PageComment, *PageCommentCursor, bool, *mmmodel.AppError) {
	if appErr := requirePageCommentArgs("GetPageComments", space, pageID); appErr != nil {
		return nil, nil, false, appErr
	}
	if appErr := s.requirePageInSpace("GetPageComments", space, pageID); appErr != nil {
		return nil, nil, false, appErr
	}

	opts := store.PageCommentListOptions{
		Resolved:    resolved,
		CommentType: commentType,
		Limit:       ClampPerPage(perPage) + 1,
	}
	if after != nil {
		opts.AfterCreateAt, opts.AfterID = after.CreateAt, after.Id
	}
	posts, listErr := s.store.GetPageCommentRoots(space.ChannelId, pageID, opts)
	if listErr != nil {
		return nil, nil, false, storeAppError("GetPageComments", listErr)
	}
	posts, hasMore := trimPage(posts, opts.Limit)

	rootIDs := make([]string, len(posts))
	for i, post := range posts {
		rootIDs[i] = post.Id
	}
	counts, countErr := s.store.GetPageCommentReplyCounts(rootIDs)
	if countErr != nil {
		return nil, nil, false, storeAppError("GetPageComments", countErr)
	}

	comments := make([]*model.PageComment, len(posts))
	for i, post := range posts {
		comments[i] = model.NewPageCommentFromPost(post, nil, space.Id, counts[post.Id])
	}
	var nextAfter *PageCommentCursor
	if hasMore && len(posts) > 0 {
		last := posts[len(posts)-1]
		nextAfter = &PageCommentCursor{CreateAt: last.CreateAt, Id: last.Id}
	}
	return comments, nextAfter, hasMore, nil
}

// GetPageComment returns one comment, root or reply — the deep-link target, needed because a
// comment beyond the first listing page is otherwise unloadable (core's post permalink 404s on a
// space channel). A reply inherits its thread's comment_type/anchor_id from the root, so the root
// is read alongside it; a missing or soft-deleted root means the thread is gone and the reply
// reads as not-found rather than projecting against a nil root.
func (s *Service) GetPageComment(space *model.Space, pageID, commentID string) (*model.PageComment, *mmmodel.AppError) {
	if appErr := requirePageCommentArgs("GetPageComment", space, pageID); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requirePageInSpace("GetPageComment", space, pageID); appErr != nil {
		return nil, appErr
	}
	post, appErr := s.resolvePageComment("GetPageComment", space, pageID, commentID, false)
	if appErr != nil {
		return nil, appErr
	}

	if post.RootId != "" {
		root, rootErr := s.resolvePageComment("GetPageComment", space, pageID, post.RootId, false)
		if rootErr != nil {
			return nil, rootErr
		}
		// A reply provably has no replies of its own (sub-replies are rejected), so 0 is a fact,
		// not a shortcut.
		return model.NewPageCommentFromPost(post, root, space.Id, 0), nil
	}

	counts, countErr := s.store.GetPageCommentReplyCounts([]string{post.Id})
	if countErr != nil {
		return nil, storeAppError("GetPageComment", countErr)
	}
	return model.NewPageCommentFromPost(post, nil, space.Id, counts[post.Id]), nil
}

// GetPageCommentReplies returns one offset page of a root comment's replies. A rootID that is
// itself a reply is rejected 400: sub-replies are impossible, so such a request could only return
// an empty page, indistinguishable on the wire from a root with no replies.
func (s *Service) GetPageCommentReplies(space *model.Space, pageID, rootID string, page, perPage int) ([]*model.PageComment, bool, *mmmodel.AppError) {
	if appErr := requirePageCommentArgs("GetPageCommentReplies", space, pageID); appErr != nil {
		return nil, false, appErr
	}
	if appErr := s.requirePageInSpace("GetPageCommentReplies", space, pageID); appErr != nil {
		return nil, false, appErr
	}
	root, appErr := s.resolvePageComment("GetPageCommentReplies", space, pageID, rootID, false)
	if appErr != nil {
		return nil, false, appErr
	}
	if root.RootId != "" {
		return nil, false, mmmodel.NewAppError("GetPageCommentReplies", "app.page_comment.get_replies.target_is_reply.app_error", nil, "", http.StatusBadRequest)
	}

	offset, limit := paginationOffsetLimit(page, perPage)
	posts, listErr := s.store.GetPageCommentReplies(space.ChannelId, pageID, rootID, offset, limit)
	if listErr != nil {
		return nil, false, storeAppError("GetPageCommentReplies", listErr)
	}
	posts, hasMore := trimPage(posts, limit)

	comments := make([]*model.PageComment, len(posts))
	for i, post := range posts {
		comments[i] = model.NewPageCommentFromPost(post, root, space.Id, 0)
	}
	return comments, hasMore, nil
}

// CreatePageComment creates a root comment (footer or inline) on the page.
func (s *Service) CreatePageComment(space *model.Space, pageID string, create *model.PageCommentCreate, userID string) (*model.PageComment, *mmmodel.AppError) {
	if appErr := s.validateCommentWrite("CreatePageComment", space, pageID, userID); appErr != nil {
		return nil, appErr
	}
	create.Normalize()
	if appErr := create.IsValid(); appErr != nil {
		return nil, appErr
	}

	props := mmmodel.StringInterface{model.PropKeyPageId: pageID}
	if create.IsInline() {
		props[model.PropKeyCommentType] = model.CommentTypeInline
		props[model.PropKeyAnchorId] = create.AnchorId
	}

	s.log.Debug("Creating page comment", "page_id", pageID, "space_id", space.Id, "user_id", userID)

	var created *mmmodel.Post
	lockErr := s.store.WithPageCommentLock(pageID, "", func(tx *sqlx.Tx, locked store.LockedPage) error {
		if appErr := requireLockedPageInSpace("CreatePageComment", locked, space); appErr != nil {
			return appErr
		}
		post, appErr := s.createCommentPost("CreatePageComment", tx, locked, pageID, "", create.Message, props, userID)
		if appErr != nil {
			return appErr
		}
		created = post
		return nil
	})
	if lockErr != nil && created == nil {
		return nil, commentLockAppError("CreatePageComment", pageID, lockErr)
	}
	if lockErr != nil {
		s.warnLockFailedAfterCommit("comment create", created.Id, lockErr)
	}

	s.publishToChannels(wsEventPageCommentCreated, pageCommentEventPayload(created.Id, "", pageID, space.Id), space.ChannelId)
	return model.NewPageCommentFromPost(created, nil, space.Id, 0), nil
}

// CreatePageCommentReply creates a reply on rootID. Replies carry no anchor fields — a reply
// inherits the root's comment_type/anchor_id by belonging to the thread — and sub-replies are
// rejected by the plugin before core is called, since the parent's RootId is already known.
func (s *Service) CreatePageCommentReply(space *model.Space, pageID, rootID, message, userID string) (*model.PageComment, *mmmodel.AppError) {
	if appErr := s.validateCommentWrite("CreatePageCommentReply", space, pageID, userID); appErr != nil {
		return nil, appErr
	}
	create := &model.PageCommentCreate{Message: message}
	create.Normalize()
	if appErr := create.IsValid(); appErr != nil {
		return nil, appErr
	}

	parent, appErr := s.resolvePageComment("CreatePageCommentReply", space, pageID, rootID, false)
	if appErr != nil {
		return nil, appErr
	}
	if parent.RootId != "" {
		return nil, mmmodel.NewAppError("CreatePageCommentReply", "app.page_comment.create_reply.parent_is_reply.app_error", nil, "", http.StatusBadRequest)
	}

	s.log.Debug("Creating page comment reply", "page_id", pageID, "root_id", rootID, "user_id", userID)

	props := mmmodel.StringInterface{model.PropKeyPageId: pageID}
	var created *mmmodel.Post
	var root *mmmodel.Post
	lockErr := s.store.WithPageCommentLock(pageID, rootID, func(tx *sqlx.Tx, locked store.LockedPage) error {
		if appErr := requireLockedPageInSpace("CreatePageCommentReply", locked, space); appErr != nil {
			return appErr
		}
		// Re-read the root under the advisory lock: a delete that won the lock first has already
		// cascaded, and core's own parent check must not be what this path relies on.
		lockedRoot, rootErr := s.resolvePageCommentTx("CreatePageCommentReply", tx, locked, pageID, rootID, false)
		if rootErr != nil {
			return rootErr
		}
		root = lockedRoot
		post, createErr := s.createCommentPost("CreatePageCommentReply", tx, locked, pageID, rootID, create.Message, props, userID)
		if createErr != nil {
			return createErr
		}
		created = post
		return nil
	})
	if lockErr != nil && created == nil {
		return nil, commentLockAppError("CreatePageCommentReply", pageID, lockErr)
	}
	if lockErr != nil {
		s.warnLockFailedAfterCommit("comment reply create", created.Id, lockErr)
	}

	s.publishToChannels(wsEventPageCommentCreated, pageCommentEventPayload(created.Id, rootID, pageID, space.Id), space.ChannelId)
	return model.NewPageCommentFromPost(created, root, space.Id, 0), nil
}

// createCommentPost builds the comment post with a handler-allocated id and creates it through
// the plugin API while the page lock is held. The id is allocated here rather than generated by
// PreSave so that a failure from CreatePost can be resolved by fact: the row is read back by that
// exact id on the lock's transaction, and found means the comment is durably created — the
// pending-post-id cache write can fail after the store save has committed — while not-found means
// nothing was written. A committed-but-errored create still publishes its event (the caller sees
// the error either way); a probe error is read in the keeping direction, since a spurious
// ids-only refetch signal is harmless and a suppressed one leaves every client stale.
func (s *Service) createCommentPost(where string, tx *sqlx.Tx, locked store.LockedPage, pageID, rootID, message string, props mmmodel.StringInterface, userID string) (*mmmodel.Post, *mmmodel.AppError) {
	post := &mmmodel.Post{
		Id:        mmmodel.NewId(),
		ChannelId: locked.ChannelId,
		RootId:    rootID,
		UserId:    userID,
		Message:   message,
		Type:      model.PostTypePageComment,
	}
	post.SetProps(props)

	if createErr := s.client.Post.CreatePost(post); createErr != nil {
		probe, probeErr := s.store.GetPageCommentTx(tx, post.Id, locked.ChannelId, pageID, false)
		if probeErr == nil {
			s.publishToChannels(wsEventPageCommentCreated, pageCommentEventPayload(probe.Id, rootID, pageID, locked.SpaceId), locked.ChannelId)
		} else if !store.IsErrNotFound(probeErr) {
			s.log.Warn("Comment create probe failed; treating the write as committed", "comment_id", post.Id, "err", probeErr)
			s.publishToChannels(wsEventPageCommentCreated, pageCommentEventPayload(post.Id, rootID, pageID, locked.SpaceId), locked.ChannelId)
		}
		return nil, mmmodel.NewAppError(where, "app.page_comment.create.create_post_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(createErr)
	}
	return post, nil
}

// UpdatePageComment applies a patch to a comment: a resolve/unresolve of a root, a message edit
// of the caller's own root or reply, or both at once. resolved_by/resolved_at attribute the last
// state change in either direction, so an unresolve records who reopened the thread; a message
// edit leaves resolve state untouched and is stamped by core as EditAt. The write is
// read-modify-write over the full prop map: the plugin API's UpdatePost replaces the map
// wholesale, and page_id is not on core's identity-prop rescue list, so a fresh map would orphan
// the comment permanently.
func (s *Service) UpdatePageComment(space *model.Space, pageID, commentID string, patch *model.PageCommentPatch, userID string) (*model.PageComment, *mmmodel.AppError) {
	if appErr := s.validateCommentWrite("UpdatePageComment", space, pageID, userID); appErr != nil {
		return nil, appErr
	}
	patch.Normalize()
	if appErr := patch.IsValid(); appErr != nil {
		return nil, appErr
	}

	target, appErr := s.resolvePageComment("UpdatePageComment", space, pageID, commentID, false)
	if appErr != nil {
		return nil, appErr
	}
	if patch.Resolved != nil && target.RootId != "" {
		// The roots listing's resolved filter only sees roots, so a resolved reply would be
		// settable and broadcast while being invisible to every filter.
		return nil, mmmodel.NewAppError("UpdatePageComment", "app.page_comment.patch.target_is_reply.app_error", nil, "", http.StatusBadRequest)
	}
	if patch.Message != nil && target.UserId != userID {
		// Message editing is author-only for every principal; other members delete, they do not
		// rewrite someone else's words. Resolve carries no author gate, so a mixed patch from a
		// non-author is refused whole rather than half-applied.
		return nil, mmmodel.NewAppError("UpdatePageComment", "app.page_comment.patch.message_not_author.app_error", nil, "", http.StatusForbidden)
	}

	s.log.Debug("Updating page comment", "comment_id", commentID, "page_id", pageID, "user_id", userID)

	var updated *mmmodel.Post
	var replyCount int
	lockErr := s.store.WithPageCommentLock(pageID, "", func(tx *sqlx.Tx, locked store.LockedPage) error {
		if lockAppErr := requireLockedPageInSpace("UpdatePageComment", locked, space); lockAppErr != nil {
			return lockAppErr
		}
		preImage, preErr := s.resolvePageCommentTx("UpdatePageComment", tx, locked, pageID, commentID, false)
		if preErr != nil {
			return preErr
		}

		// The count is read before the write: nothing may run between a successful UpdatePost and
		// the callback's return, or its failure would report a committed write as failed and
		// suppress the publish below — the only signal other clients get.
		counts, countErr := s.store.GetPageCommentReplyCountsTx(tx, []string{commentID})
		if countErr != nil {
			return storeAppError("UpdatePageComment", countErr)
		}
		replyCount = counts[commentID]

		post, getErr := s.client.Post.GetPost(commentID)
		if getErr != nil {
			return mmmodel.NewAppError("UpdatePageComment", "app.page_comment.patch.get_post_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(getErr)
		}
		if patch.Message != nil {
			post.Message = *patch.Message
		}
		if patch.Resolved != nil {
			props := make(mmmodel.StringInterface, len(post.GetProps())+3)
			maps.Copy(props, post.GetProps())
			props[model.PropKeyResolved] = *patch.Resolved
			props[model.PropKeyResolvedBy] = userID
			props[model.PropKeyResolvedAt] = mmmodel.GetMillis()
			post.SetProps(props)
		}

		if updateErr := s.client.Post.UpdatePost(post); updateErr != nil {
			if s.probePatchCommitted(tx, locked, pageID, commentID, preImage) {
				s.publishToChannels(wsEventPageCommentUpdated, pageCommentEventPayload(commentID, target.RootId, pageID, locked.SpaceId), locked.ChannelId)
			}
			return mmmodel.NewAppError("UpdatePageComment", "app.page_comment.patch.update_post_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(updateErr)
		}
		updated = post
		return nil
	})
	if lockErr != nil && updated == nil {
		return nil, commentLockAppError("UpdatePageComment", pageID, lockErr)
	}
	if lockErr != nil {
		s.warnLockFailedAfterCommit("comment update", commentID, lockErr)
	}

	s.publishToChannels(wsEventPageCommentUpdated, pageCommentEventPayload(commentID, updated.RootId, pageID, space.Id), space.ChannelId)
	if updated.RootId != "" {
		root, rootErr := s.resolvePageComment("UpdatePageComment", space, pageID, updated.RootId, false)
		if rootErr != nil {
			return nil, rootErr
		}
		return model.NewPageCommentFromPost(updated, root, space.Id, 0), nil
	}
	return model.NewPageCommentFromPost(updated, nil, space.Id, replyCount), nil
}

// warnLockFailedAfterCommit records a WithPageCommentLock failure that arrived after its callback
// completed — in practice the lock transaction's own commit. That transaction writes nothing, so
// its failure cannot undo the plugin-API write the callback already made; the operation succeeded
// and the caller proceeds to publish and report success.
func (s *Service) warnLockFailedAfterCommit(operation, commentID string, lockErr error) {
	s.log.Warn("Comment lock transaction failed after the write committed", "operation", operation, "comment_id", commentID, "err", lockErr)
}

// probePatchCommitted reports whether a PATCH whose UpdatePost returned an error actually
// persisted, by comparing the re-read row against the pre-image taken under the same lock — any
// difference means committed. Comparing against the requested value instead would misreport a
// PATCH asking for the value already stored. The comparison errs in the keeping direction: a
// concurrent third-party change makes the post-image differ for a reason this request did not
// cause, and a spurious ids-only refetch signal is the cheap mistake.
func (s *Service) probePatchCommitted(tx *sqlx.Tx, locked store.LockedPage, pageID, commentID string, preImage *mmmodel.Post) bool {
	probe, probeErr := s.store.GetPageCommentTx(tx, commentID, locked.ChannelId, pageID, false)
	if probeErr != nil {
		if store.IsErrNotFound(probeErr) {
			// The row fell out of the live read; nothing this PATCH asked for does that, so read
			// it as changed-by-someone rather than not-committed.
			return true
		}
		s.log.Warn("Comment patch probe failed; treating the write as committed", "comment_id", commentID, "err", probeErr)
		return true
	}
	preProps, probeProps := preImage.GetProps(), probe.GetProps()
	return probe.UpdateAt != preImage.UpdateAt ||
		preProps[model.PropKeyResolved] != probeProps[model.PropKeyResolved] ||
		preProps[model.PropKeyResolvedBy] != probeProps[model.PropKeyResolvedBy] ||
		preProps[model.PropKeyResolvedAt] != probeProps[model.PropKeyResolvedAt]
}

// DeletePageComment soft-deletes a comment. The author deletes their own comment, except a root
// with live replies, which is refused 409 (the returned replyCount populates the 409 body so the
// client can say how many replies the delete would destroy); any other space member deletes any
// comment and force-deletes through the guard — the platform cascade takes the replies with the
// root. The per-permission split of that contract (delete_own_page vs delete_page_comment) lands
// with the RBAC epic.
func (s *Service) DeletePageComment(space *model.Space, pageID, commentID, userID string) (int, *mmmodel.AppError) {
	if appErr := s.validateCommentWrite("DeletePageComment", space, pageID, userID); appErr != nil {
		return 0, appErr
	}

	target, appErr := s.resolvePageComment("DeletePageComment", space, pageID, commentID, false)
	if appErr != nil {
		return 0, appErr
	}
	// Every root deletion takes the advisory lock, not only the own-delete path: the lock's job
	// is to keep a reply from committing after the root's cascade ran, and a force-delete skips
	// the guard, never the cascade.
	rootLockKey := ""
	if target.RootId == "" {
		rootLockKey = commentID
	}

	s.log.Debug("Deleting page comment", "comment_id", commentID, "page_id", pageID, "user_id", userID)

	var deletedRootID string
	var replyCount int
	deleted := false
	lockErr := s.store.WithPageCommentLock(pageID, rootLockKey, func(tx *sqlx.Tx, locked store.LockedPage) error {
		if lockAppErr := requireLockedPageInSpace("DeletePageComment", locked, space); lockAppErr != nil {
			return lockAppErr
		}
		preImage, preErr := s.resolvePageCommentTx("DeletePageComment", tx, locked, pageID, commentID, false)
		if preErr != nil {
			return preErr
		}
		deletedRootID = preImage.RootId

		// The guard applies only on the own-delete path, and only after that path admitted the
		// caller — force-delete, by definition, must bypass it for every other principal.
		if preImage.UserId == userID && preImage.RootId == "" {
			counts, countErr := s.store.GetPageCommentReplyCountsTx(tx, []string{commentID})
			if countErr != nil {
				return storeAppError("DeletePageComment", countErr)
			}
			if counts[commentID] > 0 {
				replyCount = counts[commentID]
				return mmmodel.NewAppError("DeletePageComment", "app.page_comment.delete.has_replies.app_error", map[string]any{"ReplyCount": replyCount}, "", http.StatusConflict)
			}
		}

		if deleteErr := s.client.Post.DeletePost(commentID); deleteErr != nil {
			if s.probeDeleteCommitted(tx, locked, pageID, commentID) {
				s.publishToChannels(wsEventPageCommentDeleted, pageCommentEventPayload(commentID, preImage.RootId, pageID, locked.SpaceId), locked.ChannelId)
			}
			return mmmodel.NewAppError("DeletePageComment", "app.page_comment.delete.delete_post_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(deleteErr)
		}
		deleted = true
		return nil
	})
	if lockErr != nil && !deleted {
		return replyCount, commentLockAppError("DeletePageComment", pageID, lockErr)
	}
	if lockErr != nil {
		s.warnLockFailedAfterCommit("comment delete", commentID, lockErr)
	}

	s.publishToChannels(wsEventPageCommentDeleted, pageCommentEventPayload(commentID, deletedRootID, pageID, space.Id), space.ChannelId)
	return 0, nil
}

// probeDeleteCommitted reports whether a DELETE whose DeletePost returned an error actually
// persisted: the same id, read with soft-deleted rows included, is committed when DeleteAt != 0.
// This is the one probe that needs an include-deleted read. Probe failures keep the committed
// verdict, matching the other probes' keeping direction.
func (s *Service) probeDeleteCommitted(tx *sqlx.Tx, locked store.LockedPage, pageID, commentID string) bool {
	probe, probeErr := s.store.GetPageCommentTx(tx, commentID, locked.ChannelId, pageID, true)
	if probeErr != nil {
		if store.IsErrNotFound(probeErr) {
			// Gone entirely (e.g. permanently deleted underneath us): announce the deletion.
			return true
		}
		s.log.Warn("Comment delete probe failed; treating the delete as committed", "comment_id", commentID, "err", probeErr)
		return true
	}
	return probe.DeleteAt != 0
}

// reconcileCommentChunkSize bounds one detection read and one core move call; the sweep loops
// until no misplaced root remains.
const reconcileCommentChunkSize = 100

// reconcileCommentChannels re-homes every comment root — and, through the core move, its whole
// thread — that is tied to a page of the space but still stored on another channel. A page move
// cannot carry its comments in the same transaction (the re-home is a core call), so the sweep
// runs after the move commits. It is idempotent: a re-issued move, or any later move into the
// space, also repairs stragglers an earlier failed sweep left behind. The misplaced state is
// bounded and detectable (the page_id prop survives the move), which is what makes deferring
// the repair to a re-run safe. A non-empty sourceChannelIDs keeps the detection on the channel
// index for the routine after-a-move case; nil sweeps the whole space, the repair posture.
func (s *Service) reconcileCommentChannels(space *model.Space, sourceChannelIDs []string) error {
	if s.client == nil {
		return errors.New("pluginapi client not wired")
	}
	lastFirst := ""
	for {
		roots, err := s.store.GetMisplacedCommentRoots(space.Id, space.ChannelId, sourceChannelIDs, reconcileCommentChunkSize)
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			return nil
		}
		// The detection is ordered by id, so the same leading id after a successful move means
		// the move is not landing rows on the target — stop rather than loop forever.
		if roots[0] == lastFirst {
			return errors.New("comment re-home is not converging; root " + roots[0] + " is still misplaced after a move")
		}
		lastFirst = roots[0]
		if err := s.client.Post.MovePostsToChannel(roots, space.ChannelId); err != nil {
			return err
		}
	}
}

// validateCommentWrite is the shared precondition of the four write paths.
func (s *Service) validateCommentWrite(where string, space *model.Space, pageID, userID string) *mmmodel.AppError {
	if appErr := s.requireClient(where, "page_id", pageID, "user_id", userID); appErr != nil {
		return appErr
	}
	if appErr := requirePageCommentArgs(where, space, pageID); appErr != nil {
		return appErr
	}
	if !mmmodel.IsValidId(userID) {
		return mmmodel.NewAppError(where, "app.page_comment.invalid_user_id.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// commentLockAppError maps WithPageCommentLock's return to the caller's error. An *AppError from
// the callback passes through; the lock's own page miss reads as the page 404 (a deleted,
// snapshotted, or missing page is not lockable); anything else is a store failure.
func commentLockAppError(where, pageID string, err error) *mmmodel.AppError {
	if err == nil {
		return nil
	}
	var appErr *mmmodel.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if store.IsErrNotFound(err) {
		return mmmodel.NewAppError(where, "app.page.not_found.app_error", nil, "page_id="+pageID, http.StatusNotFound).Wrap(err)
	}
	return storeAppError(where, err)
}
