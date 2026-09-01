// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

	"github.com/jmoiron/sqlx"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// This file is read-only against core's Posts table on the plugin's master handle: comment writes
// go through the plugin API's post methods, never through DML here. Direct SQL is used only
// because the plugin API offers no prop- or type-predicated post read; the file carries no
// authorization or business rules.

// pageIdProp reads the page_id prop as a literal-key JSON expression. The key must be a literal
// (not a bind parameter): a partial expression index on (Props->>'page_id') can only be matched
// by a query using the identical literal expression.
const pageIdProp = "Props->>'" + model.PropKeyPageId + "'"

const pageIdPropExpr = pageIdProp + " = ?"

// resolvedTrueExpr / resolvedNotTrueExpr filter on the resolved prop. The negative form must be
// IS DISTINCT FROM: the key is absent, not 'false', on never-resolved comments, so a plain != or
// NOT IN would match nothing.
const (
	resolvedTrueExpr    = "Props->>'" + model.PropKeyResolved + "' = 'true'"
	resolvedNotTrueExpr = "Props->>'" + model.PropKeyResolved + "' IS DISTINCT FROM 'true'"
)

// inlineTypeExpr / notInlineTypeExpr filter on the comment_type prop. Absence means footer, so
// the footer predicate is IS DISTINCT FROM 'inline' rather than = 'footer'.
const (
	inlineTypeExpr    = "Props->>'" + model.PropKeyCommentType + "' = '" + model.CommentTypeInline + "'"
	notInlineTypeExpr = "Props->>'" + model.PropKeyCommentType + "' IS DISTINCT FROM '" + model.CommentTypeInline + "'"
)

// postColumnList is the set of Posts columns the comment projections read.
var postColumnList = []string{"Id", "CreateAt", "UpdateAt", "EditAt", "DeleteAt", "UserId", "ChannelId", "RootId", "Message", "Type", "Props"}

// PageCommentListOptions narrows the roots listing. Resolved nil means all roots; CommentType ""
// means both kinds. AfterCreateAt/AfterID carry the keyset cursor boundary; both zero values mean
// the first page. Limit must be positive (callers pass perPage+1 and trim the probe row).
type PageCommentListOptions struct {
	Resolved      *bool
	CommentType   string
	AfterCreateAt int64
	AfterID       string
	Limit         int
}

// pageCommentBaseQuery is the shared identity predicate of every comment read: bounded to the
// space's backing channel (no Posts index covers Type or Props, so ChannelId is what bounds the
// scan) and keyed on the page_id prop.
func (s *Store) pageCommentBaseQuery(channelID, pageID string) sq.SelectBuilder {
	return s.getQueryBuilder().
		Select(postColumnList...).
		From("Posts").
		Where(sq.Eq{"ChannelId": channelID, "Type": model.PostTypePageComment}).
		Where(sq.Expr(pageIdPropExpr, pageID))
}

// GetPageCommentRoots returns one window of a page's live root comments, ordered
// CreateAt ASC, Id ASC — Id makes the sort key total, which the keyset cursor requires: CreateAt
// alone is millisecond-resolution and not unique, so a tied row could repeat or drop across a
// page boundary.
func (s *Store) GetPageCommentRoots(channelID, pageID string, opts PageCommentListOptions) ([]*mmmodel.Post, error) {
	if err := requirePositiveLimit("PageComment", opts.Limit); err != nil {
		return nil, err
	}
	query := s.pageCommentBaseQuery(channelID, pageID).
		Where(sq.Eq{"DeleteAt": 0, "RootId": ""})
	if opts.Resolved != nil {
		if *opts.Resolved {
			query = query.Where(sq.Expr(resolvedTrueExpr))
		} else {
			query = query.Where(sq.Expr(resolvedNotTrueExpr))
		}
	}
	switch opts.CommentType {
	case model.CommentTypeInline:
		query = query.Where(sq.Expr(inlineTypeExpr))
	case model.CommentTypeFooter:
		query = query.Where(sq.Expr(notInlineTypeExpr))
	}
	if opts.AfterID != "" {
		// A pure value comparison: whether the row the cursor names still exists, never existed,
		// or belongs to another page, the window resumes from the nearest greater row.
		query = query.Where(sq.Expr("(CreateAt, Id) > (?, ?)", opts.AfterCreateAt, opts.AfterID))
	}
	query = query.
		OrderBy("CreateAt ASC", "Id ASC").
		Limit(uint64(opts.Limit)) //nolint:gosec // limit>0 enforced above

	var posts []*mmmodel.Post
	if err := s.selectBuilder(s.db, &posts, query); err != nil {
		return nil, errors.Wrap(err, "failed to list page comment roots")
	}
	return posts, nil
}

