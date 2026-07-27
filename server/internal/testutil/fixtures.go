// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"database/sql"
	"strings"
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

	// The RBAC capability model resolves a space's default capability set through its backing
	// channel's core Channels/Schemes/Roles rows (GetSchemeRolesForChannel, GetSchemeIDByName,
	// GetRolePermissionsByName) — core tables owned by the paired core branch's migration in
	// production, absent from this plugin-only isolated schema. Stand them in with just the
	// columns the plugin store queries, and seed the three preset schemes the core seeding
	// migration creates in production, so scheme-resolving app methods work against test-created
	// spaces.
	mustCreateSchemeStandInTables(t, db)
	mustSeedSpaceSchemes(t, db)

	return s, db
}

// mustCreateSchemeStandInTables creates the core Channels/Schemes/Roles stand-in tables with only
// the columns the plugin store reads or writes (see space_store.go/scheme_store.go).
func mustCreateSchemeStandInTables(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS Channels (
		Id varchar(26) PRIMARY KEY,
		SchemeId varchar(26)
	)`)
	require.NoError(t, err, "create Channels stand-in")

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS Schemes (
		Id varchar(26) PRIMARY KEY,
		Name varchar(64) UNIQUE NOT NULL,
		DisplayName varchar(128) NOT NULL DEFAULT '',
		Description varchar(1024) NOT NULL DEFAULT '',
		Scope varchar(32) NOT NULL DEFAULT '',
		DefaultTeamAdminRole varchar(64) NOT NULL DEFAULT '',
		DefaultTeamUserRole varchar(64) NOT NULL DEFAULT '',
		DefaultTeamGuestRole varchar(64) NOT NULL DEFAULT '',
		DefaultChannelAdminRole varchar(64) NOT NULL DEFAULT '',
		DefaultChannelUserRole varchar(64) NOT NULL DEFAULT '',
		DefaultChannelGuestRole varchar(64) NOT NULL DEFAULT '',
		CreateAt bigint NOT NULL DEFAULT 0,
		UpdateAt bigint NOT NULL DEFAULT 0,
		DeleteAt bigint NOT NULL DEFAULT 0,
		DefaultPlaybookAdminRole varchar(64) NOT NULL DEFAULT '',
		DefaultPlaybookMemberRole varchar(64) NOT NULL DEFAULT '',
		DefaultRunAdminRole varchar(64) NOT NULL DEFAULT '',
		DefaultRunMemberRole varchar(64) NOT NULL DEFAULT ''
	)`)
	require.NoError(t, err, "create Schemes stand-in")

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS Roles (
		Id varchar(26) PRIMARY KEY,
		Name varchar(64) UNIQUE NOT NULL,
		DisplayName varchar(128) NOT NULL DEFAULT '',
		Description varchar(1024) NOT NULL DEFAULT '',
		Permissions text NOT NULL DEFAULT '',
		CreateAt bigint NOT NULL DEFAULT 0,
		UpdateAt bigint NOT NULL DEFAULT 0,
		DeleteAt bigint NOT NULL DEFAULT 0,
		SchemeManaged boolean NOT NULL DEFAULT false,
		BuiltIn boolean NOT NULL DEFAULT false,
		SchemeId varchar(26)
	)`)
	require.NoError(t, err, "create Roles stand-in")
}

// mustSeedSpaceSchemes seeds the three preset space schemes (contribute/comment/read-only), each
// with a generated user/admin/guest role carrying the canonical permission sets — the same shape
// the paired core branch's core seeding migration creates in production.
func mustSeedSpaceSchemes(t *testing.T, db *sql.DB) {
	t.Helper()
	presets := []struct {
		name  string
		user  []*mmmodel.Permission
		admin []*mmmodel.Permission
		guest []*mmmodel.Permission
	}{
		{mmmodel.SchemeNameSpaceContribute, mmmodel.SpaceDefaultContributePermissions, mmmodel.SpaceAdminRolePermissions, []*mmmodel.Permission{mmmodel.PermissionReadPage}},
		{mmmodel.SchemeNameSpaceComment, mmmodel.SpaceDefaultCommentPermissions, mmmodel.SpaceAdminRolePermissions, []*mmmodel.Permission{mmmodel.PermissionReadPage}},
		{mmmodel.SchemeNameSpaceReadOnly, mmmodel.SpaceDefaultReadOnlyPermissions, mmmodel.SpaceAdminRolePermissions, []*mmmodel.Permission{mmmodel.PermissionReadPage}},
	}
	now := mmmodel.GetMillis()
	for _, p := range presets {
		userRoleName := mustInsertStandInRole(t, db, "User Role for "+p.name, p.user, now)
		adminRoleName := mustInsertStandInRole(t, db, "Admin Role for "+p.name, p.admin, now)
		guestRoleName := mustInsertStandInRole(t, db, "Guest Role for "+p.name, p.guest, now)

		_, err := db.Exec(`INSERT INTO Schemes
			(Id, Name, Scope, DefaultChannelUserRole, DefaultChannelAdminRole, DefaultChannelGuestRole, CreateAt, UpdateAt)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
			mmmodel.NewId(), p.name, mmmodel.SchemeScopeChannel, userRoleName, adminRoleName, guestRoleName, now)
		require.NoError(t, err, "seed space scheme %q", p.name)
	}
}

// mustInsertStandInRole inserts one generated role row carrying permissions, returning its
// (randomly generated) name — mirroring core's scheme-generated role shape closely enough for the
// plugin store's Permissions-column parsing (space-joined string, strings.Fields on read).
func mustInsertStandInRole(t *testing.T, db *sql.DB, displayName string, permissions []*mmmodel.Permission, now int64) string {
	t.Helper()
	ids := make([]string, 0, len(permissions))
	for _, p := range permissions {
		ids = append(ids, p.Id)
	}
	roleName := mmmodel.NewId()
	_, err := db.Exec(`INSERT INTO Roles (Id, Name, DisplayName, Permissions, CreateAt, UpdateAt, SchemeManaged, BuiltIn)
		VALUES ($1, $2, $3, $4, $5, $5, true, false)`,
		mmmodel.NewId(), roleName, displayName, " "+strings.Join(ids, " "), now)
	require.NoError(t, err, "seed stand-in role %q", displayName)
	return roleName
}

// MustSeedChannelScheme seeds (or repoints) a Channels stand-in row so channelID resolves to the
// named seeded space scheme preset (see model.SpaceSchemeNames) — production sets this via the
// real Channels table when CreateSpace/SetSpaceDefaultCapabilities points a space's backing
// channel at a scheme; tests that mock the backing channel (CreateChannel via plugintest) need
// this seeded explicitly, since the mock never writes to a real Channels table.
func MustSeedChannelScheme(t *testing.T, db *sql.DB, channelID, schemeName string) {
	t.Helper()
	var schemeID string
	err := db.QueryRow(`SELECT Id FROM Schemes WHERE Name = $1`, schemeName).Scan(&schemeID)
	require.NoError(t, err, "look up seeded scheme %q", schemeName)
	_, err = db.Exec(`INSERT INTO Channels (Id, SchemeId) VALUES ($1, $2)
		ON CONFLICT (Id) DO UPDATE SET SchemeId = EXCLUDED.SchemeId`, channelID, schemeID)
	require.NoError(t, err, "seed Channels stand-in for %q", channelID)
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
