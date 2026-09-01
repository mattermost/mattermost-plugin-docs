// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

const (
	// PostTypePageComment is the Posts.Type of every page comment row. The "custom_" prefix is
	// required by Post.IsValid (PostCustomTypePrefix).
	PostTypePageComment = mmmodel.PostCustomTypePrefix + "page_comment"

	// PropKeyPageId ties a comment post to its page. It is the only provenance linking the two,
	// so it is read and written exclusively through this constant — a misspelling at any site
	// permanently orphans the comment.
	PropKeyPageId = "page_id"

	// PropKeyCommentType distinguishes footer from inline comments on a root post. Absence on a
	// root means footer; replies carry neither this key nor the anchor and inherit the root's
	// values in the projection.
	PropKeyCommentType = "comment_type"

	// PropKeyAnchorId carries the document-embedded marker id an inline comment is anchored to.
	// The server treats it as an opaque string and never dereferences it against the page body.
	PropKeyAnchorId = "anchor_id"

	// PropKeyResolved marks a resolved root comment. Absence means unresolved.
	PropKeyResolved = "resolved"

	// PropKeyResolvedBy and PropKeyResolvedAt attribute the last resolve-state change in either
	// direction (resolve or unresolve); they are set together or not at all, except on an
	// orphan resolve, which has a time but no actor.
	PropKeyResolvedBy = "resolved_by"
	PropKeyResolvedAt = "resolved_at"

	// PropKeyResolvedReason separates a resolve the server performed because the comment's anchor
	// left the page from one a person chose. Absence reads as manual, so comments written before
	// the key existed need no backfill.
	PropKeyResolvedReason = "resolved_reason"

	CommentTypeFooter = "footer"
	CommentTypeInline = "inline"

	// ResolvedReasonManual and ResolvedReasonDangling are the values of PropKeyResolvedReason.
	// "dangling" matches Confluence's own name for a comment resolved because its text went away,
	// and clients use it to offer restoring the thread to the page.
	ResolvedReasonManual   = "manual"
	ResolvedReasonDangling = "dangling"

	// MaxAnchorIdRunes bounds the client-generated anchor marker id. Enforced at create and
	// clamped again by the projector, since other writers of comment rows are not bound by the
	// create-time check.
	MaxAnchorIdRunes = 128
)

var _ mmmodel.Auditable = (*PageComment)(nil)

