// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// export_test.go exposes internal store methods that are needed only by package-level
// tests (store_test package). Nothing in here is accessible from production code.

package store

import "database/sql"

// RawExecForTest executes a raw SQL query directly on the underlying DB handle.
// It is intentionally exported only for tests (via the _test.go build tag) so that
// test helpers can inject data-corruption scenarios (e.g. CTE cycle detection) without
// going through the public API's validation guards.
func (s *Store) RawExecForTest(query string, args ...any) (sql.Result, error) {
	return s.exec(s.db, query, args...)
}
