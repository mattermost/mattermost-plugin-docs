// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/http"
	"slices"
	"strings"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// The capability vocabulary is the core page-permission id strings themselves, so the API speaks
// the same tokens core enforces — no invented level names. Consumed only as symbols (never a
// re-declared permission list), keeping the plugin single-sourced against core (see the drift
// defence discussion on the round-trip tests in space_capabilities_test.go).
var (
	// CapabilityReadPage is the baseline capability, always present in an effective set, and never
	// independently grantable (see ValidateGrantedCapabilities/ValidateDefaultCapabilities).
	CapabilityReadPage      = mmmodel.PermissionReadPage.Id
	CapabilityCreatePage    = mmmodel.PermissionCreatePage.Id
	CapabilityCommentPage   = mmmodel.PermissionCommentPage.Id
	CapabilityEditPage      = mmmodel.PermissionEditPage.Id
	CapabilityDeleteOwnPage = mmmodel.PermissionDeleteOwnPage.Id
	// CapabilityDeletePage is delete-any: it covers pages the holder does not own, and so
	// subsumes CapabilityDeleteOwnPage. Grantable independently of the admin capability, matching
	// Confluence, where Delete is assignable without Admin.
	CapabilityDeletePage = mmmodel.PermissionDeletePage.Id
	// CapabilityAdminSpace is the admin capability: a member-grant target (toggles SchemeAdmin),
	// but never a valid space-default capability.
	CapabilityAdminSpace = mmmodel.PermissionAdminSpace.Id
)

// grantableMemberCapabilities is the wire vocabulary a caller may explicitly grant to a member:
// the five atomic per-page capabilities plus the admin capability.
var grantableMemberCapabilities = map[string]bool{
	CapabilityCreatePage:    true,
	CapabilityCommentPage:   true,
	CapabilityEditPage:      true,
	CapabilityDeleteOwnPage: true,
	CapabilityDeletePage:    true,
	CapabilityAdminSpace:    true,
}

// grantableDefaultCapabilities is the wire vocabulary a space-default capability set may hold:
// the five atomic per-page capabilities. admin_space is member-grant-only, never a space default.
var grantableDefaultCapabilities = map[string]bool{
	CapabilityCreatePage:    true,
	CapabilityCommentPage:   true,
	CapabilityEditPage:      true,
	CapabilityDeleteOwnPage: true,
	CapabilityDeletePage:    true,
}

// capabilityAtomicRole maps each non-admin grantable capability to the core atomic capability
// role that carries it in ExplicitRoles.
var capabilityAtomicRole = map[string]string{
	CapabilityCreatePage:    mmmodel.SpacePageCreatorRoleId,
	CapabilityCommentPage:   mmmodel.SpacePageCommenterRoleId,
	CapabilityEditPage:      mmmodel.SpacePageEditorRoleId,
	CapabilityDeleteOwnPage: mmmodel.SpacePageDeleterOwnRoleId,
	CapabilityDeletePage:    mmmodel.SpacePageDeleterRoleId,
}

// atomicRoleCapability is the reverse of capabilityAtomicRole, used to parse a stored
// ExplicitRoles string back into the granted capability set. Derived from capabilityAtomicRole so
// the two cannot drift.
var atomicRoleCapability = func() map[string]string {
	m := make(map[string]string, len(capabilityAtomicRole))
	for capability, roleName := range capabilityAtomicRole {
		m[roleName] = capability
	}
	return m
}()

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
// which already includes read_page). Stored already normalized, like presetCapabilitySets below,
// so the accessor copies without re-deriving the canonical form on every call.
var spaceAdminEffectiveCapabilities = NormalizeCapabilitySet(mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions))

// presetCapabilitySets are the three seeded default-capability presets in wire form (read_page-
// free — the baseline is implicit and never listed), single-sourced from core's canonical
// permission slices. Stored already normalized so the lookups below compare and copy without
// re-deriving the canonical form on every call.
var presetCapabilitySets = map[string][]string{
	mmmodel.SchemeNameSpaceContribute: NormalizeCapabilitySet(stripReadPage(mmmodel.SpaceDefaultContributePermissions)),
	mmmodel.SchemeNameSpaceComment:    NormalizeCapabilitySet(stripReadPage(mmmodel.SpaceDefaultCommentPermissions)),
	mmmodel.SchemeNameSpaceReadOnly:   NormalizeCapabilitySet(stripReadPage(mmmodel.SpaceDefaultReadOnlyPermissions)),
}

// validateCapabilities validates capabilities against allowed, rejecting read_page as the
// non-grantable baseline, plus admin_space when rejectAdmin is set. An unknown token is rejected.
// Dedup-tolerant. where attributes the rejection to the calling validator.
func validateCapabilities(where string, capabilities []string, allowed map[string]bool, rejectAdmin bool) *mmmodel.AppError {
	for _, c := range capabilities {
		if c == CapabilityReadPage {
			return mmmodel.NewAppError(where, "model.space_capabilities.read_page_not_grantable.app_error", nil, "", http.StatusBadRequest)
		}
		if rejectAdmin && c == CapabilityAdminSpace {
			return mmmodel.NewAppError(where, "model.space_capabilities.admin_not_a_default.app_error", nil, "", http.StatusBadRequest)
		}
		if !allowed[c] {
			return mmmodel.NewAppError(where, "model.space_capabilities.unknown_capability.app_error", map[string]any{"Capability": c}, "", http.StatusBadRequest)
		}
	}
	return nil
}

// ValidateGrantedCapabilities validates a per-member granted-capability request: each token must
// be one of the grantable member capabilities.
func ValidateGrantedCapabilities(capabilities []string) *mmmodel.AppError {
	return validateCapabilities("ValidateGrantedCapabilities", capabilities, grantableMemberCapabilities, false)
}