// PageComment is the response projection of a page-comment Posts row. Resolve state and the
// inline fields are projected out of the post's props into typed fields at the serialization
// boundary so clients are never coupled to prop-key spellings. ChannelId is never serialized,
// matching the Page invariant.
type PageComment struct {
	Id          string `json:"id"`
	SpaceId     string `json:"space_id"`
	PageId      string `json:"page_id"`
	UserId      string `json:"user_id"`
	RootId      string `json:"root_id"`
	Message     string `json:"message"`
	CommentType string `json:"comment_type"`
	AnchorId    string `json:"anchor_id,omitempty"`
	ReplyCount  int    `json:"reply_count"`
	Resolved    bool   `json:"resolved"`
	ResolvedBy  string `json:"resolved_by,omitempty"`
	ResolvedAt  int64  `json:"resolved_at,omitempty"`
	// ResolvedReason is empty on an unresolved comment and otherwise names why the thread closed.
	ResolvedReason string `json:"resolved_reason,omitempty"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	EditAt         int64  `json:"edit_at,omitempty"`
}

// Auditable returns the comment's audit projection. Message is excluded: comment text is user
// content and does not belong in audit records, matching Page.Auditable and Draft.Auditable.
func (c *PageComment) Auditable() map[string]any {
	return map[string]any{
		"id":              c.Id,
		"space_id":        c.SpaceId,
		"page_id":         c.PageId,
		"user_id":         c.UserId,
		"root_id":         c.RootId,
		"comment_type":    c.CommentType,
		"anchor_id":       c.AnchorId,
		"reply_count":     c.ReplyCount,
		"resolved":        c.Resolved,
		"resolved_by":     c.ResolvedBy,
		"resolved_at":     c.ResolvedAt,
		"resolved_reason": c.ResolvedReason,
		"create_at":       c.CreateAt,
		"update_at":       c.UpdateAt,
		"edit_at":         c.EditAt,
	}
}

// PageCommentCounts is a page's thread tally, the shape behind the page's comment affordance.
// The numbers cover root comments only: a thread is one discussion however many replies it holds.
// Total is always Open + Resolved, computed in one scan so the three cannot disagree.
type PageCommentCounts struct {
	Total    int `json:"total"`
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
}

// PageCommentCreate is the request body for both comment-create routes. CommentType and AnchorId
// are honoured on the roots route only; the replies route decodes just Message, since a reply
// inherits its root's anchor.
type PageCommentCreate struct {
	Message     string `json:"message"`
	CommentType string `json:"comment_type,omitempty"`
	AnchorId    string `json:"anchor_id,omitempty"`
}

// Normalize trims the message. The anchor is left as-is: it is an opaque marker id, not text.
func (c *PageCommentCreate) Normalize() {
	if c == nil {
		return
	}
	c.Message = strings.TrimSpace(mmmodel.SanitizeUnicode(c.Message))
}

// IsValid checks the create body after Normalize. CommentType defaults to footer when empty;
// an inline comment requires an anchor and a footer comment must not carry one — each half-state
// gets its own error id, since a client fixes one by adding a field and the other by removing one.
func (c *PageCommentCreate) IsValid() *mmmodel.AppError {
	if c == nil {
		return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.message_required.app_error", nil, "", http.StatusBadRequest)
	}
	if c.Message == "" {
		return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.message_required.app_error", nil, "", http.StatusBadRequest)
	}
	if utf8.RuneCountInString(c.Message) > mmmodel.PostMessageMaxRunesV2 {
		return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.message_too_long.app_error", map[string]any{"MaxLength": mmmodel.PostMessageMaxRunesV2}, "", http.StatusBadRequest)
	}
	switch c.CommentType {
	case "", CommentTypeFooter:
		if c.AnchorId != "" {
			return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.anchor_not_allowed.app_error", nil, "", http.StatusBadRequest)
		}
	case CommentTypeInline:
		if strings.TrimSpace(c.AnchorId) == "" {
			return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.anchor_required.app_error", nil, "", http.StatusBadRequest)
		}
		if utf8.RuneCountInString(c.AnchorId) > MaxAnchorIdRunes {
			return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.anchor_too_long.app_error", map[string]any{"MaxLength": MaxAnchorIdRunes}, "", http.StatusBadRequest)
		}
	default:
		return mmmodel.NewAppError("PageCommentCreate.IsValid", "model.page_comment.create.invalid_comment_type.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// PageCommentPatch is the PATCH body for a comment. Both fields are pointers so an empty body {}
// is distinguishable from an explicit false or empty string; IsValid rejects an all-nil patch so
// a malformed PATCH cannot silently unresolve. Mirrors PagePatch and SpacePatch.
type PageCommentPatch struct {
	Resolved *bool   `json:"resolved"`
	Message  *string `json:"message"`
}

// Normalize trims the message, when one is being set.
func (p *PageCommentPatch) Normalize() {
	if p == nil {
		return
	}
	if p.Message != nil {
		*p.Message = strings.TrimSpace(mmmodel.SanitizeUnicode(*p.Message))
	}
}

// IsValid checks the patch after Normalize, rejecting a nil or all-nil patch.
func (p *PageCommentPatch) IsValid() *mmmodel.AppError {
	if p == nil || (p.Resolved == nil && p.Message == nil) {
		return mmmodel.NewAppError("PageCommentPatch.IsValid", "model.page_comment.patch.nothing_to_update.app_error", nil, "", http.StatusBadRequest)
	}
	if p.Message != nil {
		if *p.Message == "" {
			return mmmodel.NewAppError("PageCommentPatch.IsValid", "model.page_comment.patch.message_required.app_error", nil, "", http.StatusBadRequest)
		}
		if utf8.RuneCountInString(*p.Message) > mmmodel.PostMessageMaxRunesV2 {
			return mmmodel.NewAppError("PageCommentPatch.IsValid", "model.page_comment.patch.message_too_long.app_error", map[string]any{"MaxLength": mmmodel.PostMessageMaxRunesV2}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// NewPageCommentFromPost is the single place a page-comment Posts row is read into a PageComment;
// every handler goes through it so prop-key spellings cannot drift. root is nil when post is
// itself a root and otherwise carries the thread root, whose comment_type/anchor_id the reply
// inherits — a reply row carries neither prop, so the root-absence-means-footer rule (correct for
// roots) must not be applied to it. Props are untyped
// JSON, so values are coerced defensively rather than asserted. The projector also enforces the
// one invariant the struct cannot express: ResolvedBy and ResolvedAt are set together or not at
// all, the sole exception being a dangling resolve, which carries a time and no actor.
func NewPageCommentFromPost(post, root *mmmodel.Post, spaceID string, replyCount int) *PageComment {
	props := post.GetProps()

	typeSource := props
	if post.RootId != "" && root != nil {
		typeSource = root.GetProps()
	}
	commentType := CommentTypeFooter
	anchorID := ""
	if propString(typeSource, PropKeyCommentType) == CommentTypeInline {
		commentType = CommentTypeInline
		anchorID, _ = mmmodel.LimitRunes(propString(typeSource, PropKeyAnchorId), MaxAnchorIdRunes)
	}

	resolved := propBool(props, PropKeyResolved)
	resolvedReason := ""
	if resolved {
		resolvedReason = ResolvedReasonManual
		if propString(props, PropKeyResolvedReason) == ResolvedReasonDangling {
			resolvedReason = ResolvedReasonDangling
		}
	}

	resolvedBy := propString(props, PropKeyResolvedBy)
	resolvedAt := propInt64(props, PropKeyResolvedAt)
	// A dangling resolve has a time but no actor, so it is the one case the pair-or-neither rule
	// must not collapse: dropping its timestamp would leave the client unable to say when the
	// anchor went away.
	if resolvedReason != ResolvedReasonDangling && (resolvedBy == "" || resolvedAt == 0) {
		resolvedBy, resolvedAt = "", 0
	}
	if resolvedReason == ResolvedReasonDangling {
		resolvedBy = ""
	}

	return &PageComment{
		Id:             post.Id,
		SpaceId:        spaceID,
		PageId:         propString(props, PropKeyPageId),
		UserId:         post.UserId,
		RootId:         post.RootId,
		Message:        post.Message,
		CommentType:    commentType,
		AnchorId:       anchorID,
		ReplyCount:     replyCount,
		Resolved:       resolved,
		ResolvedBy:     resolvedBy,
		ResolvedAt:     resolvedAt,
		ResolvedReason: resolvedReason,
		CreateAt:       post.CreateAt,
		UpdateAt:       post.UpdateAt,
		EditAt:         post.EditAt,
	}
}

// propString reads a string prop, yielding "" for an absent or non-string value.
func propString(props mmmodel.StringInterface, key string) string {
	s, _ := props[key].(string)
	return s
}

// propBool reads a boolean prop, accepting the JSON bool true and the string "true" — the two
// encodings a round-trip through Posts.Props can produce.
func propBool(props mmmodel.StringInterface, key string) bool {
	switch v := props[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

// propInt64 reads an integer prop across the numeric encodings a JSON round-trip can produce.
func propInt64(props mmmodel.StringInterface, key string) int64 {
	switch v := props[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	default:
		return 0
	}
}
