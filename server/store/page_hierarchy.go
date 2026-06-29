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

// MaxRowsPerQuery caps any unpaginated "return all" listing (limit<=0) — pages, spaces, or
// drafts — so it cannot load an unbounded number of rows. This is a defensive backstop, not a
// pagination policy; the per-page default/maximum (cf. core's 60/200, server/channels/web/params.go)
// belongs in the HTTP API layer once the REST endpoints are wired.
const MaxRowsPerQuery = 5000

// pageColListP is the comma-joined "p."-prefixed column list, precomputed for the
// hierarchy CTE final SELECTs.
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
// The full-row caller passes MaxPageHierarchyDepth+2 so it can emit one row beyond the
// cap, letting GetPageAncestors distinguish "at limit" from "truncated".
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
// excluding the root node and returning full page columns plus the node's depth. Uses the SQL
// CYCLE clause (PG 14+), guaranteed by the plugin's min_server_version constraint.
// CYCLE stops recursion on a ParentId loop; NOT is_cycle drops the sentinel row.
// sort_path/create_path/id_path accumulate each ancestor's ordering keys so the final
// ORDER BY yields a pre-order depth-first walk with sibling order matching GetPageChildren
// (SortOrder, CreateAt, Id). depth counts edges below the requested page: the root is seeded
// at 0, so a direct child is depth 1. The recursion runs one level past MaxPageHierarchyDepth
// so a subtree deeper than the cap emits a depth > MaxPageHierarchyDepth row, letting
// GetPageDescendants distinguish "at the cap" from "truncated" instead of silently dropping.
func computeDescendantsCTE() string {
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
	return cte + `
	SELECT ` + pageColListP + `, d.depth
	FROM descendants d
	INNER JOIN DOCS_Page p ON p.Id = d.Id
	WHERE d.Id != $1 AND NOT d.is_cycle AND p.DeleteAt = 0
	ORDER BY d.sort_path, d.create_path, d.id_path`
}

// computeAncestorsCTE generates the recursive CTE that walks the parent chain above a page,
// excluding the root node and returning full page columns. The +2 lets the chain emit
// MaxPageHierarchyDepth+1 rows; GetPageAncestors errors when it receives more than
// MaxPageHierarchyDepth (see ancestorsRecursiveCTE).
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
