// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"
	"strings"
)

// MaxPageHierarchyDepth limits CTE recursion depth.
const MaxPageHierarchyDepth = 50

// MaxPageDescendantsLimit is the maximum number of descendants returned by GetPageDescendants.
const MaxPageDescendantsLimit = 5000

// MaxPageSiblingsLimit caps a single parent's direct live children. reindexSiblingGroup
// renumbers the whole sibling group in one statement under a held lock on every explicit
// reposition, so an unbounded group would let one request build an arbitrarily large statement
// while blocking concurrent creates/moves in the group; nextSortOrder and reindexSiblingGroup both
// enforce this cap at the points where a group gains a member. Matches MaxPageDescendantsLimit so a
// full-fan-out subtree (root + all direct children) still fits the descendant cap.
const MaxPageSiblingsLimit = MaxPageDescendantsLimit

// MaxRowsPerQuery caps GetDraftsForSpace's unpaginated "return all" read (limit<=0) so it cannot
// load an unbounded number of rows. Pages and spaces listings reject limit<=0 outright via
// requirePositiveLimit instead of falling back to this cap; the HTTP API layer clamps per_page to
// [1, perPageMaximum] before it ever reaches the store (cf. core's 60/200, server/channels/web/params.go).
const MaxRowsPerQuery = 5000

var pageColListP = strings.Join(pageColumnsP, ", ")

// These CTEs are built once at package init (inputs are compile-time constants) rather than on
// every query. pageAncestorCountCTE counts ancestors without hydrating full rows, for the
// create-time depth check.
var (
	pageDescendantsCTE = computeDescendantsCTE()

	// pageDescendantIDsCTE/pageAncestorIDsCTE project only {Id, ParentId}/{Id} instead of full page
	// columns, for callers (MovePage/MovePageToSpace's cycle and depth-cap pre-checks) that don't
	// need page content — avoids pulling Body/SearchText/Props for a subtree just to compute a depth.
	pageDescendantIDsCTE = computeDescendantIDsCTE()
	pageAncestorIDsCTE   = ancestorsRecursiveCTE(MaxPageHierarchyDepth+2) + `
	SELECT a.Id
	FROM ancestors a
	INNER JOIN DOCS_Page p ON p.Id = a.Id
	WHERE a.Id != $1 AND NOT a.is_cycle AND p.DeleteAt = 0`

	pageAncestorCountCTE = ancestorsRecursiveCTE(MaxPageHierarchyDepth) + `
	SELECT COUNT(*)
	FROM ancestors a
	WHERE a.Id != $1 AND NOT a.is_cycle`

	// moveAncestorsCTE walks a page's parent chain upward, excluding snapshot rows
	// (OriginalId != ''), bounded by MaxPageHierarchyDepth. Callers run it within the move
	// transaction so it observes locked, uncommitted state, and append their own SELECT (which
	// must filter NOT is_cycle to drop the cycle sentinel row). The CYCLE clause matches the
	// other recursive CTEs in this file (ancestorsRecursiveCTE/computeDescendantsCTE) so a
	// corrupted ParentId loop is broken explicitly rather than relying on the depth bound alone.
	// Shared by the cycle guard (pageHasAncestor) and the depth check (pageDepth).
	moveAncestorsCTE = fmt.Sprintf(`
	WITH RECURSIVE ancestors AS (
		SELECT Id, ParentId, 1 AS depth FROM DOCS_Page WHERE Id = $1 AND DeleteAt = 0 AND OriginalId = ''
		UNION ALL
		SELECT p.Id, p.ParentId, a.depth + 1 FROM DOCS_Page p
		INNER JOIN ancestors a ON p.Id = a.ParentId
		WHERE p.DeleteAt = 0 AND p.OriginalId = '' AND a.depth <= %d
	) CYCLE Id SET is_cycle USING cycle_path`, MaxPageHierarchyDepth)

	// pageSubtreeCTE walks a page's live subtree downward (root at depth 0), bounded by
	// MaxPageHierarchyDepth so a subtree one level past the cap still emits a row rather than being
	// silently truncated. Callers append their own SELECT. Shared by the move-to-space subtree
	// collection (MovePageToSpace) and the under-lock subtree depth check (pageSubtreeMaxDepth).
	pageSubtreeCTE = fmt.Sprintf(`
	WITH RECURSIVE page_subtree AS (
		SELECT Id, 0 AS depth FROM DOCS_Page WHERE Id = $1 AND DeleteAt = 0 AND OriginalId = ''
		UNION ALL
		SELECT p.Id, ps.depth + 1 FROM DOCS_Page p
		INNER JOIN page_subtree ps ON p.ParentId = ps.Id
		WHERE p.DeleteAt = 0 AND p.OriginalId = '' AND ps.depth <= %d
	) CYCLE Id SET is_cycle USING cycle_path`, MaxPageHierarchyDepth)
)

