// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"maps"
	"net/http"
	"slices"
	"strings"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// The space permission vocabulary is the core page-permission id strings themselves, so the API
// speaks the same tokens core enforces — no invented level names.

// grantableMemberPermissions is the wire vocabulary a caller may explicitly grant to a member:
// five independently grantable page capabilities plus the admin permission. Each page capability
// maps to a core role that also carries the read_page baseline.
var grantableMemberPermissions = map[string]bool{
	mmmodel.PermissionCreatePage.Id:    true,
	mmmodel.PermissionCommentPage.Id:   true,
	mmmodel.PermissionEditPage.Id:      true,
	mmmodel.PermissionDeleteOwnPage.Id: true,
	mmmodel.PermissionDeletePage.Id:    true,
	mmmodel.PermissionAdminSpace.Id:    true,
}

// isGrantableDefault reports whether p may appear in a space-default permission set: the member
// vocabulary less admin_space, which is member-grant-only and never a space default. Derived from
// the member vocabulary rather than spelled out a second time, so a permission added there cannot
// be silently absent here.
func isGrantableDefault(p string) bool {
	return p != mmmodel.PermissionAdminSpace.Id && grantableMemberPermissions[p]
}

// permissionAtomicRole maps each non-admin grantable permission to the core capability (atomic)
// role recorded in ExplicitRoles: a core role carrying exactly one page permission, as opposed to
// the scheme's generated user/admin roles, which bundle a space's whole default or admin set.
var permissionAtomicRole = map[string]string{
	mmmodel.PermissionCreatePage.Id:    mmmodel.SpacePageCreatorRoleId,
	mmmodel.PermissionCommentPage.Id:   mmmodel.SpacePageCommenterRoleId,
	mmmodel.PermissionEditPage.Id:      mmmodel.SpacePageEditorRoleId,
	mmmodel.PermissionDeleteOwnPage.Id: mmmodel.SpacePageDeleterOwnRoleId,
	mmmodel.PermissionDeletePage.Id:    mmmodel.SpacePageDeleterRoleId,
}

// atomicRolePermission is the reverse of permissionAtomicRole, used to parse a stored
// ExplicitRoles string back into the granted permission set. Derived from permissionAtomicRole so
// the two cannot drift.
var atomicRolePermission = func() map[string]string {
	m := make(map[string]string, len(permissionAtomicRole))
	for permission, roleName := range permissionAtomicRole {
		m[roleName] = permission
	}
	return m
}()

// stripReadPage returns permissions' wire id strings with read_page removed, so a canonical core
// permission set can be single-sourced into the read_page-free wire vocabulary without drift.
func stripReadPage(permissions []*mmmodel.Permission) []string {
	return slices.DeleteFunc(mmmodel.PermissionIDs(permissions), func(id string) bool {
		return id == mmmodel.PermissionReadPage.Id
	})
}

// spaceAdminEffectivePermissions is the full permission set a SchemeAdmin member effectively
// holds, single-sourced from core's canonical admin permission slice (SpaceAdminRolePermissions,
// which already includes read_page). Stored already normalized, like presetPermissionSets below,
// so the accessor copies without re-deriving the canonical form on every call.
var spaceAdminEffectivePermissions = mmmodel.NormalizePermissions(mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions))

// presetPermissionSets are the three seeded default-permission presets in wire form (read_page-
// free — the baseline is implicit and never listed), single-sourced from core's canonical
// permission slices. Stored already normalized so the lookups below compare and copy without
// re-deriving the canonical form on every call.
var presetPermissionSets = map[string][]string{
	mmmodel.SchemeNameSpaceContribute: mmmodel.NormalizePermissions(stripReadPage(mmmodel.SpaceDefaultContributePermissions)),
	mmmodel.SchemeNameSpaceComment:    mmmodel.NormalizePermissions(stripReadPage(mmmodel.SpaceDefaultCommentPermissions)),
	mmmodel.SchemeNameSpaceReadOnly:   mmmodel.NormalizePermissions(stripReadPage(mmmodel.SpaceDefaultReadOnlyPermissions)),
}

