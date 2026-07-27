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

// The capability vocabulary is the core page-permission id strings themselves, so the API speaks
// the same tokens core enforces — no invented level names. Consumed only as symbols (never a
// re-declared permission list), keeping the plugin single-sourced against core (see the drift
// defence discussion on the round-trip tests below).
var (
	// CapabilityReadPage is the baseline capability, always present in an effective set, and never
	// independently grantable (see ValidateGrantedCapabilities/ValidateDefaultCapabilities).
	CapabilityReadPage      = mmmodel.PermissionReadPage.Id
	CapabilityCreatePage    = mmmodel.PermissionCreatePage.Id
	CapabilityCommentPage   = mmmodel.PermissionCommentPage.Id
	CapabilityEditPage      = mmmodel.PermissionEditPage.Id
	CapabilityDeleteOwnPage = mmmodel.PermissionDeleteOwnPage.Id
	// CapabilityDeletePage (delete-any) is never independently grantable — it rides only the
	// admin capability (SchemeAdmin), never an ExplicitRoles atomic role.
	CapabilityDeletePage = mmmodel.PermissionDeletePage.Id
	// CapabilityAdminSpace is the admin capability: a member-grant target (toggles SchemeAdmin),
	// but never a valid space-default capability.
	CapabilityAdminSpace = mmmodel.PermissionAdminSpace.Id
)

// grantableMemberCapabilities is the wire vocabulary a caller may explicitly grant to a member:
// the four atomic per-page capabilities plus the admin capability.
var grantableMemberCapabilities = map[string]bool{
	CapabilityCreatePage:    true,
	CapabilityCommentPage:   true,
	CapabilityEditPage:      true,
	CapabilityDeleteOwnPage: true,
	CapabilityAdminSpace:    true,
}

// grantableDefaultCapabilities is the wire vocabulary a space-default capability set may hold:
// the four atomic per-page capabilities. admin_space is member-grant-only, never a space default.
var grantableDefaultCapabilities = map[string]bool{
	CapabilityCreatePage:    true,
	CapabilityCommentPage:   true,
	CapabilityEditPage:      true,
	CapabilityDeleteOwnPage: true,
}

// capabilityAtomicRole maps each non-admin grantable capability to the core atomic capability
// role that carries it in ExplicitRoles.
var capabilityAtomicRole = map[string]string{
	CapabilityCreatePage:    mmmodel.SpacePageCreatorRoleId,
	CapabilityCommentPage:   mmmodel.SpacePageCommenterRoleId,
	CapabilityEditPage:      mmmodel.SpacePageEditorRoleId,
	CapabilityDeleteOwnPage: mmmodel.SpacePageDeleterOwnRoleId,
}

// atomicRoleCapability is the reverse of capabilityAtomicRole, used to parse a stored
// ExplicitRoles string back into the granted capability set.
var atomicRoleCapability = map[string]string{
	mmmodel.SpacePageCreatorRoleId:    CapabilityCreatePage,
	mmmodel.SpacePageCommenterRoleId:  CapabilityCommentPage,
	mmmodel.SpacePageEditorRoleId:     CapabilityEditPage,
	mmmodel.SpacePageDeleterOwnRoleId: CapabilityDeleteOwnPage,
}

// stripReadPage projects a core permission slice onto its wire id strings with the implicit
// read_page baseline removed, so a canonical core permission set can be single-sourced into the
// read_page-free wire vocabulary without drift.
func stripReadPage(permissions []*mmmodel.Permission) []string {
	return slices.DeleteFunc(mmmodel.PermissionIDs(permissions), func(id string) bool {
		return id == CapabilityReadPage
	})
}

// spaceAdminEffectiveCapabilities is the full capability set a SchemeAdmin member effectively
// holds, single-sourced from core's canonical admin permission slice (SpaceAdminRolePermissions,
// which already includes read_page).
var spaceAdminEffectiveCapabilities = mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions)