// ancestorsRecursiveCTE returns the recursive WITH clause that walks the parent chain
// to the root while depth < maxDepth. Shared by the id-only and count-only queries.
// The id-only caller (GetPageAncestorIDs) passes maxDepth = MaxPageHierarchyDepth+2:
// depth is 1-indexed at the queried page itself (excluded from ancestor output), accounting
// for +1, and the second +1 lets the chain emit one row beyond MaxPageHierarchyDepth so
// GetPageAncestorIDs can distinguish "at limit" from "truncated".
func ancestorsRecursiveCTE(maxDepth int) string {
	return fmt.Sprintf(`
	WITH RECURSIVE ancestors AS (
		SELECT Id, ParentId, 1 AS depth
		FROM DOCS_Page WHERE Id = $1 AND DeleteAt = 0
		UNION ALL
		SELECT p.Id, p.ParentId, a.depth + 1
		FROM DOCS_Page p
		INNER JOIN ancestors a ON p.Id = a.ParentId
		WHERE a.ParentId != ''
		  AND p.DeleteAt = 0 AND a.depth < %d
	) CYCLE Id SET is_cycle USING cycle_path`, maxDepth)
}

// computeDescendantsCTE generates the recursive CTE that walks the subtree below a page,
// excluding the root node and returning full page columns plus the node's depth. depth counts
// edges below the requested page: the root is seeded at 0, so a direct child is depth 1. The
// recursion runs one level past MaxPageHierarchyDepth so a subtree deeper than the cap emits a
// depth > MaxPageHierarchyDepth row, letting GetPageDescendants distinguish "at the cap" from
// "truncated" instead of silently dropping. Uses the SQL CYCLE clause (requires PostgreSQL
// 14+); the plugin does not verify the deployment's Postgres version.
func computeDescendantsCTE() string {
	// sort_path/create_path/id_path accumulate each ancestor's ordering keys so the ORDER BY
	// below yields a pre-order depth-first walk with sibling order matching GetPageChildren
	// (SortOrder, CreateAt, Id).
	return descendantsRecursiveCTE() + `
	SELECT ` + pageColListP + `, d.depth
	FROM descendants d
	INNER JOIN DOCS_Page p ON p.Id = d.Id
	WHERE d.Id != $1 AND NOT d.is_cycle AND p.DeleteAt = 0
	ORDER BY d.sort_path, d.create_path, d.id_path`
}

// computeDescendantIDsCTE is the {Id, ParentId, depth}-only counterpart to computeDescendantsCTE,
// for callers that don't need full page content — same recursion, cap, and pre-order guarantee.
func computeDescendantIDsCTE() string {
	return descendantsRecursiveCTE() + `
	SELECT d.Id, d.ParentId, d.depth
	FROM descendants d
	INNER JOIN DOCS_Page p ON p.Id = d.Id
	WHERE d.Id != $1 AND NOT d.is_cycle AND p.DeleteAt = 0
	ORDER BY d.sort_path, d.create_path, d.id_path`
}

// descendantsRecursiveCTE returns the recursive WITH clause that walks the subtree below a page,
// excluding the root node, bounded one level past MaxPageHierarchyDepth so an over-deep subtree
// emits a depth > MaxPageHierarchyDepth row instead of being silently truncated. Shared by the
// full-row and id-only queries. Uses the SQL CYCLE clause (requires PostgreSQL 14+); the plugin
// does not verify the deployment's Postgres version.
func descendantsRecursiveCTE() string {
	// CYCLE stops recursion on a ParentId loop; NOT is_cycle (in the caller's SELECT) drops the
	// sentinel row.
	return fmt.Sprintf(`
		WITH RECURSIVE descendants AS (
			SELECT Id, ParentId, 0 AS depth,
				ARRAY[]::bigint[] AS sort_path,
				ARRAY[]::bigint[] AS create_path,
				ARRAY[]::text[] AS id_path
			FROM DOCS_Page WHERE Id = $1 AND DeleteAt = 0
			UNION ALL
			SELECT p.Id, p.ParentId, d.depth + 1,
				d.sort_path || p.SortOrder,
				d.create_path || p.CreateAt,
				d.id_path || p.Id
			FROM DOCS_Page p
			INNER JOIN descendants d ON p.ParentId = d.Id
			WHERE p.DeleteAt = 0 AND d.depth < %d
		) CYCLE Id SET is_cycle USING cycle_path`, MaxPageHierarchyDepth+1)
}
