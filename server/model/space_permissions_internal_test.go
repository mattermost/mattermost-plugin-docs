// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// TestPermissionAtomicRoleMatchesCoreCapabilityRoles pins the plugin's permission-to-role table to
// core's SpaceCapabilityRolePermissions in both directions: every role the plugin records for a
// permission is a core capability role granting read_page plus exactly that permission, and every
// core capability role is one the plugin can parse back into a permission.
func TestPermissionAtomicRoleMatchesCoreCapabilityRoles(t *testing.T) {
	for permission, roleName := range permissionAtomicRole {
		granted, ok := mmmodel.SpaceCapabilityRolePermissions[roleName]
		require.True(t, ok, "%s maps to %q, which core does not define as a capability role", permission, roleName)
		require.ElementsMatch(t, []string{mmmodel.PermissionReadPage.Id, permission}, mmmodel.PermissionIDs(granted),
			"core's %q must grant read_page plus %s and nothing else", roleName, permission)
	}
	for roleName := range mmmodel.SpaceCapabilityRolePermissions {
		_, ok := atomicRolePermission[roleName]
		require.True(t, ok, "core capability role %q has no permission in the plugin's table, so a member holding it would project as holding nothing", roleName)
	}
}