// presetCapabilitySets are the three seeded default-capability presets in wire form (read_page-
// free — the baseline is implicit and never listed), single-sourced from core's canonical
// permission slices.
var presetCapabilitySets = map[string][]string{
	mmmodel.SchemeNameSpaceContribute: stripReadPage(mmmodel.SpaceDefaultContributePermissions),
	mmmodel.SchemeNameSpaceComment:    stripReadPage(mmmodel.SpaceDefaultCommentPermissions),
	mmmodel.SchemeNameSpaceReadOnly:   stripReadPage(mmmodel.SpaceDefaultReadOnlyPermissions),
}

// ValidateGrantedCapabilities validates a per-member granted-capability request: each token must
// be one of the grantable member capabilities. read_page is rejected as the non-grantable
// baseline and delete_page as admin-only; an unknown token is rejected. Dedup-tolerant.
func ValidateGrantedCapabilities(caps []string) *mmmodel.AppError {
	for _, c := range caps {
		if c == CapabilityReadPage {
			return mmmodel.NewAppError("ValidateGrantedCapabilities", "model.space_capabilities.read_page_not_grantable.app_error", nil, "", http.StatusBadRequest)
		}
		if c == CapabilityDeletePage {
			return mmmodel.NewAppError("ValidateGrantedCapabilities", "model.space_capabilities.delete_page_not_grantable.app_error", nil, "", http.StatusBadRequest)
		}
		if !grantableMemberCapabilities[c] {
			return mmmodel.NewAppError("ValidateGrantedCapabilities", "model.space_capabilities.unknown_capability.app_error", map[string]any{"Capability": c}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// ValidateDefaultCapabilities validates a space-default capability set: same rule as
// ValidateGrantedCapabilities, plus admin_space is also rejected — a space default is never
// admin-granting.
func ValidateDefaultCapabilities(caps []string) *mmmodel.AppError {
	for _, c := range caps {
		if c == CapabilityReadPage {
			return mmmodel.NewAppError("ValidateDefaultCapabilities", "model.space_capabilities.read_page_not_grantable.app_error", nil, "", http.StatusBadRequest)
		}
		if c == CapabilityDeletePage {
			return mmmodel.NewAppError("ValidateDefaultCapabilities", "model.space_capabilities.delete_page_not_grantable.app_error", nil, "", http.StatusBadRequest)
		}
		if c == CapabilityAdminSpace {
			return mmmodel.NewAppError("ValidateDefaultCapabilities", "model.space_capabilities.admin_not_a_default.app_error", nil, "", http.StatusBadRequest)
		}
		if !grantableDefaultCapabilities[c] {
			return mmmodel.NewAppError("ValidateDefaultCapabilities", "model.space_capabilities.unknown_capability.app_error", map[string]any{"Capability": c}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// RolesForCapabilities maps the requested non-admin capabilities to their atomic role names for
// ExplicitRoles, and reports whether admin_space was requested (schemeAdmin). The base
// schemeUserRole token is always emitted first — core rejects a member update that leaves the base
// scheme role unset. Pure: schemeUserRole (the scheme's generated user-role name) comes from the
// caller, resolved via a store lookup elsewhere.
func RolesForCapabilities(caps []string, schemeUserRole string) (explicitRoles string, schemeAdmin bool) {
	roles := []string{schemeUserRole}
	for _, c := range NormalizeCapabilitySet(caps) {
		if c == CapabilityAdminSpace {
			schemeAdmin = true
			continue
		}
		if roleName, ok := capabilityAtomicRole[c]; ok {
			roles = append(roles, roleName)
		}
	}
	return strings.Join(roles, " "), schemeAdmin
}

// MemberCapabilities is the reverse projection of a member's stored role state onto the capability
// vocabulary: Effective is the member's effective capability set, Granted is the per-member
// granted set beyond the space default.
type MemberCapabilities struct {
	Effective []string
	Granted   []string
	IsAdmin   bool
	IsGuest   bool
}

// CapabilitiesFromMember reverse-projects a member's raw role state onto the capability vocabulary.
// explicitRoles is the raw space-delimited ChannelMember.ExplicitRoles string; any token that is
// not an atomic capability role is ignored (harmless if the base scheme token is passed too).
// defaultCaps is the space's default capability set (wire form, read_page-free).
//
// A SchemeGuest member's effective set is read_page union Granted only — never the space default,
// since a guest resolves through the read-only DefaultChannelGuestRole, not the scheme's user-role
// default. A SchemeAdmin member's effective set additionally includes the full canonical admin
// capability set, since SchemeAdmin resolves through DefaultChannelAdminRole regardless of what is
// (or isn't) recorded in ExplicitRoles/the space default. Pure: the model never touches the store.
func CapabilitiesFromMember(explicitRoles string, schemeAdmin, schemeGuest bool, defaultCaps []string) MemberCapabilities {
	granted := make(map[string]bool)
	for token := range strings.FieldsSeq(explicitRoles) {
		if c, ok := atomicRoleCapability[token]; ok {
			granted[c] = true
		}
	}
	if schemeAdmin {
		granted[CapabilityAdminSpace] = true
	}
	grantedList := NormalizeCapabilitySet(slices.Collect(maps.Keys(granted)))

	effective := map[string]bool{CapabilityReadPage: true}
	switch {
	case schemeGuest:
		for _, c := range grantedList {
			effective[c] = true
		}
	case schemeAdmin:
		for _, c := range spaceAdminEffectiveCapabilities {
			effective[c] = true
		}
		for _, c := range defaultCaps {
			effective[c] = true
		}
		for _, c := range grantedList {
			effective[c] = true
		}
	default:
		for _, c := range defaultCaps {
			effective[c] = true
		}
		for _, c := range grantedList {
			effective[c] = true
		}
	}

	return MemberCapabilities{
		Effective: NormalizeCapabilitySet(slices.Collect(maps.Keys(effective))),
		Granted:   grantedList,
		IsAdmin:   schemeAdmin,
		IsGuest:   schemeGuest,
	}
}

// AdminEffectiveCapabilities returns the full capability set a SchemeAdmin effectively holds, wire
// form, non-nil.
func AdminEffectiveCapabilities() []string {
	return NormalizeCapabilitySet(spaceAdminEffectiveCapabilities)
}

// DefaultCapabilitiesFromPermissions projects a custom scheme's stored user-role permission set
// (raw core permission ids) onto the wire default-capability vocabulary: read_page (the implicit
// baseline) and any non-default-capability permission are stripped; only the grantable default
// capability tokens survive.
func DefaultCapabilitiesFromPermissions(permissions []string) []string {
	out := make([]string, 0, len(permissions))
	for _, p := range permissions {
		if grantableDefaultCapabilities[p] {
			out = append(out, p)
		}
	}
	return NormalizeCapabilitySet(out)
}

// DefaultCapabilitiesForSchemeName returns the wire-form default capability set for one of the
// three seeded preset scheme names, or false if name is not a preset.
func DefaultCapabilitiesForSchemeName(name string) ([]string, bool) {
	caps, ok := presetCapabilitySets[name]
	if !ok {
		return nil, false
	}
	return NormalizeCapabilitySet(caps), true
}

// SchemeNameForDefaultCapabilities returns the seeded preset scheme name matching caps, or false
// if caps does not match any preset. Recognition is set equality — order-insensitive, deduplicated
// — never a raw array comparison.
func SchemeNameForDefaultCapabilities(caps []string) (string, bool) {
	normalized := NormalizeCapabilitySet(caps)
	for name, preset := range presetCapabilitySets {
		if slices.Equal(NormalizeCapabilitySet(preset), normalized) {
			return name, true
		}
	}
	return "", false
}

// NormalizeCapabilitySet dedupes and sorts caps into a deterministic, non-nil capability slice.
func NormalizeCapabilitySet(caps []string) []string {
	seen := make(map[string]bool, len(caps))
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}
