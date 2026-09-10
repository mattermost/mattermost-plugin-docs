// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"database/sql"
	"encoding/json"
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

	// The team listing joins core's ChannelMembers table for visibility, but the isolated test
	// database contains only plugin tables. Create a minimal stand-in with the columns the join
	// reads; production never creates this table — core owns it there.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS ChannelMembers (
		ChannelId varchar(26) NOT NULL,
		UserId varchar(26) NOT NULL,
		PRIMARY KEY (ChannelId, UserId)
	)`)
	require.NoError(t, err, "create ChannelMembers stand-in")

	// The comment store reads core's Posts table (a page comment is a Posts row); the isolated
	// test database contains only plugin tables, so create a minimal stand-in with the columns
	// the comment queries read. Production never creates this table — core owns it there.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS Posts (
		Id varchar(26) PRIMARY KEY,
		CreateAt bigint NOT NULL DEFAULT 0,
		UpdateAt bigint NOT NULL DEFAULT 0,
		EditAt bigint NOT NULL DEFAULT 0,
		DeleteAt bigint NOT NULL DEFAULT 0,
		UserId varchar(26) NOT NULL,
		ChannelId varchar(26) NOT NULL,
		RootId varchar(26) NOT NULL DEFAULT '',
		OriginalId varchar(26) NOT NULL DEFAULT '',
		Message varchar(65535) NOT NULL DEFAULT '',
		Type varchar(26) NOT NULL DEFAULT '',
		Props jsonb
	)`)
	require.NoError(t, err, "create Posts stand-in")

	// The retention sweep probes core's RetentionPoliciesChannels; same stand-in rules as
	// above. The primary key mirrors core's: the channel alone, so a channel carries at most
	// one policy assignment — the constraint the sweep's re-home behavior exists for.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS RetentionPoliciesChannels (
		PolicyId varchar(26) NOT NULL,
		ChannelId varchar(26) NOT NULL,
		PRIMARY KEY (ChannelId)
	)`)
	require.NoError(t, err, "create RetentionPoliciesChannels stand-in")

	return s, db
}

// MustInsertPost seeds a Posts row into the stand-in table (see OpenTestStore) so comment store
// queries can see it. Zero CreateAt/UpdateAt are stamped with the current millis, mirroring the
// platform's PreSave.
func MustInsertPost(t *testing.T, db *sql.DB, post *mmmodel.Post) *mmmodel.Post {
	t.Helper()
	if post.Id == "" {
		post.Id = mmmodel.NewId()
	}
	now := mmmodel.GetMillis()
	if post.CreateAt == 0 {
		post.CreateAt = now
	}
	if post.UpdateAt == 0 {
		post.UpdateAt = post.CreateAt
	}
	props, err := json.Marshal(post.GetProps())
	require.NoError(t, err, "marshal post props")
	_, err = db.Exec(`INSERT INTO Posts (Id, CreateAt, UpdateAt, EditAt, DeleteAt, UserId, ChannelId, RootId, OriginalId, Message, Type, Props)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		post.Id, post.CreateAt, post.UpdateAt, post.EditAt, post.DeleteAt, post.UserId, post.ChannelId, post.RootId, post.OriginalId, post.Message, post.Type, props)
	require.NoError(t, err, "insert Posts stand-in row")
	return post
}

// MustAddChannelMember seeds a ChannelMembers row (see the stand-in table in OpenTestStore) so
// store queries that resolve visibility through channel membership can see channelID as userID.
func MustAddChannelMember(t *testing.T, db *sql.DB, channelID, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2)`, channelID, userID)
	require.NoError(t, err)
}

// NewSpace returns the standard test space fixture backed by channelID in teamID, with a random
// creator.
func NewSpace(channelID, teamID string) *model.Space {
	return &model.Space{
		ChannelId: channelID,
		TeamId:    teamID,
		CreatorId: mmmodel.NewId(),
		Title:     "Test Space",
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