// GetPageCommentReplies returns one window of a root comment's live replies, ordered
// CreateAt ASC, Id ASC. Offset paging is kept here (unlike the cursor-paged roots listing)
// because threads are expected to stay small; the page_id predicate closes cross-page probing by
// construction.
func (s *Store) GetPageCommentReplies(channelID, pageID, rootID string, offset, limit int) ([]*mmmodel.Post, error) {
	if err := requirePositiveLimit("PageComment", limit); err != nil {
		return nil, err
	}
	query := s.pageCommentBaseQuery(channelID, pageID).
		Where(sq.Eq{"DeleteAt": 0, "RootId": rootID}).
		OrderBy("CreateAt ASC", "Id ASC")
	query = applyLimitOffset(query, offset, limit)

	var posts []*mmmodel.Post
	if err := s.selectBuilder(s.db, &posts, query); err != nil {
		return nil, errors.Wrap(err, "failed to list page comment replies")
	}
	return posts, nil
}

// GetPageComment resolves a comment through the full identity predicate — id, backing channel,
// and page — so a comment id from another page, another space, or an ordinary chat post reads as
// not-found rather than resolving. Every {comment_id} route goes through this. includeDeleted is
// used only by DELETE's committed-state probe, which must observe the soft-deleted row.
func (s *Store) GetPageComment(commentID, channelID, pageID string, includeDeleted bool) (*mmmodel.Post, error) {
	return s.getPageComment(s.db, commentID, channelID, pageID, includeDeleted)
}

// GetPageCommentTx is GetPageComment executed on tx, for reads that must reuse the connection a
// WithPageCommentLock transaction already holds: an in-lock read through the pool would check out
// a second connection per request while the first is held, exhausting the pool under concurrent
// writers.
func (s *Store) GetPageCommentTx(tx *sqlx.Tx, commentID, channelID, pageID string, includeDeleted bool) (*mmmodel.Post, error) {
	return s.getPageComment(tx, commentID, channelID, pageID, includeDeleted)
}

func (s *Store) getPageComment(e sqlx.ExtContext, commentID, channelID, pageID string, includeDeleted bool) (*mmmodel.Post, error) {
	query := s.pageCommentBaseQuery(channelID, pageID).
		Where(sq.Eq{"Id": commentID})
	if !includeDeleted {
		query = query.Where(sq.Eq{"DeleteAt": 0})
	}
	var post mmmodel.Post
	if err := s.getBuilder(e, &post, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "PageComment", ID: commentID}
		}
		return nil, errors.Wrap(err, "failed to get page comment")
	}
	return &post, nil
}

// GetPageCommentReplyCounts returns the live-reply count per root for rootIDs, in one batched
// query (roots with no live replies are absent from the map). The listing path calls it once per
// page of roots — never once per root — and the single-object paths call it with one element.
func (s *Store) GetPageCommentReplyCounts(rootIDs []string) (map[string]int, error) {
	return s.getPageCommentReplyCounts(s.db, rootIDs)
}

// GetPageCommentReplyCountsTx is GetPageCommentReplyCounts on tx; see GetPageCommentTx for why
// in-lock reads must not go through the pool.
func (s *Store) GetPageCommentReplyCountsTx(tx *sqlx.Tx, rootIDs []string) (map[string]int, error) {
	return s.getPageCommentReplyCounts(tx, rootIDs)
}

func (s *Store) getPageCommentReplyCounts(e sqlx.ExtContext, rootIDs []string) (map[string]int, error) {
	counts := make(map[string]int, len(rootIDs))
	if len(rootIDs) == 0 {
		return counts, nil
	}
	query := s.getQueryBuilder().
		Select("RootId", "COUNT(*) AS ReplyCount").
		From("Posts").
		Where(sq.Eq{"RootId": rootIDs, "Type": model.PostTypePageComment, "DeleteAt": 0}).
		GroupBy("RootId")
	var rows []struct {
		RootId     string
		ReplyCount int
	}
	if err := s.selectBuilder(e, &rows, query); err != nil {
		return nil, errors.Wrap(err, "failed to count page comment replies")
	}
	for _, r := range rows {
		counts[r.RootId] = r.ReplyCount
	}
	return counts, nil
}