// validatePermissions validates permissions against allowed, rejecting read_page as the
// non-grantable baseline, plus admin_space when rejectAdmin is set. An unknown token is rejected.
// Dedup-tolerant. where attributes the rejection to the calling validator.
func validatePermissions(where string, permissions []string, allowed map[string]bool, rejectAdmin bool) *mmmodel.AppError {
	for _, p := range permissions {
		if p == mmmodel.PermissionReadPage.Id {
			return mmmodel.NewAppError(where, "model.space_permissions.read_page_not_grantable.app_error", nil, "", http.StatusBadRequest)
		}
		if rejectAdmin && p == mmmodel.PermissionAdminSpace.Id {
			return mmmodel.NewAppError(where, "model.space_permissions.admin_not_a_default.app_error", nil, "", http.StatusBadRequest)
		}
		if !allowed[p] {
			return mmmodel.NewAppError(where, "model.space_permissions.unknown_permission.app_error", map[string]any{"Permission": p}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// ValidateGrantedPermissions validates a per-member granted-permission request: each token must
// be one of the grantable member permissions.
func ValidateGrantedPermissions(permissions []string) *mmmodel.AppError {
	return validatePermissions("ValidateGrantedPermissions", permissions, grantableMemberPermissions, false)
}

// ValidateDefaultPermissions validates a space-default permission set: same rule as
// ValidateGrantedPermissions, plus admin_space is also rejected — a space default is never
// admin-granting. rejectAdmin does that rejection, and does it with its own message, so the
// allowlist passed here is still the member vocabulary.
func ValidateDefaultPermissions(permissions []string) *mmmodel.AppError {
	return validatePermissions("ValidateDefaultPermissions", permissions, grantableMemberPermissions, true)
}

// RolesForPermissions maps the requested non-admin permissions to their capability role names for
// ExplicitRoles, and reports whether admin_space was requested (schemeAdmin). The base
// schemeUserRole token is always emitted first — core rejects a member update that leaves the base
// scheme role unset. Pure: schemeUserRole (the scheme's generated user-role name) comes from the
// caller, resolved via a store lookup elsewhere.
func RolesForPermissions(permissions []string, schemeUserRole string) (explicitRoles string, schemeAdmin bool) {
	roles := []string{schemeUserRole}
	for _, p := range mmmodel.NormalizePermissions(permissions) {
		if p == mmmodel.PermissionAdminSpace.Id {
			schemeAdmin = true
			continue
		}
		if roleName, ok := permissionAtomicRole[p]; ok {
			roles = append(roles, roleName)
		}
	}
	return strings.Join(roles, " "), schemeAdmin
}

// MemberPermissions is the reverse projection of a member's stored role state onto the permission
// vocabulary: Effective is the member's effective permission set, Granted is the per-member
// granted set beyond the space default.
type MemberPermissions struct {
	Effective []string
	Granted   []string
	IsAdmin   bool
	IsGuest   bool
}

// PermissionsFromMember reverse-projects a member's raw role state onto the permission vocabulary.
// explicitRoles is the raw space-delimited ChannelMember.ExplicitRoles string; any token that is
// not a recognized capability role is ignored (harmless if the base scheme token is passed too).
// defaultPermissions is the space's default permission set (wire form, read_page-free).
//
// A SchemeGuest member's effective set is read_page alone — neither the space default nor its own
// granted set. A guest resolves through the read-only DefaultChannelGuestRole rather than the
// scheme's user-role default, and the write gate holds a guest to read_page even when a grant made
// before a demotion is still recorded in ExplicitRoles. Granted still reports what is recorded, so
// the two fields together show a stale grant rather than hiding it.
//
// A SchemeAdmin member's effective set additionally includes the full canonical admin
// permission set, since SchemeAdmin resolves through DefaultChannelAdminRole regardless of what is
// (or isn't) recorded in ExplicitRoles/the space default. Pure: the model never touches the store.
func PermissionsFromMember(explicitRoles string, schemeAdmin, schemeGuest bool, defaultPermissions []string) MemberPermissions {
	granted := make(map[string]bool)
	for token := range strings.FieldsSeq(explicitRoles) {
		if p, ok := atomicRolePermission[token]; ok {
			granted[p] = true
		}
	}
	if schemeAdmin {
		granted[mmmodel.PermissionAdminSpace.Id] = true
	}
	grantedList := mmmodel.NormalizePermissions(slices.Collect(maps.Keys(granted)))

	effective := map[string]bool{mmmodel.PermissionReadPage.Id: true}
	if !schemeGuest {
		if schemeAdmin {
			for _, p := range spaceAdminEffectivePermissions {
				effective[p] = true
			}
		}
		for _, p := range defaultPermissions {
			effective[p] = true
		}
		for _, p := range grantedList {
			effective[p] = true
		}
	}

	return MemberPermissions{
		Effective: mmmodel.NormalizePermissions(slices.Collect(maps.Keys(effective))),
		Granted:   grantedList,
		IsAdmin:   schemeAdmin,
		IsGuest:   schemeGuest,
	}
}

// PermissionsFromChannelMember is PermissionsFromMember for a backing-channel membership row.
// The roster and SpaceWithAccess member branch both go through here so a ChannelMember is
// unpacked in one place.
func PermissionsFromChannelMember(cm *mmmodel.ChannelMember, defaultPermissions []string) MemberPermissions {
	return PermissionsFromMember(cm.ExplicitRoles, cm.SchemeAdmin, cm.SchemeGuest, defaultPermissions)
}

// AdminEffectivePermissions returns the full permission set a SchemeAdmin effectively holds, wire
// form, non-nil.
func AdminEffectivePermissions() []string {
	// Cloned, not returned directly: the set is package state a caller must not be able to mutate
	// through the returned slice.
	return slices.Clone(spaceAdminEffectivePermissions)
}

// DefaultPermissionsFrom projects a backing-channel scheme's stored user-role permission set
// (raw core permission ids) onto the wire default-permission vocabulary: read_page (the implicit
// baseline) and any non-default permission are stripped; only the grantable default
// permission tokens remain.
func DefaultPermissionsFrom(permissions []string) []string {
	out := make([]string, 0, len(permissions))
	for _, p := range permissions {
		if isGrantableDefault(p) {
			out = append(out, p)
		}
	}
	return mmmodel.NormalizePermissions(out)
}

// DefaultPermissionsForSchemeName returns the wire-form default permission set for one of the
// three seeded preset scheme names, or false if name is not a preset.
func DefaultPermissionsForSchemeName(name string) ([]string, bool) {
	permissions, ok := presetPermissionSets[name]
	if !ok {
		return nil, false
	}
	// Cloned, not returned directly: the presets are package state a caller must not be able to
	// mutate through the returned slice.
	return slices.Clone(permissions), true
}

// SchemeNameForDefaultPermissions returns the seeded preset scheme name matching permissions, or false
// if permissions does not match any preset. Recognition is set equality — order-insensitive, deduplicated
// — never a raw array comparison.
func SchemeNameForDefaultPermissions(permissions []string) (string, bool) {
	normalized := mmmodel.NormalizePermissions(permissions)
	for name, preset := range presetPermissionSets {
		if slices.Equal(preset, normalized) {
			return name, true
		}
	}
	return "", false
}
