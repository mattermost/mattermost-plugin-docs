// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// maxCommentBodyBytes caps the raw request body for comment create/patch. A comment message is
// capped at core's post-message rune limit, so this only needs headroom for JSON escaping.
const maxCommentBodyBytes = 1 << 20 // 1 MiB

// encodeCommentCursor renders a (CreateAt, Id) keyset boundary as the opaque, URL-safe `after`
// token. The encoding is fixed in code, not in the API contract: clients must treat it as opaque.
func encodeCommentCursor(c *app.PageCommentCursor) *string {
	if c == nil {
		return nil
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.CreateAt, 10) + "|" + c.Id))
	return &token
}

// afterCursorParam parses the `after` query param. Absent means the first window. Only an
// unparseable cursor is a 400; a well-formed cursor naming a deleted or foreign row is not an
// error — the SQL comparison simply resumes from the nearest greater row.
func (p *Plugin) afterCursorParam(w http.ResponseWriter, r *http.Request, where string) (*app.PageCommentCursor, bool) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return nil, true
	}
	invalid := func() (*app.PageCommentCursor, bool) {
		p.writeAppError(w, mmmodel.NewAppError(where, "api.page_comment.invalid_cursor.app_error", nil, "", http.StatusBadRequest))
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return invalid()
	}
	createAtPart, id, found := strings.Cut(string(decoded), "|")
	if !found || !mmmodel.IsValidId(id) {
		return invalid()
	}
	createAt, parseErr := strconv.ParseInt(createAtPart, 10, 64)
	if parseErr != nil || createAt < 0 {
		return invalid()
	}
	return &app.PageCommentCursor{CreateAt: createAt, Id: id}, true
}

// resolvedFilterParam parses the `resolved` query param: absent/empty means all roots.
func (p *Plugin) resolvedFilterParam(w http.ResponseWriter, r *http.Request, where string) (*bool, bool) {
	switch r.URL.Query().Get("resolved") {
	case "":
		return nil, true
	case "true":
		v := true
		return &v, true
	case "false":
		v := false
		return &v, true
	default:
		p.writeAppError(w, mmmodel.NewAppError(where, "api.page_comment.invalid_resolved_filter.app_error", nil, "", http.StatusBadRequest))
		return nil, false
	}
}

// commentTypeFilterParam parses the `comment_type` query param: absent/empty means both kinds.
func (p *Plugin) commentTypeFilterParam(w http.ResponseWriter, r *http.Request, where string) (string, bool) {
	switch v := r.URL.Query().Get("comment_type"); v {
	case "", model.CommentTypeFooter, model.CommentTypeInline:
		return v, true
	default:
		p.writeAppError(w, mmmodel.NewAppError(where, "api.page_comment.invalid_comment_type_filter.app_error", nil, "", http.StatusBadRequest))
		return "", false
	}
}

// handleGetPageComments handles GET /api/v1/spaces/{space_id}/pages/{page_id}/comments.
// Roots only, keyset-paged via `after`; `resolved` and `comment_type` compose in the SQL
// predicate, so has_more is computed over the filtered set.
func (p *Plugin) handleGetPageComments(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	resolved, ok := p.resolvedFilterParam(w, r, "handleGetPageComments")
	if !ok {
		return
	}
	commentType, ok := p.commentTypeFilterParam(w, r, "handleGetPageComments")
	if !ok {
		return
	}
	after, ok := p.afterCursorParam(w, r, "handleGetPageComments")
	if !ok {
		return
	}
	perPage := perPageParam(r)

	comments, nextAfter, hasMore, appErr := p.service.GetPageComments(space, vars["page_id"], resolved, commentType, after, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeCursorJSON(w, comments, encodeCommentCursor(nextAfter), perPage, hasMore)
}

// handleGetPageComment handles GET /api/v1/spaces/{space_id}/pages/{page_id}/comments/{comment_id} —
// the deep-link target. Unlike the replies sub-resource it accepts both roots and replies.
func (p *Plugin) handleGetPageComment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	comment, appErr := p.service.GetPageComment(space, vars["page_id"], vars["comment_id"])
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, comment)
}