// GetMisplacedCommentRoots returns up to limit ids of comment roots tied (by the page_id prop)
// to a page of the space but stored on a different channel — the state a page move leaves
// behind when its comment re-home fails part-way. Soft-deleted pages and comments are included:
// a restored page returns with its whole comment tree, so stranded rows under deleted entities
// still need repair. Ordered by id, so a sweeping caller can tell a shrinking backlog from one
// that is not converging.
// A non-empty sourceChannelIDs narrows the read to rows still on those channels, which lets
// the routine after-a-move sweep ride the channel index rather than scan the space's whole tie
// set; an empty slice scans it all and is reserved for the explicit repair path, where the
// stragglers' channels are unknown.
func (s *Store) GetMisplacedCommentRoots(spaceID, channelID string, sourceChannelIDs []string, limit int) ([]string, error) {
	if err := requirePositiveLimit("PageComment", limit); err != nil {
		return nil, err
	}
	query := s.getQueryBuilder().
		Select("c.Id").
		From("Posts c").
		Join("DOCS_Page p ON p.Id = c." + pageIdProp).
		Where(sq.Eq{"p.SpaceId": spaceID, "p.OriginalId": "", "c.Type": model.PostTypePageComment, "c.RootId": ""}).
		// A root's edit-history row carries RootId='' with OriginalId set, so a roots-only
		// predicate admits it — and the move primitive then rejects the whole batch as
		// non-root input. History rows re-home through the primitive's OriginalId leg when
		// their root moves; they are never named as inputs.
		Where(sq.Eq{"c.OriginalId": ""}).
		Where(sq.NotEq{"c.ChannelId": channelID}).
		OrderBy("c.Id ASC").
		Limit(uint64(limit)) //nolint:gosec // limit>0 enforced above
	if len(sourceChannelIDs) > 0 {
		query = query.Where(sq.Eq{"c.ChannelId": sourceChannelIDs})
	}

	var ids []string
	if err := s.selectBuilder(s.db, &ids, query); err != nil {
		return nil, errors.Wrap(err, "failed to list misplaced comment roots")
	}
	return ids, nil
}

// LockedPage is the page identity WithPageCommentLock hands to its callback, read under the lock
// and therefore authoritative for the duration of the transaction.
type LockedPage struct {
	SpaceId   string
	ChannelId string
}

// WithPageCommentLock serializes a comment write against a concurrent move (or delete) of its
// page, and — when rootID is non-empty — against concurrent writes on the same comment thread.
//
// It begins a transaction, takes FOR SHARE on the live page row, takes the transaction-scoped
// advisory lock on rootID when one is given, runs fn, and commits. MovePageToSpace takes FOR
// UPDATE on the same row, so a move blocks until the comment write commits and vice versa: the
// space the caller was authorized against is the space the write lands in. The DeleteAt/OriginalId
// predicate is what makes a concurrently deleted page read as not-found rather than locking a
// soft-deleted row — under READ COMMITTED, Postgres re-evaluates the WHERE clause after a lock
// wait, so a delete that commits while the lock is queued makes the row fall out.
//
// The lock order is fixed here rather than by callers and is deadlock-free against the move path:
// both paths acquire their row locks before any advisory key — the move takes space rows FOR
// UPDATE, then page rows, then (when it repositions) the sibling-group advisory key — and the two
// advisory keyspaces are disjoint: the sibling-group key is channelID+":"+parentID while this one
// is a bare post id, which never contains a colon. This path takes no space row.
//
// fn receives the transaction so its reads ride the connection the lock already holds (see
// GetPageCommentTx), and the locks are held for exactly as long as fn runs — including across any
// plugin-API call fn makes. A hung call therefore blocks every move and comment mutation of this
// page until it returns, and holds one pooled connection while it does; that visible stall is the
// accepted alternative to releasing the lock while the call may still be writing.
func (s *Store) WithPageCommentLock(pageID, rootID string, fn func(tx *sqlx.Tx, page LockedPage) error) (err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "failed to begin page comment lock transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	sel := s.getQueryBuilder().
		Select("SpaceId", "ChannelId").
		From("DOCS_Page").
		Where(sq.Eq{"Id": pageID}).
		Where(liveNonSnapshotFilter("")).
		Suffix("FOR SHARE")
	var locked LockedPage
	if e := s.getBuilder(tx, &locked, sel); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Page", ID: pageID}
		}
		return errors.Wrap(e, "failed to lock page for comment write")
	}

	if rootID != "" {
		if e := s.advisoryXactLock(tx, rootID); e != nil {
			return errors.Wrap(e, "failed to take comment root advisory lock")
		}
	}

	if e := fn(tx, locked); e != nil {
		return e
	}

	if e := tx.Commit(); e != nil {
		return errors.Wrap(e, "failed to commit page comment lock transaction")
	}
	return nil
}
