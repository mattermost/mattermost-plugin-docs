// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// webappTierScheme maps each tier the webapp names to the seeded preset scheme whose default
// permission set it must equal.
var webappTierScheme = map[string]string{
	"view":    mmmodel.SchemeNameSpaceReadOnly,
	"comment": mmmodel.SchemeNameSpaceComment,
	"edit":    mmmodel.SchemeNameSpaceContribute,
}

// readWebappSource returns the contents of a webapp source file, addressed from this package.
func readWebappSource(t *testing.T, relative string) string {
	t.Helper()
	path := filepath.Join("..", "..", "webapp", "src", relative)
	contents, err := os.ReadFile(path)
	require.NoError(t, err, "cannot read %s", path)
	return string(contents)
}

// parseWebappPermissionIDs maps each name in the webapp's Permissions object to its wire id, so a
// tier's entries can be resolved to the tokens the server speaks.
func parseWebappPermissionIDs(t *testing.T) map[string]string {
	t.Helper()
	source := readWebappSource(t, filepath.Join("types", "permissions.ts"))
	body := regexp.MustCompile(`(?s)export const Permissions = \{(.*?)\n\} as const;`).FindStringSubmatch(source)
	require.Len(t, body, 2, "cannot locate the Permissions object in types/permissions.ts")

	ids := map[string]string{}
	for _, entry := range regexp.MustCompile(`(\w+): '([a-z_]+)',`).FindAllStringSubmatch(body[1], -1) {
		ids[entry[1]] = entry[2]
	}
	require.NotEmpty(t, ids, "parsed no permissions out of types/permissions.ts")
	return ids
}

// parseWebappTierPermissions maps each tier in the webapp's TIER_PERMISSIONS object to the wire
// ids it lists.
func parseWebappTierPermissions(t *testing.T) map[string][]string {
	t.Helper()
	source := readWebappSource(t, filepath.Join("utils", "space_permission_sets.ts"))
	body := regexp.MustCompile(`(?s)export const TIER_PERMISSIONS: Record<PermissionTier, readonly Permission\[\]> = \{(.*?)\n\};`).FindStringSubmatch(source)
	require.Len(t, body, 2, "cannot locate the TIER_PERMISSIONS object in utils/space_permission_sets.ts")

	permissionIDs := parseWebappPermissionIDs(t)
	tiers := map[string][]string{}
	for _, entry := range regexp.MustCompile(`(?m)^\s+(\w+): \[(.*)\],$`).FindAllStringSubmatch(body[1], -1) {
		permissions := []string{}
		for _, ref := range regexp.MustCompile(`Permissions\.(\w+)`).FindAllStringSubmatch(entry[2], -1) {
			id, ok := permissionIDs[ref[1]]
			require.True(t, ok, "TIER_PERMISSIONS references Permissions.%s, which types/permissions.ts does not define", ref[1])
			permissions = append(permissions, id)
		}
		tiers[entry[1]] = permissions
	}
	require.NotEmpty(t, tiers, "parsed no tiers out of utils/space_permission_sets.ts")
	return tiers
}

// TestWebappTierPermissionsMatchPresets is the compile-time link the webapp cannot have: its
// TIER_PERMISSIONS table restates the three seeded presets, which are single-sourced from core on
// this side, and nothing else would notice a core preset changing underneath it. Failing here means
// webapp/src/utils/space_permission_sets.ts must be updated to match the preset the tier names —
// the presets are canonical, not the copy.
func TestWebappTierPermissionsMatchPresets(t *testing.T) {
	tiers := parseWebappTierPermissions(t)

	require.Len(t, tiers, len(webappTierScheme), "the webapp names tiers %v; the presets are %v", tiers, webappTierScheme)
	for tier, schemeName := range webappTierScheme {
		listed, ok := tiers[tier]
		require.True(t, ok, "the webapp no longer defines the %q tier", tier)

		preset, ok := model.DefaultPermissionsForSchemeName(schemeName)
		require.True(t, ok, "%s is not a seeded preset scheme name", schemeName)

		require.Equal(t, preset, mmmodel.NormalizePermissions(listed),
			"TIER_PERMISSIONS.%s must equal the %s preset's default set", tier, schemeName)
	}
}

