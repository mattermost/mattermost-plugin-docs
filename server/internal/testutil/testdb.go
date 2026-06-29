// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"database/sql"
	"os"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// defaultTestDSN matches the Mattermost convention (storetest.MakeSqlSettings):
// the standard local dev Postgres. Tests default to it rather than skipping, so a
// run always attempts the DB and fails — never skips — when none is reachable.
const defaultTestDSN = "postgres://mmuser:mostest@localhost:5432/mattermost_test?sslmode=disable" //nolint:gosec // G101: well-known local test DSN (same as MM-core storetest), not a secret

// OpenSchemaDB resolves the test DSN from MM_SQLSETTINGS_DATASOURCE or TEST_DATABASE_DSN
// (defaulting to the standard local dev Postgres), creates an isolated schema named
// schemaPrefix+<id> for this run, and returns a schema-scoped *sql.DB. The schema is dropped
// via t.Cleanup so parallel package runs never share tables. Tests never skip: a missing
// database fails the connection checks rather than passing on a skip.
func OpenSchemaDB(t *testing.T, schemaPrefix string) *sql.DB {
	t.Helper()

	dsn := os.Getenv("MM_SQLSETTINGS_DATASOURCE")
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_DSN")
	}
	if dsn == "" {
		dsn = defaultTestDSN
	}

	schema := schemaPrefix + mmmodel.NewId()

	// Connect to the base DSN to create the schema.
	baseDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open base postgres")
	t.Cleanup(func() { _ = baseDB.Close() })
	require.NoError(t, baseDB.Ping(), "ping base postgres")
	_, err = baseDB.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err, "create test schema")
	// Register schema teardown immediately so it still runs if a later setup step fails.
	t.Cleanup(func() {
		// Drop the isolated schema using a fresh connection so it always runs.
		dropDB, dropErr := sql.Open("postgres", dsn)
		if dropErr == nil {
			_, _ = dropDB.Exec("DROP SCHEMA IF EXISTS " + pq.QuoteIdentifier(schema) + " CASCADE")
			_ = dropDB.Close()
		}
	})

	// Rebuild DSN with search_path set so every pooled connection uses the schema.
	db, err := sql.Open("postgres", AddSearchPath(dsn, schema))
	require.NoError(t, err, "open schema-scoped postgres")
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping(), "ping schema-scoped postgres")

	return db
}
