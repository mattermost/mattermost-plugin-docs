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
