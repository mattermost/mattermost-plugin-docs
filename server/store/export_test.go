// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// export_test.go exposes internal store methods that are needed only by package-level
// tests (store_test package). Nothing in here is accessible from production code.

package store

import (
	"database/sql"

	sq "github.com/mattermost/squirrel"
)

// RawExecForTest executes a raw SQL query directly on the underlying DB handle.
// It is intentionally exported only for tests (via the _test.go filename suffix) so that
// test helpers can inject data-corruption scenarios (e.g. CTE cycle detection) without
// going through the public API's validation guards.
func (s *Store) RawExecForTest(query string, args ...any) (sql.Result, error) {
	return s.exec(s.db, query, args...)
}

// QueryBuilderForTest returns the store's configured squirrel builder, so test setup can
// construct queries the same way the store does (correct placeholder format).
func (s *Store) QueryBuilderForTest() sq.StatementBuilderType {
	return s.getQueryBuilder()
}

// ExecBuilderForTest executes a squirrel builder on the underlying DB handle. Use it (with
// QueryBuilderForTest) for well-formed test setup writes that the public API does not expose —
// e.g. setting a page's SortOrder, which is intentionally not a generic-patch field. For
// deliberately malformed/corrupt rows, use RawExecForTest instead.
func (s *Store) ExecBuilderForTest(b sq.Sqlizer) (sql.Result, error) {
	return s.execBuilder(s.db, b)
}
