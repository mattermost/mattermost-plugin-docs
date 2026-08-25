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

// TestRolesForPermissions_AtomicRoleMapping pins each permission to the atomic role that carries it.
// The round-trip test above proves only that the permission/role maps agree with each other — the
// reverse map is derived from the forward one — so any permutation of the pairing still round-trips
// unchanged. Asserting the role name per permission is what catches a swap that would grant
// edit_page where create_page was requested. Both directions are pinned against the role constant
// rather than against each other.
func TestRolesForPermissions_AtomicRoleMapping(t *testing.T) {
	const schemeUserRole = "generated_scheme_user_role"

	atomicRoles := map[string]string{
		mmmodel.PermissionCreatePage.Id:    mmmodel.SpacePageCreatorRoleId,
		mmmodel.PermissionCommentPage.Id:   mmmodel.SpacePageCommenterRoleId,
		mmmodel.PermissionEditPage.Id:      mmmodel.SpacePageEditorRoleId,
		mmmodel.PermissionDeleteOwnPage.Id: mmmodel.SpacePageDeleterOwnRoleId,
		mmmodel.PermissionDeletePage.Id:    mmmodel.SpacePageDeleterRoleId,
	}

	for permission, roleName := range atomicRoles {
		t.Run(permission, func(t *testing.T) {
			explicitRoles, schemeAdmin := model.RolesForPermissions([]string{permission}, schemeUserRole)
			require.False(t, schemeAdmin, "%s must not imply admin", permission)
			require.Equal(t, []string{schemeUserRole, roleName}, strings.Fields(explicitRoles))

			granted := model.PermissionsFromMember(roleName, false, false, nil).Granted
			require.Equal(t, []string{permission}, granted, "%s must reverse-project to %s", roleName, permission)
		})
	}

	t.Run(mmmodel.PermissionAdminSpace.Id, func(t *testing.T) {
		// admin_space is carried by the SchemeAdmin flag rather than an atomic role, so it must emit
		// the base scheme-user token alone.
		explicitRoles, schemeAdmin := model.RolesForPermissions([]string{mmmodel.PermissionAdminSpace.Id}, schemeUserRole)
		require.True(t, schemeAdmin)
		require.Equal(t, []string{schemeUserRole}, strings.Fields(explicitRoles))
	})
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

// TestPresetPermissionContents pins the literal permission set each seeded preset grants.
// TestPresetRoundTrip above derives both lookup directions from the same preset table, so swapping
// two presets stays self-consistent and round-trips cleanly; only asserting the contents catches a
// mix-up that would seed a comment-only space with create/edit rights. read_page is the implicit
// baseline and must never appear in a wire-form preset.
func TestPresetPermissionContents(t *testing.T) {
	presets := map[string][]string{
		mmmodel.SchemeNameSpaceContribute: {
			mmmodel.PermissionCommentPage.Id,
			mmmodel.PermissionCreatePage.Id,
			mmmodel.PermissionEditPage.Id,
			mmmodel.PermissionDeleteOwnPage.Id,
		},
		mmmodel.SchemeNameSpaceComment:  {mmmodel.PermissionCommentPage.Id},
		mmmodel.SchemeNameSpaceReadOnly: {},
	}

	for name, want := range presets {
		t.Run(name, func(t *testing.T) {
			got, ok := model.DefaultPermissionsForSchemeName(name)
			require.True(t, ok)
			require.ElementsMatch(t, want, got)
			require.NotContains(t, got, mmmodel.PermissionReadPage.Id)
		})
	}
}

// TestAtomicRolesMatchCorePermissions checks the plugin's permission->role mapping against what
// core's roles actually grant. TestRolesForPermissions_AtomicRoleMapping above pins the mapping
// against role-name constants, so it catches the plugin mixing two roles up — but it reads only
// names, so it cannot see core changing which permission a role carries. That change would leave
// every name matching while a grant silently started conferring a different authority.
//
// Core's roles are the authority: each atomic role grants read_page (the baseline every space role
// carries) plus exactly the one permission it exists to confer. Reading them from
// MakeDefaultRoles keeps this honest — it is the same table core seeds from.
func TestAtomicRolesMatchCorePermissions(t *testing.T) {
	coreRoles := mmmodel.MakeDefaultRoles()

	for _, permission := range grantablePermissions {
		if permission == mmmodel.PermissionAdminSpace.Id {
			// admin_space is carried by the SchemeAdmin flag, not an atomic role.
			continue
		}
		t.Run(permission, func(t *testing.T) {
			explicitRoles, _ := model.RolesForPermissions([]string{permission}, "base_role")
			tokens := strings.Fields(explicitRoles)
			require.Len(t, tokens, 2, "expected the base role plus one atomic role, got %q", explicitRoles)
			roleName := tokens[1]

			coreRole, ok := coreRoles[roleName]
			require.True(t, ok, "%s maps to role %q, which core does not define", permission, roleName)

			granted := slices.DeleteFunc(slices.Clone(coreRole.Permissions), func(id string) bool {
				return id == mmmodel.PermissionReadPage.Id
			})
			require.Equal(t, []string{permission}, granted,
				"core's %q grants %v beyond read_page; the plugin maps %s to it, so the grant no longer confers what the caller asked for",
				roleName, granted, permission)
		})
	}
}

// TestAtomicRoleVocabularyCoversCore checks the plugin's atomic-role vocabulary against core's
// canonical list. TestAtomicRolesMatchCorePermissions above verifies the roles the plugin already
// knows about; this catches the other direction — core adding a sixth atomic role that the plugin
// never maps, so the permission it confers would be ungrantable through this API and a member
// carrying that role would reverse-project as holding nothing.
func TestAtomicRoleVocabularyCoversCore(t *testing.T) {
	mapped := make([]string, 0, len(mmmodel.SpaceCapabilityRoles))
	for _, permission := range grantablePermissions {
		if permission == mmmodel.PermissionAdminSpace.Id {
			continue
		}
		explicitRoles, _ := model.RolesForPermissions([]string{permission}, "base_role")
		mapped = append(mapped, strings.Fields(explicitRoles)[1])
	}

	require.ElementsMatch(t, mmmodel.SpaceCapabilityRoles, mapped,
		"the plugin's atomic-role vocabulary must match core's SpaceCapabilityRoles exactly")
}

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
		require.Equal(t, "model.space_permissions.read_page_not_grantable.app_error", aerr.Id)
	})

	t.Run("admin_space accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateGrantedPermissions([]string{mmmodel.PermissionAdminSpace.Id}))
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		aerr := model.ValidateGrantedPermissions([]string{"not_a_real_permission"})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_permissions.unknown_permission.app_error", aerr.Id)
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
		require.Equal(t, "model.space_permissions.read_page_not_grantable.app_error", aerr.Id)
	})

	t.Run("admin_space rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultPermissions([]string{mmmodel.PermissionAdminSpace.Id})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_permissions.admin_not_a_default.app_error", aerr.Id)
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultPermissions([]string{"not_a_real_permission"})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_permissions.unknown_permission.app_error", aerr.Id)
	})

	t.Run("every non-admin grantable permission accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateDefaultPermissions([]string{
			mmmodel.PermissionCreatePage.Id, mmmodel.PermissionCommentPage.Id, mmmodel.PermissionEditPage.Id,
			mmmodel.PermissionDeleteOwnPage.Id, mmmodel.PermissionDeletePage.Id,
		}))
	})
}