// parseWebappPermissionList maps one `export const NAME: readonly Permission[] = [...]` array in
// types/permissions.ts to the wire ids it lists. A list built by spreading another (
// MEMBER_PERMISSION_ORDER spreads DEFAULT_PERMISSION_ORDER) is resolved against that list, so the
// ids compared here are the ones the webapp actually renders.
func parseWebappPermissionList(t *testing.T, name string) []string {
	t.Helper()
	source := readWebappSource(t, filepath.Join("types", "permissions.ts"))
	body := regexp.MustCompile(`(?s)export const ` + name + `: readonly Permission\[\] = \[(.*?)\n\];`).FindStringSubmatch(source)
	require.Len(t, body, 2, "cannot locate %s in types/permissions.ts", name)

	permissionIDs := parseWebappPermissionIDs(t)
	ids := []string{}
	for _, entry := range regexp.MustCompile(`\.\.\.(\w+)|Permissions\.(\w+)`).FindAllStringSubmatch(body[1], -1) {
		if entry[1] != "" {
			ids = append(ids, parseWebappPermissionList(t, entry[1])...)
			continue
		}
		id, ok := permissionIDs[entry[2]]
		require.True(t, ok, "%s references Permissions.%s, which types/permissions.ts does not define", name, entry[2])
		ids = append(ids, id)
	}
	require.NotEmpty(t, ids, "parsed no permissions out of %s", name)
	return ids
}

// spaceWireVocabulary is every permission id the plugin can put on the wire: the channel-scoped
// space permissions a member holds or is granted, plus the two team-scoped tiers, which reach a
// caller in an effective set. The webapp names each of them, so this is what its Permissions
// object must cover.
func spaceWireVocabulary() []string {
	ids := mmmodel.PermissionIDs(mmmodel.SpaceChannelScopedPermissions)
	return append(ids, mmmodel.PermissionManageSpace.Id, mmmodel.PermissionDeleteSpace.Id)
}

// TestWebappPermissionIDsMatchCore pins the id strings themselves. types/permissions.ts calls
// itself byte-for-byte aligned with the server, and until now only the ids a tier happened to
// reference were resolved at all — a wrong token on any other permission reached the client as a
// permission the server has never heard of, and was simply never granted.
func TestWebappPermissionIDsMatchCore(t *testing.T) {
	ids := parseWebappPermissionIDs(t)

	known := map[string]bool{}
	for _, permission := range mmmodel.AllPermissions {
		known[permission.Id] = true
	}
	listed := map[string]bool{}
	for name, id := range ids {
		require.True(t, known[id], "Permissions.%s is %q, which core defines no permission for", name, id)
		listed[id] = true
	}

	for _, id := range spaceWireVocabulary() {
		require.True(t, listed[id], "core speaks %q on the space wire; types/permissions.ts does not name it", id)
	}
}

// TestWebappDefaultPermissionOrderMatchesGrantable pins the space-default vocabulary the two
// permission surfaces offer — the Share menu's checkboxes and the settings tab's toggles both
// render DEFAULT_PERMISSION_ORDER — against the set ValidateDefaultPermissions accepts. A
// permission made grantable server-side and not added here is offered by neither surface.
func TestWebappDefaultPermissionOrderMatchesGrantable(t *testing.T) {
	grantable := []string{}
	for _, id := range mmmodel.PermissionIDs(mmmodel.SpaceChannelScopedPermissions) {
		if model.ValidateDefaultPermissions([]string{id}) == nil {
			grantable = append(grantable, id)
		}
	}

	require.ElementsMatch(t, grantable, parseWebappPermissionList(t, "DEFAULT_PERMISSION_ORDER"),
		"DEFAULT_PERMISSION_ORDER must list exactly the permissions a space default may carry")
}

// TestWebappMemberPermissionOrderMatchesGrantable is the same pin on the per-member grant
// vocabulary, which differs from the space default by admin_space alone.
func TestWebappMemberPermissionOrderMatchesGrantable(t *testing.T) {
	grantable := []string{}
	for _, id := range mmmodel.PermissionIDs(mmmodel.SpaceChannelScopedPermissions) {
		if model.ValidateGrantedPermissions([]string{id}) == nil {
			grantable = append(grantable, id)
		}
	}

	require.ElementsMatch(t, grantable, parseWebappPermissionList(t, "MEMBER_PERMISSION_ORDER"),
		"MEMBER_PERMISSION_ORDER must list exactly the permissions a member may be granted")
}
