// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package testutil holds helpers shared across the server's test packages.
package testutil

import (
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/store/storetest"
)

// OpenTestDB provisions an isolated Postgres database for a test using storetest.MakeSqlSettings
// and registers t.Cleanup to drop it when the test ends. A missing or unreachable database
// fails the test rather than skipping it.
func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()

	settings := storetest.MakeSqlSettings(mmmodel.DatabaseDriverPostgres)
	t.Cleanup(func() { storetest.CleanupSqlSettings(settings) })

	db, err := sql.Open(*settings.DriverName, *settings.DataSource)
	require.NoError(t, err, "open test postgres")
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping(), "ping test postgres")

	return db
}
