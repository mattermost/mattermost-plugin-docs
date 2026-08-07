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

// grantableCapabilities is the full grant vocabulary (the five atomic per-page capabilities plus
// admin_space), used to enumerate every subset for the round-trip test below.
var grantableCapabilities = []string{
	model.CapabilityCreatePage,
	model.CapabilityCommentPage,
	model.CapabilityEditPage,
	model.CapabilityDeleteOwnPage,
	model.CapabilityDeletePage,
	model.CapabilityAdminSpace,
}

// TestRolesForCapabilities_CapabilitiesFromMember_RoundTrip verifies that every subset of the
// grantable capabilities is unchanged after RolesForCapabilities -> (core persistence, which
// drops the base scheme-user token and keeps only the atomic role names) -> CapabilitiesFromMember.
func TestRolesForCapabilities_CapabilitiesFromMember_RoundTrip(t *testing.T) {
	const schemeUserRole = "generated_scheme_user_role"

	for mask := range 1 << len(grantableCapabilities) {
		var capabilities []string
		for i, c := range grantableCapabilities {
			if mask&(1<<i) != 0 {
				capabilities = append(capabilities, c)
			}
		}
		want := model.NormalizeCapabilitySet(capabilities)

		explicitRoles, schemeAdmin := model.RolesForCapabilities(capabilities, schemeUserRole)

		// Simulate core's own persistence: the base scheme-user token is consumed into the
		// SchemeUser flag and never stored; only the atomic role names survive in ExplicitRoles.
		tokens := strings.Fields(explicitRoles)
		require.Equal(t, schemeUserRole, tokens[0], "the base scheme-user token must always be emitted first")
		stored := strings.Join(tokens[1:], " ")

		mc := model.CapabilitiesFromMember(stored, schemeAdmin, false, nil)
		require.Equal(t, want, mc.Granted, "capabilities=%v", capabilities)
		require.Equal(t, len(capabilities) > 0 && slices.Contains(capabilities, model.CapabilityAdminSpace), mc.IsAdmin)
	}
}

// TestCapabilitiesFromMember_Guest verifies that a SchemeGuest member's effective capabilities are
// read_page alone: neither the space default nor a grant recorded before a demotion contributes,
// since a guest resolves through the read-only guest role and the write gate holds them to reads.
func TestCapabilitiesFromMember_Guest(t *testing.T) {
	t.Run("grant-free guest is read-only", func(t *testing.T) {
		mc := model.CapabilitiesFromMember("", false, true, []string{model.CapabilityCreatePage, model.CapabilityCommentPage, model.CapabilityEditPage, model.CapabilityDeleteOwnPage})
		require.Equal(t, []string{model.CapabilityReadPage}, mc.Effective)
		require.Empty(t, mc.Granted)
		require.True(t, mc.IsGuest)
		require.False(t, mc.IsAdmin)
	})

	t.Run("grants surviving a demotion are reported but not effective", func(t *testing.T) {
		// The roles a demotion leaves behind: core clears SchemeUser/SchemeAdmin but not the atomic
		// capability roles in ExplicitRoles.
		explicitRoles := mmmodel.SpacePageCreatorRoleId + " " + mmmodel.SpacePageEditorRoleId
		// A contribute default that must NOT leak into a guest's effective set either.
		defaultCapabilities := []string{model.CapabilityCommentPage, model.CapabilityCreatePage, model.CapabilityEditPage, model.CapabilityDeleteOwnPage}

		mc := model.CapabilitiesFromMember(explicitRoles, false, true, defaultCapabilities)

		// Granted still reports the stale grant, so it is visible rather than hidden.
		require.Equal(t, model.NormalizeCapabilitySet([]string{model.CapabilityCreatePage, model.CapabilityEditPage}), mc.Granted)
		require.Equal(t, []string{model.CapabilityReadPage}, mc.Effective)
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
			capabilities, ok := model.DefaultCapabilitiesForSchemeName(name)
			require.True(t, ok)

			recognizedName, ok := model.SchemeNameForDefaultCapabilities(capabilities)
			require.True(t, ok)
			require.Equal(t, name, recognizedName)

			roundTripped, ok := model.DefaultCapabilitiesForSchemeName(recognizedName)
			require.True(t, ok)
			require.Equal(t, capabilities, roundTripped)
		})
	}

	t.Run("recognition is order-insensitive and dedup-tolerant", func(t *testing.T) {
		contribute, ok := model.DefaultCapabilitiesForSchemeName(mmmodel.SchemeNameSpaceContribute)
		require.True(t, ok)
		require.NotEmpty(t, contribute)

		// Reverse the set and duplicate every entry: still recognized as contribute.
		shuffled := make([]string, 0, len(contribute)*2)
		for i := len(contribute) - 1; i >= 0; i-- {
			shuffled = append(shuffled, contribute[i], contribute[i])
		}

		name, ok := model.SchemeNameForDefaultCapabilities(shuffled)
		require.True(t, ok)
		require.Equal(t, mmmodel.SchemeNameSpaceContribute, name)
	})

	t.Run("a non-preset set is not recognized", func(t *testing.T) {
		_, ok := model.SchemeNameForDefaultCapabilities([]string{model.CapabilityCreatePage})
		require.False(t, ok)
	})
}