// handleGetPageCommentReplies handles
// GET /api/v1/spaces/{space_id}/pages/{page_id}/comments/{comment_id}/replies.
func (p *Plugin) handleGetPageCommentReplies(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	page, perPage := pageParam(r), perPageParam(r)
	replies, hasMore, appErr := p.service.GetPageCommentReplies(space, vars["page_id"], vars["comment_id"], page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, replies, page, perPage, hasMore)
}

// handleCreatePageComment handles POST /api/v1/spaces/{space_id}/pages/{page_id}/comments.
func (p *Plugin) handleCreatePageComment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	auditRec := p.makeAuditRecord(r, auditEventCreatePageComment, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	var req model.PageCommentCreate
	if !p.decodeJSONBody(w, r, maxCommentBodyBytes, &req, "handleCreatePageComment", false) {
		return
	}
	comment, appErr := p.service.CreatePageComment(space, vars["page_id"], &req, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("page_comment")
	auditRec.AddEventResultState(comment)
	writeJSON(w, http.StatusCreated, comment)
}

// handleCreatePageCommentReply handles
// POST /api/v1/spaces/{space_id}/pages/{page_id}/comments/{comment_id}/replies. Only message is
// decoded: a reply inherits the root's anchor, so the anchor keys are dropped like any other
// unknown field.
func (p *Plugin) handleCreatePageCommentReply(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	auditRec := p.makeAuditRecord(r, auditEventCreatePageCommentReply, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if !p.decodeJSONBody(w, r, maxCommentBodyBytes, &req, "handleCreatePageCommentReply", false) {
		return
	}
	comment, appErr := p.service.CreatePageCommentReply(space, vars["page_id"], vars["comment_id"], req.Message, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("page_comment")
	auditRec.AddEventResultState(comment)
	writeJSON(w, http.StatusCreated, comment)
}

// handleUpdatePageComment handles PATCH /api/v1/spaces/{space_id}/pages/{page_id}/comments/{comment_id}.
// The patch accepts resolved (roots only) and message (author-only, roots and replies);
// comment_type/anchor_id are immutable after create — re-anchoring is a delete plus a create.
func (p *Plugin) handleUpdatePageComment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	auditRec := p.makeAuditRecord(r, auditEventUpdatePageComment, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	var patch model.PageCommentPatch
	if !p.decodeJSONBody(w, r, maxCommentBodyBytes, &patch, "handleUpdatePageComment", false) {
		return
	}
	comment, appErr := p.service.UpdatePageComment(space, vars["page_id"], vars["comment_id"], &patch, userID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("page_comment")
	auditRec.AddEventResultState(comment)
	writeJSON(w, http.StatusOK, comment)
}

// handleDeletePageComment handles DELETE /api/v1/spaces/{space_id}/pages/{page_id}/comments/{comment_id}.
// The has-replies 409 carries the reply count as a declared field on the conflict body.
func (p *Plugin) handleDeletePageComment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := userIDFromRequest(r)
	auditRec := p.makeAuditRecord(r, auditEventDeletePageComment, userID)
	defer p.client.Audit.Record(auditRec)
	space, ok := p.requireSpaceMembership(w, vars["space_id"], userID, false)
	if !ok {
		return
	}
	replyCount, appErr := p.service.DeletePageComment(space, vars["page_id"], vars["comment_id"], userID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusConflict && replyCount > 0 {
			p.writeConflictWithReplyCount(w, appErr, replyCount)
			return
		}
		p.writeAppError(w, appErr)
		return
	}
	auditRec.Success()
	auditRec.AddEventObjectType("page_comment")
	writeStatusOK(w)
}
