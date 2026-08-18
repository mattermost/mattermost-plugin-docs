// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model_test

import (
	"slices"
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// grantablePermissions is the full grant vocabulary (the five atomic per-page permissions plus
// admin_space), used to enumerate every subset for the round-trip test below.
var grantablePermissions = []string{
	mmmodel.PermissionCreatePage.Id,
	mmmodel.PermissionCommentPage.Id,
	mmmodel.PermissionEditPage.Id,
	mmmodel.PermissionDeleteOwnPage.Id,
	mmmodel.PermissionDeletePage.Id,
	mmmodel.PermissionAdminSpace.Id,
}

// TestRolesForPermissions_PermissionsFromMember_RoundTrip verifies that every subset of the
// grantable permissions is unchanged after RolesForPermissions -> (core persistence, which
// drops the base scheme-user token and keeps only the atomic role names) -> PermissionsFromMember.
func TestRolesForPermissions_PermissionsFromMember_RoundTrip(t *testing.T) {
	const schemeUserRole = "generated_scheme_user_role"

	for mask := range 1 << len(grantablePermissions) {
		var permissions []string
		for i, p := range grantablePermissions {
			if mask&(1<<i) != 0 {
				permissions = append(permissions, p)
			}
		}
		want := model.NormalizePermissions(permissions)

		explicitRoles, schemeAdmin := model.RolesForPermissions(permissions, schemeUserRole)

		// Simulate core's own persistence: the base scheme-user token is consumed into the
		// SchemeUser flag and never stored; only the atomic role names survive in ExplicitRoles.
		tokens := strings.Fields(explicitRoles)
		require.Equal(t, schemeUserRole, tokens[0], "the base scheme-user token must always be emitted first")
		stored := strings.Join(tokens[1:], " ")

		mc := model.PermissionsFromMember(stored, schemeAdmin, false, nil)
		require.Equal(t, want, mc.Granted, "permissions=%v", permissions)
		require.Equal(t, len(permissions) > 0 && slices.Contains(permissions, mmmodel.PermissionAdminSpace.Id), mc.IsAdmin)
	}
}

// TestPermissionsFromMember_Guest verifies that a SchemeGuest member's effective permissions are
// read_page alone: neither the space default nor a grant recorded before a demotion contributes,
// since a guest resolves through the read-only guest role and the write gate holds them to reads.
func TestPermissionsFromMember_Guest(t *testing.T) {
	t.Run("grant-free guest is read-only", func(t *testing.T) {
		mc := model.PermissionsFromMember("", false, true, []string{mmmodel.PermissionCreatePage.Id, mmmodel.PermissionCommentPage.Id, mmmodel.PermissionEditPage.Id, mmmodel.PermissionDeleteOwnPage.Id})
		require.Equal(t, []string{mmmodel.PermissionReadPage.Id}, mc.Effective)
		require.Empty(t, mc.Granted)
		require.True(t, mc.IsGuest)
		require.False(t, mc.IsAdmin)
	})

	t.Run("grants surviving a demotion are reported but not effective", func(t *testing.T) {
		// The roles a demotion leaves behind: core clears SchemeUser/SchemeAdmin but not the atomic
		// roles in ExplicitRoles.
		explicitRoles := mmmodel.SpacePageCreatorRoleId + " " + mmmodel.SpacePageEditorRoleId
		// A contribute default that must NOT leak into a guest's effective set either.
		defaultPermissions := []string{mmmodel.PermissionCommentPage.Id, mmmodel.PermissionCreatePage.Id, mmmodel.PermissionEditPage.Id, mmmodel.PermissionDeleteOwnPage.Id}

		mc := model.PermissionsFromMember(explicitRoles, false, true, defaultPermissions)

		// Granted still reports the stale grant, so it is visible rather than hidden.
		require.Equal(t, model.NormalizePermissions([]string{mmmodel.PermissionCreatePage.Id, mmmodel.PermissionEditPage.Id}), mc.Granted)
		require.Equal(t, []string{mmmodel.PermissionReadPage.Id}, mc.Effective)
		require.True(t, mc.IsGuest)
	})
}

// TestPresetRoundTrip verifies the default-preset <-> scheme-name maps invert each other for the
// three seeded presets, and that preset recognition is order-insensitive and dedup-tolerant.
func TestPresetRoundTrip(t *testing.T) {
	presetNames := []string{
		mmmodel.SchemeNameSpaceContribute,
		mmmodel.SchemeNameSpaceComment,
		mmmodel.SchemeNameSpaceReadOnly,
	}

	for _, name := range presetNames {
		t.Run(name, func(t *testing.T) {
			permissions, ok := model.DefaultPermissionsForSchemeName(name)
			require.True(t, ok)

			recognizedName, ok := model.SchemeNameForDefaultPermissions(permissions)
			require.True(t, ok)
			require.Equal(t, name, recognizedName)

			roundTripped, ok := model.DefaultPermissionsForSchemeName(recognizedName)
			require.True(t, ok)
			require.Equal(t, permissions, roundTripped)
		})
	}

	t.Run("recognition is order-insensitive and dedup-tolerant", func(t *testing.T) {
		contribute, ok := model.DefaultPermissionsForSchemeName(mmmodel.SchemeNameSpaceContribute)
		require.True(t, ok)
		require.NotEmpty(t, contribute)

		// Reverse the set and duplicate every entry: still recognized as contribute.
		shuffled := make([]string, 0, len(contribute)*2)
		for i := len(contribute) - 1; i >= 0; i-- {
			shuffled = append(shuffled, contribute[i], contribute[i])
		}

		name, ok := model.SchemeNameForDefaultPermissions(shuffled)
		require.True(t, ok)
		require.Equal(t, mmmodel.SchemeNameSpaceContribute, name)
	})

	t.Run("a non-preset set is not recognized", func(t *testing.T) {
		_, ok := model.SchemeNameForDefaultPermissions([]string{mmmodel.PermissionCreatePage.Id})
		require.False(t, ok)
	})
}

// TestGrantablePermissionSets pins the exact vocabulary ValidateGrantedPermissions and
// ValidateDefaultPermissions each accept — the full known permission set, checked one token at a
// time — so an unintended token added to (or a valid one dropped from) grantableMemberPermissions
// or grantableDefaultPermissions fails here, rather than surfacing later as an unnoticed grant or
// space default this suite's other tests, which only ever pass the tokens already expected, would
// never exercise.
func TestGrantablePermissionSets(t *testing.T) {
	wantMember := map[string]bool{
		mmmodel.PermissionReadPage.Id:      false,
		mmmodel.PermissionCreatePage.Id:    true,
		mmmodel.PermissionCommentPage.Id:   true,
		mmmodel.PermissionEditPage.Id:      true,
		mmmodel.PermissionDeleteOwnPage.Id: true,
		mmmodel.PermissionDeletePage.Id:    true,
		mmmodel.PermissionAdminSpace.Id:    true,
	}
	wantDefault := map[string]bool{
		mmmodel.PermissionReadPage.Id:      false,
		mmmodel.PermissionCreatePage.Id:    true,
		mmmodel.PermissionCommentPage.Id:   true,
		mmmodel.PermissionEditPage.Id:      true,
		mmmodel.PermissionDeleteOwnPage.Id: true,
		mmmodel.PermissionDeletePage.Id:    true,
		mmmodel.PermissionAdminSpace.Id:    false,
	}
	for permission, grantable := range wantMember {
		t.Run("member/"+permission, func(t *testing.T) {
			aerr := model.ValidateGrantedPermissions([]string{permission})
			if grantable {
				require.Nil(t, aerr, "%s must be grantable to a member", permission)
			} else {
				require.NotNil(t, aerr, "%s must not be grantable to a member", permission)
			}
		})
	}
	for permission, isDefault := range wantDefault {
		t.Run("default/"+permission, func(t *testing.T) {
			aerr := model.ValidateDefaultPermissions([]string{permission})
			if isDefault {
				require.Nil(t, aerr, "%s must be a valid space default", permission)
			} else {
				require.NotNil(t, aerr, "%s must not be a valid space default", permission)
			}
		})
	}
}

// TestValidateGrantedPermissions verifies the per-member grant validator: read_page is rejected
// as the non-grantable baseline, admin_space is accepted, and an unknown token is rejected.
func TestValidateGrantedPermissions(t *testing.T) {
	t.Run("read_page rejected", func(t *testing.T) {
		aerr := model.ValidateGrantedPermissions([]string{mmmodel.PermissionReadPage.Id})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.read_page_not_grantable.app_error", aerr.Id)
	})

	t.Run("admin_space accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateGrantedPermissions([]string{mmmodel.PermissionAdminSpace.Id}))
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		aerr := model.ValidateGrantedPermissions([]string{"not_a_real_permission"})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.unknown_capability.app_error", aerr.Id)
	})

	t.Run("every non-admin grantable permission accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateGrantedPermissions([]string{
			mmmodel.PermissionCreatePage.Id, mmmodel.PermissionCommentPage.Id, mmmodel.PermissionEditPage.Id,
			mmmodel.PermissionDeleteOwnPage.Id, mmmodel.PermissionDeletePage.Id,
		}))
	})
}

// TestValidateDefaultPermissions verifies the space-default validator: read_page and admin_space
// are both rejected (the baseline is implicit; admin is never a default), and an unknown token is
// rejected.
func TestValidateDefaultPermissions(t *testing.T) {
	t.Run("read_page rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultPermissions([]string{mmmodel.PermissionReadPage.Id})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.read_page_not_grantable.app_error", aerr.Id)
	})

	t.Run("admin_space rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultPermissions([]string{mmmodel.PermissionAdminSpace.Id})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.admin_not_a_default.app_error", aerr.Id)
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultPermissions([]string{"not_a_real_permission"})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.unknown_capability.app_error", aerr.Id)
	})

	t.Run("every non-admin grantable permission accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateDefaultPermissions([]string{
			mmmodel.PermissionCreatePage.Id, mmmodel.PermissionCommentPage.Id, mmmodel.PermissionEditPage.Id,
			mmmodel.PermissionDeleteOwnPage.Id, mmmodel.PermissionDeletePage.Id,
		}))
	})
}
