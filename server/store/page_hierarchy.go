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

// MaxRowsPerQuery caps GetDraftsForSpace, which has no pagination parameters, so it cannot
// load an unbounded number of rows for a single user/space pair.
const MaxRowsPerQuery = 5000

var pageColListP = strings.Join(pageColumnsP, ", ")

// These CTEs are built once at package init (inputs are compile-time constants) rather than on
// every query. pageAncestorCountCTE counts ancestors without hydrating full rows, for the
// create-time depth check.
var (
	pageDescendantsCTE = computeDescendantsCTE()
	pageAncestorsCTE   = computeAncestorsCTE()

	pageAncestorCountCTE = ancestorsRecursiveCTE(MaxPageHierarchyDepth) + `
	SELECT COUNT(*)
	FROM ancestors a
	WHERE a.Id != $1 AND NOT a.is_cycle`
)

// ancestorsRecursiveCTE returns the recursive WITH clause that walks the parent chain
// to the root while depth < maxDepth. Shared by the full-row and count-only queries.
// The full-row caller passes maxDepth = MaxPageHierarchyDepth+2: depth is 1-indexed at
// the queried page itself (excluded from ancestor output), accounting for +1, and the
// second +1 lets the chain emit one row beyond MaxPageHierarchyDepth so GetPageAncestors
// can distinguish "at limit" from "truncated".
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
	// CYCLE stops recursion on a ParentId loop; NOT is_cycle (below) drops the sentinel row.
	cte := fmt.Sprintf(`
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
	// sort_path/create_path/id_path accumulate each ancestor's ordering keys so the ORDER BY
	// below yields a pre-order depth-first walk with sibling order matching GetPageChildren
	// (SortOrder, CreateAt, Id).
	return cte + `
	SELECT ` + pageColListP + `, d.depth
	FROM descendants d
	INNER JOIN DOCS_Page p ON p.Id = d.Id
	WHERE d.Id != $1 AND NOT d.is_cycle AND p.DeleteAt = 0
	ORDER BY d.sort_path, d.create_path, d.id_path`
}

// computeAncestorsCTE generates the recursive CTE that walks the parent chain above a page,
// excluding the root node and returning full page columns (see ancestorsRecursiveCTE for the
// +2 derivation). GetPageAncestors errors when it receives more than MaxPageHierarchyDepth rows.
func computeAncestorsCTE() string {
	return ancestorsRecursiveCTE(MaxPageHierarchyDepth+2) + `
	SELECT ` + pageColListP + `
	FROM ancestors a
	INNER JOIN DOCS_Page p ON p.Id = a.Id
	WHERE a.Id != $1 AND NOT a.is_cycle AND p.DeleteAt = 0
	-- Order by chain position (depth descending = root first), not CreateAt: ancestor
	-- depth is the canonical order of a parent chain, whereas CreateAt can invert it
	-- after an import or a same-millisecond create.
	ORDER BY a.depth DESC, p.Id`
}