// TestGrantableCapabilitySets pins the exact vocabulary ValidateGrantedCapabilities and
// ValidateDefaultCapabilities each accept — the full known capability set, checked one token at a
// time — so an unintended token added to (or a valid one dropped from) grantableMemberCapabilities
// or grantableDefaultCapabilities fails here, rather than surfacing later as an unnoticed grant or
// space default this suite's other tests, which only ever pass the tokens already expected, would
// never exercise.
func TestGrantableCapabilitySets(t *testing.T) {
	wantMember := map[string]bool{
		model.CapabilityReadPage:      false,
		model.CapabilityCreatePage:    true,
		model.CapabilityCommentPage:   true,
		model.CapabilityEditPage:      true,
		model.CapabilityDeleteOwnPage: true,
		model.CapabilityDeletePage:    true,
		model.CapabilityAdminSpace:    true,
	}
	wantDefault := map[string]bool{
		model.CapabilityReadPage:      false,
		model.CapabilityCreatePage:    true,
		model.CapabilityCommentPage:   true,
		model.CapabilityEditPage:      true,
		model.CapabilityDeleteOwnPage: true,
		model.CapabilityDeletePage:    true,
		model.CapabilityAdminSpace:    false,
	}
	for capability, grantable := range wantMember {
		t.Run("member/"+capability, func(t *testing.T) {
			aerr := model.ValidateGrantedCapabilities([]string{capability})
			if grantable {
				require.Nil(t, aerr, "%s must be grantable to a member", capability)
			} else {
				require.NotNil(t, aerr, "%s must not be grantable to a member", capability)
			}
		})
	}
	for capability, isDefault := range wantDefault {
		t.Run("default/"+capability, func(t *testing.T) {
			aerr := model.ValidateDefaultCapabilities([]string{capability})
			if isDefault {
				require.Nil(t, aerr, "%s must be a valid space default", capability)
			} else {
				require.NotNil(t, aerr, "%s must not be a valid space default", capability)
			}
		})
	}
}

// TestValidateGrantedCapabilities verifies the per-member grant validator: read_page is rejected
// as the non-grantable baseline, admin_space is accepted, and an unknown token is rejected.
func TestValidateGrantedCapabilities(t *testing.T) {
	t.Run("read_page rejected", func(t *testing.T) {
		aerr := model.ValidateGrantedCapabilities([]string{model.CapabilityReadPage})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.read_page_not_grantable.app_error", aerr.Id)
	})

	t.Run("admin_space accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateGrantedCapabilities([]string{model.CapabilityAdminSpace}))
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		aerr := model.ValidateGrantedCapabilities([]string{"not_a_real_capability"})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.unknown_capability.app_error", aerr.Id)
	})

	t.Run("every non-admin grantable capability accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateGrantedCapabilities([]string{
			model.CapabilityCreatePage, model.CapabilityCommentPage, model.CapabilityEditPage,
			model.CapabilityDeleteOwnPage, model.CapabilityDeletePage,
		}))
	})
}

// TestValidateDefaultCapabilities verifies the space-default validator: read_page and admin_space
// are both rejected (the baseline is implicit; admin is never a default), and an unknown token is
// rejected.
func TestValidateDefaultCapabilities(t *testing.T) {
	t.Run("read_page rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultCapabilities([]string{model.CapabilityReadPage})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.read_page_not_grantable.app_error", aerr.Id)
	})

	t.Run("admin_space rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultCapabilities([]string{model.CapabilityAdminSpace})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.admin_not_a_default.app_error", aerr.Id)
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		aerr := model.ValidateDefaultCapabilities([]string{"not_a_real_capability"})
		require.NotNil(t, aerr)
		require.Equal(t, "model.space_capabilities.unknown_capability.app_error", aerr.Id)
	})

	t.Run("every non-admin grantable capability accepted", func(t *testing.T) {
		require.Nil(t, model.ValidateDefaultCapabilities([]string{
			model.CapabilityCreatePage, model.CapabilityCommentPage, model.CapabilityEditPage,
			model.CapabilityDeleteOwnPage, model.CapabilityDeletePage,
		}))
	})
}
