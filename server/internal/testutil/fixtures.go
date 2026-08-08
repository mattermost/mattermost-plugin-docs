// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// UncappedMaxDepth is the maxDepth passed to CreatePage by tests that aren't exercising the
// depth cap itself, chosen well past MaxPageHierarchyDepth so it never interferes with chains
// built to test the read-side CTE limit.
const UncappedMaxDepth = store.MaxPageHierarchyDepth + 10

// OpenTestStore opens an isolated Postgres schema (OpenTestDB), builds a Store over it, and runs
// migrations, failing the test on any error. The store is closed and the schema dropped via
// t.Cleanup. The schema-scoped *sql.DB is returned alongside the store for tests that seed states
// the public store API forbids.
func OpenTestStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()

	db := OpenTestDB(t)

	s, err := store.New(db, "postgres", nil)
	require.NoError(t, err, "create store")
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.RunMigrations(), "run migrations")

	// The team listing and the membership guards join core's ChannelMembers, TeamMembers, and
	// Channels tables, but the isolated test database contains only plugin tables. Create minimal
	// stand-ins with the columns those queries read; production never creates these tables — core
	// owns them there. SchemeAdmin is nullable, as in core's schema, so seeding through
	// MustAddChannelMember exercises the queries' NULL handling.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS ChannelMembers (
		ChannelId varchar(26) NOT NULL,
		UserId varchar(26) NOT NULL,
		SchemeAdmin boolean,
		PRIMARY KEY (ChannelId, UserId)
	)`)
	require.NoError(t, err, "create ChannelMembers stand-in")

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS TeamMembers (
		TeamId varchar(26) NOT NULL,
		UserId varchar(26) NOT NULL,
		DeleteAt bigint NOT NULL DEFAULT 0,
		PRIMARY KEY (TeamId, UserId)
	)`)
	require.NoError(t, err, "create TeamMembers stand-in")

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS Channels (
		Id varchar(26) NOT NULL,
		TeamId varchar(26) NOT NULL,
		PRIMARY KEY (Id)
	)`)
	require.NoError(t, err, "create Channels stand-in")

	return s, db
}

// MustAddChannelMember seeds a ChannelMembers row (see the stand-in table in OpenTestStore) so
// store queries that resolve visibility through channel membership can see channelID as userID.
// SchemeAdmin is left NULL, as core's schema allows.
func MustAddChannelMember(t *testing.T, db *sql.DB, channelID, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2)`, channelID, userID)
	require.NoError(t, err)
}

// MustAddChannelAdmin seeds a ChannelMembers row with SchemeAdmin set, for queries that
// distinguish admins from plain members.
func MustAddChannelAdmin(t *testing.T, db *sql.DB, channelID, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO ChannelMembers (ChannelId, UserId, SchemeAdmin) VALUES ($1, $2, TRUE)`, channelID, userID)
	require.NoError(t, err)
}

// MustAddTeamMember seeds a TeamMembers row (see the stand-in table in OpenTestStore). Core keeps
// removed team members as rows with DeleteAt set, so pass a non-zero deleteAt to seed a former
// member.
func MustAddTeamMember(t *testing.T, db *sql.DB, teamID, userID string, deleteAt int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO TeamMembers (TeamId, UserId, DeleteAt) VALUES ($1, $2, $3)`, teamID, userID, deleteAt)
	require.NoError(t, err)
}

// MustAddChannel seeds a Channels row (see the stand-in table in OpenTestStore) so queries that
// resolve a channel's team through the Channels table can see it.
func MustAddChannel(t *testing.T, db *sql.DB, channelID, teamID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO Channels (Id, TeamId) VALUES ($1, $2)`, channelID, teamID)
	require.NoError(t, err)
}

// NewSpace returns the standard test space fixture backed by channelID in teamID, with a random
// creator.
func NewSpace(channelID, teamID string) *model.Space {
	return &model.Space{
		ChannelId:  channelID,
		TeamId:     teamID,
		CreatorId:  mmmodel.NewId(),
		Title:      "Test Space",
		ViewAccess: model.ViewAccessOpen,
	}
}

// NewPage returns the standard live test page fixture (an empty TipTap doc body), not yet saved.
func NewPage(spaceID, channelID, userID, parentID string) *model.Page {
	return &model.Page{
		SpaceId:   spaceID,
		ChannelId: channelID,
		UserId:    userID,
		ParentId:  parentID,
		Type:      model.PageTypePage,
		Title:     "Test Page",
		Body:      `{"type":"doc","content":[]}`,
	}
}

// MustCreateSpace saves the standard space fixture (NewSpace) through the store, failing the
// test on error.
func MustCreateSpace(t *testing.T, s *store.Store, channelID, teamID string) *model.Space {
	t.Helper()
	space, err := s.CreateSpace(NewSpace(channelID, teamID))
	require.NoError(t, err)
	return space
}

// MustCreatePage saves the standard page fixture (NewPage) through the store, failing the test
// on error. The depth cap is bypassed (UncappedMaxDepth): some callers build chains deeper than
// the app-layer cap to exercise store.MaxPageHierarchyDepth instead.
func MustCreatePage(t *testing.T, s *store.Store, spaceID, channelID, userID, parentID string) *model.Page {
	t.Helper()
	page, err := s.CreatePage(NewPage(spaceID, channelID, userID, parentID), UncappedMaxDepth)
	require.NoError(t, err)
	return page
}
