// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"
	"strings"
)

// pageHierarchyCTEDirection specifies the traversal direction for page hierarchies.
type pageHierarchyCTEDirection string

const (
	pageHierarchyDescendants pageHierarchyCTEDirection = "descendants"
	pageHierarchyAncestors   pageHierarchyCTEDirection = "ancestors"
)

// MaxPageHierarchyDepth limits CTE recursion depth.
const MaxPageHierarchyDepth = 50

// MaxPageDescendantsLimit is the maximum number of descendants returned by GetPageDescendants.
const MaxPageDescendantsLimit = 5000

// pageDescendantsCTE, pageAncestorsCTE, and pageAncestorCountCTE are built once at
// package init (inputs are compile-time constants) rather than on every query.
// pageAncestorCountCTE counts ancestors without hydrating full rows, used by the
// create-time depth check.
var (
	pageDescendantsCTE = computePageHierarchyCTE(pageHierarchyDescendants)
	pageAncestorsCTE   = computePageHierarchyCTE(pageHierarchyAncestors)

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

// computePageHierarchyCTE generates a recursive CTE for hierarchy traversal, excluding
// the root node and returning full page columns. Uses the SQL CYCLE clause (PG 14+),
// which is guaranteed by the plugin's min_server_version constraint.
func computePageHierarchyCTE(direction pageHierarchyCTEDirection) string {
	pageColList := strings.Join(pageColumnsP, ", ")

	switch direction {
	case pageHierarchyDescendants:
		// CYCLE stops recursion on a ParentId loop; NOT is_cycle drops the sentinel row.
		// sort_path/create_path/id_path accumulate each ancestor's ordering keys so
		// the final ORDER BY yields a pre-order depth-first walk with sibling order
		// matching GetPageChildren (SortOrder, CreateAt, Id).
		cte := fmt.Sprintf(`
		WITH RECURSIVE descendants AS (
			SELECT Id, ParentId, 1 AS depth,
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
		) CYCLE Id SET is_cycle USING cycle_path`, MaxPageHierarchyDepth)
		return cte + `
	SELECT ` + pageColList + `
	FROM descendants d
	INNER JOIN DOCS_Page p ON p.Id = d.Id
	WHERE d.Id != $1 AND NOT d.is_cycle AND p.DeleteAt = 0
	ORDER BY d.sort_path, d.create_path, d.id_path`

	case pageHierarchyAncestors:
		// +2 lets the chain emit MaxPageHierarchyDepth+1 rows; GetPageAncestors errors
		// when it receives more than MaxPageHierarchyDepth (see ancestorsRecursiveCTE).
		return ancestorsRecursiveCTE(MaxPageHierarchyDepth+2) + `
	SELECT ` + pageColList + `
	FROM ancestors a
	INNER JOIN DOCS_Page p ON p.Id = a.Id
	WHERE a.Id != $1 AND NOT a.is_cycle AND p.DeleteAt = 0
	-- Order by chain position (depth descending = root first), not CreateAt: ancestor
	-- depth is the canonical order of a parent chain, whereas CreateAt can invert it
	-- after an import or a same-millisecond create.
	ORDER BY a.depth DESC, p.Id`

	default:
		panic(fmt.Sprintf("computePageHierarchyCTE: unknown direction %q", direction))
	}
}
