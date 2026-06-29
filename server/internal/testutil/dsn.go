// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package testutil holds helpers shared across the server's test packages.
package testutil

import "net/url"

// AddSearchPath returns dsn with the Postgres search_path set to schema.
// Handles both URL-form DSNs (postgres://…) and libpq key=value DSNs.
func AddSearchPath(dsn, schema string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return dsn + " options='-c search_path=" + schema + "'"
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
