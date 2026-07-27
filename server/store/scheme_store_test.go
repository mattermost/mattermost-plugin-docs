// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// TestSpaceCustomScheme_RoundTrip verifies CreateSpaceCustomScheme -> GetSchemeRolesForChannel ->
// GetRolePermissionsByName: a custom scheme's generated user-role permissions are readable back
// exactly once a channel is repointed at it (mirroring SetSpaceDefaultCapabilities' repoint).
func TestSpaceCustomScheme_RoundTrip(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	userPerms := []string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionCommentPage.Id}
	adminPerms := []string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionAdminSpace.Id}
	guestPerms := []string{mmmodel.PermissionReadPage.Id}

	schemeID, err := s.CreateSpaceCustomScheme(userPerms, adminPerms, guestPerms)
	require.NoError(t, err)
	require.NotEmpty(t, schemeID)

	channelID := mmmodel.NewId()
	_, err = db.Exec(`INSERT INTO Channels (Id, SchemeId) VALUES ($1, $2)`, channelID, schemeID)
	require.NoError(t, err)

	roles, err := s.GetSchemeRolesForChannel(channelID)
	require.NoError(t, err)
	require.Equal(t, schemeID, roles.SchemeId)
	require.NotEmpty(t, roles.UserRoleName)
	require.NotEmpty(t, roles.AdminRoleName)
	require.NotEmpty(t, roles.GuestRoleName)

	userRolePerms, err := s.GetRolePermissionsByName(roles.UserRoleName)
	require.NoError(t, err)
	require.ElementsMatch(t, userPerms, userRolePerms)

	adminRolePerms, err := s.GetRolePermissionsByName(roles.AdminRoleName)
	require.NoError(t, err)
	require.ElementsMatch(t, adminPerms, adminRolePerms)

	guestRolePerms, err := s.GetRolePermissionsByName(roles.GuestRoleName)
	require.NoError(t, err)
	require.ElementsMatch(t, guestPerms, guestRolePerms)
}

// TestDeleteSpaceCustomSchemeIfUnreferenced covers the three outcomes: a no-op while a channel
// still references the scheme, an actual delete once unreferenced, and a rejection when the
// scheme id names one of the three seeded presets rather than a space-private custom scheme.
func TestDeleteSpaceCustomSchemeIfUnreferenced(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	t.Run("no-op while referenced", func(t *testing.T) {
		schemeID, err := s.CreateSpaceCustomScheme(
			[]string{mmmodel.PermissionReadPage.Id},
			[]string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionAdminSpace.Id},
			[]string{mmmodel.PermissionReadPage.Id},
		)
		require.NoError(t, err)

		channelID := mmmodel.NewId()
		_, err = db.Exec(`INSERT INTO Channels (Id, SchemeId) VALUES ($1, $2)`, channelID, schemeID)
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpaceCustomSchemeIfUnreferenced(schemeID, ""))

		// Still readable: the delete was a no-op.
		roles, err := s.GetSchemeRolesForChannel(channelID)
		require.NoError(t, err)
		require.Equal(t, schemeID, roles.SchemeId)
	})

	t.Run("deletes when the only reference is the excluded channel", func(t *testing.T) {
		schemeID, err := s.CreateSpaceCustomScheme(
			[]string{mmmodel.PermissionReadPage.Id},
			[]string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionAdminSpace.Id},
			[]string{mmmodel.PermissionReadPage.Id},
		)
		require.NoError(t, err)

		// Stands in for a channel abandoned by a failed space-creation step (see
		// DeleteSpaceCustomSchemeIfUnreferenced's excludeChannelID doc).
		channelID := mmmodel.NewId()
		_, err = db.Exec(`INSERT INTO Channels (Id, SchemeId) VALUES ($1, $2)`, channelID, schemeID)
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpaceCustomSchemeIfUnreferenced(schemeID, channelID))

		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM Schemes WHERE Id = $1`, schemeID).Scan(&count))
		require.Zero(t, count, "the abandoned backing channel must not count as a live reference")
	})

	t.Run("still a no-op when another channel references the scheme", func(t *testing.T) {
		schemeID, err := s.CreateSpaceCustomScheme(
			[]string{mmmodel.PermissionReadPage.Id},
			[]string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionAdminSpace.Id},
			[]string{mmmodel.PermissionReadPage.Id},
		)
		require.NoError(t, err)

		abandonedID, otherID := mmmodel.NewId(), mmmodel.NewId()
		_, err = db.Exec(`INSERT INTO Channels (Id, SchemeId) VALUES ($1, $2), ($3, $2)`, abandonedID, schemeID, otherID)
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpaceCustomSchemeIfUnreferenced(schemeID, abandonedID))

		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM Schemes WHERE Id = $1`, schemeID).Scan(&count))
		require.Equal(t, 1, count, "excluding one channel must not delete a scheme another channel still uses")
	})

	t.Run("deletes when unreferenced", func(t *testing.T) {
		schemeID, err := s.CreateSpaceCustomScheme(
			[]string{mmmodel.PermissionReadPage.Id},
			[]string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionAdminSpace.Id},
			[]string{mmmodel.PermissionReadPage.Id},
		)
		require.NoError(t, err)

		require.NoError(t, s.DeleteSpaceCustomSchemeIfUnreferenced(schemeID, ""))

		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM Schemes WHERE Id = $1`, schemeID).Scan(&count))
		require.Zero(t, count, "the scheme row must be gone once unreferenced")
	})

	t.Run("rejects a preset scheme id", func(t *testing.T) {
		var presetID string
		require.NoError(t, db.QueryRow(`SELECT Id FROM Schemes WHERE Name = $1`, mmmodel.SchemeNameSpaceContribute).Scan(&presetID))

		err := s.DeleteSpaceCustomSchemeIfUnreferenced(presetID, "")
		require.Error(t, err)
		require.True(t, store.IsErrInvalidInput(err))

		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM Schemes WHERE Id = $1`, presetID).Scan(&count))
		require.Equal(t, 1, count, "a preset scheme must never be deleted")
	})
}