// ValidateDefaultCapabilities validates a space-default capability set: same rule as
// ValidateGrantedCapabilities, plus admin_space is also rejected — a space default is never
// admin-granting.
func ValidateDefaultCapabilities(capabilities []string) *mmmodel.AppError {
	return validateCapabilities("ValidateDefaultCapabilities", capabilities, grantableDefaultCapabilities, true)
}

// RolesForCapabilities maps the requested non-admin capabilities to their atomic role names for
// ExplicitRoles, and reports whether admin_space was requested (schemeAdmin). The base
// schemeUserRole token is always emitted first — core rejects a member update that leaves the base
// scheme role unset. Pure: schemeUserRole (the scheme's generated user-role name) comes from the
// caller, resolved via a store lookup elsewhere.
func RolesForCapabilities(capabilities []string, schemeUserRole string) (explicitRoles string, schemeAdmin bool) {
	roles := []string{schemeUserRole}
	for _, c := range NormalizeCapabilitySet(capabilities) {
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
// not an atomic capability role (a generated per-scheme role granting exactly one page capability)
// is ignored (harmless if the base scheme token is passed too).
// defaultCapabilities is the space's default capability set (wire form, read_page-free).
//
// A SchemeGuest member's effective set is read_page alone — neither the space default nor its own
// granted set. A guest resolves through the read-only DefaultChannelGuestRole rather than the
// scheme's user-role default, and the write gate holds a guest to read_page even when a grant made
// before a demotion is still recorded in ExplicitRoles. Granted still reports what is recorded, so
// the two fields together show a stale grant rather than hiding it.
//
// A SchemeAdmin member's effective set additionally includes the full canonical admin
// capability set, since SchemeAdmin resolves through DefaultChannelAdminRole regardless of what is
// (or isn't) recorded in ExplicitRoles/the space default. Pure: the model never touches the store.
func CapabilitiesFromMember(explicitRoles string, schemeAdmin, schemeGuest bool, defaultCapabilities []string) MemberCapabilities {
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
	if !schemeGuest {
		if schemeAdmin {
			for _, c := range spaceAdminEffectiveCapabilities {
				effective[c] = true
			}
		}
		for _, c := range defaultCapabilities {
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
	// Cloned, not returned directly: the set is package state a caller must not be able to mutate
	// through the returned slice.
	return slices.Clone(spaceAdminEffectiveCapabilities)
}

// DefaultCapabilitiesFromPermissions projects a pooled scheme's stored user-role permission set
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
	capabilities, ok := presetCapabilitySets[name]
	if !ok {
		return nil, false
	}
	// Cloned, not returned directly: the presets are package state a caller must not be able to
	// mutate through the returned slice.
	return slices.Clone(capabilities), true
}

// SharedSchemeNamePrefix labels every scheme in the shared default-capability pool. The suffix is
// derived from the capability set, so one scheme serves every space configured that way. Exported
// so a caller matching pooled scheme names resolves the prefix from here rather than restating it.
const SharedSchemeNamePrefix = "docs_space_default_"

// sharedSchemeDisplayNamePrefix opens the operator-facing name of a pooled scheme, which then lists
// the capability set the scheme grants.
const sharedSchemeDisplayNamePrefix = "Space defaults: "

// sharedSchemeNameDigestLength is how much of the digest the pool scheme name carries. Together
// with SharedSchemeNamePrefix it fits core's 64-character limit with room to spare, and 64 bits is
// far more than a vocabulary of a few tokens can collide within.
const sharedSchemeNameDigestLength = 16

// SharedSchemeNameForCapabilities returns the pool scheme name expressing capabilities: a
// deterministic function of the capability set, so two spaces configured the same way resolve to
// one shared scheme rather than each owning an identical private copy. The suffix is a digest
// rather than the tokens themselves, which keeps the name inside core's 64-character
// [a-z0-9_] limit and — unlike a positional encoding — leaves existing names meaning what they
// always meant when the capability vocabulary grows.
func SharedSchemeNameForCapabilities(capabilities []string) string {
	sum := sha256.Sum256([]byte(strings.Join(NormalizeCapabilitySet(capabilities), " ")))
	return SharedSchemeNamePrefix + hex.EncodeToString(sum[:])[:sharedSchemeNameDigestLength]
}

// SharedSchemeDisplayNameForCapabilities returns the operator-facing name of the pooled scheme for
// capabilities, listing the set so the digest in the scheme name is legible in the System Console.
// Truncated to core's DisplayName limit, which the current vocabulary is far short of.
func SharedSchemeDisplayNameForCapabilities(capabilities []string) string {
	name := sharedSchemeDisplayNamePrefix + strings.Join(NormalizeCapabilitySet(capabilities), ", ")
	if len(name) > mmmodel.SchemeDisplayNameMaxLength {
		name = name[:mmmodel.SchemeDisplayNameMaxLength]
	}
	return name
}

// SchemeNameForDefaultCapabilities returns the seeded preset scheme name matching capabilities, or false
// if capabilities does not match any preset. Recognition is set equality — order-insensitive, deduplicated
// — never a raw array comparison.
func SchemeNameForDefaultCapabilities(capabilities []string) (string, bool) {
	normalized := NormalizeCapabilitySet(capabilities)
	for name, preset := range presetCapabilitySets {
		if slices.Equal(preset, normalized) {
			return name, true
		}
	}
	return "", false
}

// NormalizeCapabilitySet dedupes and sorts capabilities into a deterministic, non-nil capability slice.
func NormalizeCapabilitySet(capabilities []string) []string {
	seen := make(map[string]bool, len(capabilities))
	out := make([]string, 0, len(capabilities))
	for _, c := range capabilities {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}
