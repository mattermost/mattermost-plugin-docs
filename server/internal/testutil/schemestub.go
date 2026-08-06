// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// presetSchemeFixture is the fixed identity and role/permission shape stubbed for one of the three
// seeded preset space schemes (see mmmodel.SpaceSchemeNames) — mirroring the paired core branch's
// core seeding migration, which creates these same three schemes with generated per-scheme roles
// in production.
type presetSchemeFixture struct {
	schemeID   string
	userRole   string
	adminRole  string
	guestRole  string
	userPerms  []*mmmodel.Permission
	adminPerms []*mmmodel.Permission
	guestPerms []*mmmodel.Permission
}

// presetSchemeFixtures holds one fixture per preset name, keyed by the scheme's Name. Scheme ids
// are fixed 26-char valid model ids (mmmodel.IsValidId) so MustSeedChannelScheme and
// StubPresetSchemes agree on which id a preset resolves to.
var presetSchemeFixtures = map[string]presetSchemeFixture{
	mmmodel.SchemeNameSpaceContribute: {
		schemeID:   "contributescheme0000000000",
		userRole:   "space_contribute_user_role",
		adminRole:  "space_contribute_admin_role",
		guestRole:  "space_contribute_guest_role",
		userPerms:  mmmodel.SpaceDefaultContributePermissions,
		adminPerms: mmmodel.SpaceAdminRolePermissions,
		guestPerms: []*mmmodel.Permission{mmmodel.PermissionReadPage},
	},
	mmmodel.SchemeNameSpaceComment: {
		schemeID:   "commentscheme0000000000000",
		userRole:   "space_comment_user_role",
		adminRole:  "space_comment_admin_role",
		guestRole:  "space_comment_guest_role",
		userPerms:  mmmodel.SpaceDefaultCommentPermissions,
		adminPerms: mmmodel.SpaceAdminRolePermissions,
		guestPerms: []*mmmodel.Permission{mmmodel.PermissionReadPage},
	},
	mmmodel.SchemeNameSpaceReadOnly: {
		schemeID:   "readonlyscheme000000000000",
		userRole:   "space_readonly_user_role",
		adminRole:  "space_readonly_admin_role",
		guestRole:  "space_readonly_guest_role",
		userPerms:  mmmodel.SpaceDefaultReadOnlyPermissions,
		adminPerms: mmmodel.SpaceAdminRolePermissions,
		guestPerms: []*mmmodel.Permission{mmmodel.PermissionReadPage},
	},
}

// schemeRoleNames is the generated guest/user/admin role names one scheme id resolves to.
type schemeRoleNames struct {
	Guest string
	User  string
	Admin string
}

// schemeRoleRegistry maps a scheme id to its generated role names, so a stubbed channel resolves
// whichever scheme it currently points at. It is keyed by globally unique scheme ids — the three
// fixed preset ids plus whatever a test registers for a custom scheme — so entries never collide
// across tests. Guarded because a test binary may run packages in parallel.
var schemeRoleRegistry = struct {
	sync.RWMutex
	byScheme map[string]schemeRoleNames
}{byScheme: presetSchemeRoleNames()}

func presetSchemeRoleNames() map[string]schemeRoleNames {
	out := make(map[string]schemeRoleNames, len(presetSchemeFixtures))
	for _, fx := range presetSchemeFixtures {
		out[fx.schemeID] = schemeRoleNames{Guest: fx.guestRole, User: fx.userRole, Admin: fx.adminRole}
	}
	return out
}

// RegisterSchemeRoles records the generated role names schemeID resolves to, so a channel later
// repointed at it resolves that scheme's roles through GetSchemeRolesForChannel. The three presets
// are registered already; a test creating a custom scheme registers its own.
func RegisterSchemeRoles(schemeID, guestRole, userRole, adminRole string) {
	schemeRoleRegistry.Lock()
	defer schemeRoleRegistry.Unlock()
	schemeRoleRegistry.byScheme[schemeID] = schemeRoleNames{Guest: guestRole, User: userRole, Admin: adminRole}
}

func rolesForScheme(schemeID string) (schemeRoleNames, bool) {
	schemeRoleRegistry.RLock()
	defer schemeRoleRegistry.RUnlock()
	roles, ok := schemeRoleRegistry.byScheme[schemeID]
	return roles, ok
}

// PresetSchemeID returns the fixed scheme id StubPresetSchemes stubbed for the named preset (see
// mmmodel.SpaceSchemeNames) — exported for callers that need to seed a *model.Channel pointing at a
// specific preset scheme directly, rather than through MustSeedChannelScheme.
func PresetSchemeID(name string) string {
	return presetSchemeFixtures[name].schemeID
}

// StubPresetSchemes registers .Maybe() stubs on mockAPI resolving each of the three preset space
// schemes by name, plus their three generated roles by name — the shape
// getSchemeIDByName/getRolePermissionsByName resolve through the plugin API in production. Call
// this once per mockAPI, alongside StubDefaultSpacePermissions.
func StubPresetSchemes(mockAPI *plugintest.API) {
	for name, fx := range presetSchemeFixtures {
		scheme := &mmmodel.Scheme{
			Id:                      fx.schemeID,
			Name:                    name,
			Scope:                   mmmodel.SchemeScopeChannel,
			DefaultChannelUserRole:  fx.userRole,
			DefaultChannelAdminRole: fx.adminRole,
			DefaultChannelGuestRole: fx.guestRole,
		}
		mockAPI.On("GetSchemeByName", name).Return(scheme, nil).Maybe()
		StubRole(mockAPI, fx.userRole, mmmodel.PermissionIDs(fx.userPerms))
		StubRole(mockAPI, fx.adminRole, mmmodel.PermissionIDs(fx.adminPerms))
		StubRole(mockAPI, fx.guestRole, mmmodel.PermissionIDs(fx.guestPerms))
	}
}

// roleRegistry maps a stubbed role's id to the shared *Role object, so a PatchRole stub keyed by
// id (the shape core's PatchRole takes) reaches the same object a GetRoleByName stub hands out.
// Keyed by generated ids, so entries never collide across tests. Guarded because a test binary may
// run packages in parallel.
var roleRegistry = struct {
	sync.RWMutex
	byID map[string]*mmmodel.Role
}{byID: map[string]*mmmodel.Role{}}

func roleByID(roleID string) (*mmmodel.Role, bool) {
	roleRegistry.RLock()
	defer roleRegistry.RUnlock()
	role, ok := roleRegistry.byID[roleID]
	return role, ok
}

// registerRoleByID records role under its id so a PatchRole stub, which core keys by id, reaches
// the same object a GetRoleByName stub handed out. Ids are generated per role, so unlike role names
// they never collide across tests.
func registerRoleByID(role *mmmodel.Role) {
	roleRegistry.Lock()
	defer roleRegistry.Unlock()
	roleRegistry.byID[role.Id] = role
}

// StubbedRoleName resolves a stubbed role's id back to its name, so a test asserting on PatchRole —
// which core takes by id — can still express its expectation in role names.
func StubbedRoleName(roleID string) (string, bool) {
	role, ok := roleByID(roleID)
	if !ok {
		return "", false
	}
	return role.Name, true
}

// stubbedChannels holds every space channel a stub has handed out, keyed by the mockAPI it was
// registered on and then by channel id, so StubPatchRole can answer which schemes currently govern
// a space. The channel objects are shared with the tests that mutate them, so a repointed SchemeId
// is visible here without re-registration.
//
// Keyed by mockAPI rather than by channel id alone: a pooled scheme's role names are a digest of
// its capability set, so two tests asking for the same set generate the same role names under
// different scheme ids. A flat registry would let a channel one test attached authorize a role
// write another test expects to be refused — hiding the very ordering regression the guard in
// StubPatchRole exists to catch. One mockAPI is one test, and it is also what confines a channel
// object to the goroutine mutating its SchemeId.
var stubbedChannels = struct {
	sync.RWMutex
	byAPI map[*plugintest.API]map[string]*mmmodel.Channel
}{byAPI: map[*plugintest.API]map[string]*mmmodel.Channel{}}

func registerStubbedChannel(mockAPI *plugintest.API, channel *mmmodel.Channel) {
	if channel == nil || channel.Id == "" {
		return
	}
	stubbedChannels.Lock()
	defer stubbedChannels.Unlock()
	channels, ok := stubbedChannels.byAPI[mockAPI]
	if !ok {
		channels = map[string]*mmmodel.Channel{}
		stubbedChannels.byAPI[mockAPI] = channels
	}
	channels[channel.Id] = channel
}

func isPooledSchemeName(name string) bool {
	return strings.HasPrefix(name, model.SharedSchemeNamePrefix)
}

// StubPooledSchemeMiss answers the shared-pool lookup with not-found for any pooled scheme name, so
// a test exercising a non-preset default-capability set reaches the create path. Register it after
// StubPresetSchemes, whose per-name stubs must keep matching first, and before any stub that
// answers a specific pooled name. Use StubSchemePool instead when the test needs the pool to
// actually accumulate.
func StubPooledSchemeMiss(mockAPI *plugintest.API) {
	mockAPI.On("GetSchemeByName", mock.MatchedBy(isPooledSchemeName)).
		Return(nil, &mmmodel.AppError{Id: "app.scheme.get.app_error", StatusCode: http.StatusNotFound}).Maybe()
}

// StubSchemePool simulates the shared default-capability pool with state: a pooled scheme name
// resolves to not-found until CreateScheme mints it and to that same scheme afterwards. This is
// what lets a test assert the property the pool exists for — that two spaces configured alike, or
// one space returning to a set it used before, resolve to a single scheme instead of minting a
// second identical one.
//
// The generated role names are derived from the scheme name so they can be answered by a standing
// stub; registering new expectations from inside a running mock call would race the mock's own lock.
func StubSchemePool(mockAPI *plugintest.API) {
	var mu sync.Mutex
	byName := map[string]*mmmodel.Scheme{}

	mockAPI.On("GetSchemeByName", mock.MatchedBy(isPooledSchemeName)).
		Return(func(name string) (*mmmodel.Scheme, *mmmodel.AppError) {
			mu.Lock()
			defer mu.Unlock()
			if scheme, ok := byName[name]; ok {
				return scheme, nil
			}
			return nil, &mmmodel.AppError{Id: "app.scheme.get.app_error", StatusCode: http.StatusNotFound}
		}).Maybe()

	mockAPI.On("CreateScheme", mock.MatchedBy(func(scheme *mmmodel.Scheme) bool {
		return scheme != nil && isPooledSchemeName(scheme.Name)
	})).Return(func(in *mmmodel.Scheme) (*mmmodel.Scheme, *mmmodel.AppError) {
		mu.Lock()
		defer mu.Unlock()
		if existing, ok := byName[in.Name]; ok {
			return existing, nil
		}
		scheme := &mmmodel.Scheme{
			Id:                      mmmodel.NewId(),
			Name:                    in.Name,
			DisplayName:             in.DisplayName,
			Scope:                   mmmodel.SchemeScopeChannel,
			DefaultChannelUserRole:  in.Name + "_user",
			DefaultChannelAdminRole: in.Name + "_admin",
			DefaultChannelGuestRole: in.Name + "_guest",
		}
		byName[in.Name] = scheme
		RegisterSchemeRoles(scheme.Id, scheme.DefaultChannelGuestRole, scheme.DefaultChannelUserRole, scheme.DefaultChannelAdminRole)
		return scheme, nil
	}).Maybe()

	stubPooledRoles(mockAPI)
	StubPatchRole(mockAPI)
}

// stubPooledRoles answers GetRoleByName for any role generated for a pooled scheme, minting the
// shared *Role on first read so a later PatchRole mutation is visible to a subsequent read.
//
// The name-keyed map is per-call rather than package-level: a pooled scheme's name is a digest of
// its capability set, so two tests asking for the same set generate the same role names, and a
// shared map would hand the second test the first one's already-patched role.
func stubPooledRoles(mockAPI *plugintest.API) {
	var mu sync.Mutex
	byName := map[string]*mmmodel.Role{}

	mockAPI.On("GetRoleByName", mock.MatchedBy(isPooledSchemeName)).
		Return(func(roleName string) (*mmmodel.Role, *mmmodel.AppError) {
			mu.Lock()
			defer mu.Unlock()
			if role, ok := byName[roleName]; ok {
				return role, nil
			}
			role := &mmmodel.Role{Id: mmmodel.NewId(), Name: roleName}
			byName[roleName] = role
			registerRoleByID(role)
			return role, nil
		}).Maybe()
}

// StubRole registers a GetRoleByName stub returning one shared *Role carrying permissions, so a
// PatchRole stub that mutates it (see StubPatchRole) is visible to a later read of the same role —
// the mock does not track state on its own. The role is given an id and registered under it, since
// production patches by id. Returns the shared Role.
func StubRole(mockAPI *plugintest.API, roleName string, permissions []string) *mmmodel.Role {
	role := &mmmodel.Role{Id: mmmodel.NewId(), Name: roleName, Permissions: permissions}
	roleRegistry.Lock()
	roleRegistry.byID[role.Id] = role
	roleRegistry.Unlock()
	mockAPI.On("GetRoleByName", roleName).Return(role, nil).Maybe()
	return role
}

// addsSpacePermission reports whether next introduces a space channel-scoped permission the role
// does not already hold. Core refuses those additions on an unattached scheme but always allows a
// removal, so a patch that only drops permissions is admissible either way.
func addsSpacePermission(role *mmmodel.Role, next []string) bool {
	held := make(map[string]bool, len(role.Permissions))
	for _, id := range role.Permissions {
		held[id] = true
	}
	for _, id := range next {
		if !held[id] && mmmodel.IsSpaceChannelScopedPermissionID(id) {
			return true
		}
	}
	return false
}

// schemeGovernsSpace reports whether roleName's scheme carries space authority: a seeded preset
// carries it by name (mirroring mmmodel.IsSpaceSchemeName, whose accepted set is frozen at init),
// and any other scheme carries it only while a space channel stubbed on this same mockAPI points
// at it.
func schemeGovernsSpace(mockAPI *plugintest.API, roleName string) bool {
	for _, fx := range presetSchemeFixtures {
		if roleName == fx.guestRole || roleName == fx.userRole || roleName == fx.adminRole {
			return true
		}
	}
	stubbedChannels.RLock()
	defer stubbedChannels.RUnlock()
	for _, channel := range stubbedChannels.byAPI[mockAPI] {
		if channel.SchemeId == nil || *channel.SchemeId == "" {
			continue
		}
		roles, ok := rolesForScheme(*channel.SchemeId)
		if !ok {
			continue
		}
		if roleName == roles.Guest || roleName == roles.User || roleName == roles.Admin {
			return true
		}
	}
	return false
}

// StubPatchRole registers a PatchRole stub applying the patch's permission set to the shared *Role
// the matching GetRoleByName stub returns, so the write is observable to a later read. Roles are
// looked up by id, mirroring core's own PatchRole, which re-reads the stored role rather than
// trusting one supplied by the caller.
//
// The stub also reproduces core's scope guard: adding a space permission is refused unless the
// role's scheme already governs a space, which is what makes the attach-before-patch ordering in
// CreateSpace and SetSpaceDefaultCapabilities mandatory rather than stylistic. Without the guard
// here, reversing that order still passes every test that uses this stub.
func StubPatchRole(mockAPI *plugintest.API) {
	mockAPI.On("PatchRole", mock.AnythingOfType("string"), mock.AnythingOfType("*model.RolePatch")).
		Return(func(roleID string, patch *mmmodel.RolePatch) (*mmmodel.Role, *mmmodel.AppError) {
			role, ok := roleByID(roleID)
			if !ok {
				return nil, &mmmodel.AppError{Id: "app.role.get.app_error", StatusCode: http.StatusNotFound}
			}
			if patch != nil && patch.Permissions != nil {
				if addsSpacePermission(role, *patch.Permissions) && !schemeGovernsSpace(mockAPI, role.Name) {
					// Core owns the id for this refusal and the plugin only branches on the status,
					// so this stands in with the status core returns rather than inventing an id the
					// plugin's translation file would then carry.
					return nil, &mmmodel.AppError{Id: "app.role.patch.app_error", StatusCode: http.StatusBadRequest}
				}
				role.Permissions = *patch.Permissions
			}
			return role, nil
		}).Maybe()
}

// MustSeedChannelScheme registers mock stubs so channelID resolves to the named seeded space scheme
// preset: GetChannelOfType returns a channel pointing at the preset's id, and
// GetSchemeRolesForChannel resolves whichever scheme that channel currently points at — so a test
// that repoints the channel sees the new scheme's roles without re-stubbing, the way core resolves
// them. Production sets this up through the real backing channel's SchemeId when
// CreateSpace/SetSpaceDefaultCapabilities points a space at a scheme.
//
// Every matching call returns the same shared *Channel, since a space's scheme-resolving paths may
// call GetChannelOfType more than once per test; a test that also needs to simulate the channel's
// archived/live state (e.g. RestoreSpace's backingChannelArchived check) mutates the returned
// Channel's DeleteAt directly rather than registering a second, competing stub. Marked .Maybe() so
// a test whose flow never reaches a scheme lookup does not fail AssertExpectations on an unused
// stub. Returns the shared Channel.
func MustSeedChannelScheme(t *testing.T, mockAPI *plugintest.API, channelID, schemeName string) *mmmodel.Channel {
	t.Helper()
	require.NotNil(t, mockAPI, "MustSeedChannelScheme needs a mock to register the channel's scheme stubs on")
	fx, ok := presetSchemeFixtures[schemeName]
	require.True(t, ok, "unknown preset space scheme name %q", schemeName)

	schemeID := fx.schemeID
	channel := &mmmodel.Channel{Id: channelID, Type: mmmodel.ChannelTypeSpace, SchemeId: &schemeID}
	StubChannelScheme(mockAPI, channelID, channel)
	return channel
}

// StubChannelScheme wires the three calls that resolve one channel's scheme: the channel read, the
// scheme-role resolution that follows the channel's current SchemeId, and the metadata-sync write.
//
// UpdateChannel must return the SAME shared object GetChannelOfType hands out, because pluginapi's
// Channel.Update copies the returned channel back over the passed one — returning a fresh channel
// would wipe SchemeId and 404 the next scheme-resolving read.
func StubChannelScheme(mockAPI *plugintest.API, channelID string, channel *mmmodel.Channel) {
	registerStubbedChannel(mockAPI, channel)
	mockAPI.On("GetChannelOfType", channelID, mmmodel.ChannelTypeSpace).Return(channel, nil).Maybe()
	mockAPI.On("UpdateChannel", channel).Return(channel, nil).Maybe()
	mockAPI.On("GetSchemeRolesForChannel", channelID).
		Return(func(string) (string, string, string, *mmmodel.AppError) {
			return resolveChannelRoles(channel)
		}).Maybe()
}

// resolveChannelRoles mirrors core's GetSchemeRolesForChannel for a stubbed channel: the roles of
// whichever scheme the channel currently points at. A channel with no scheme resolves to empty role
// names, matching core. A channel pointing at a scheme no test registered resolves to not-found, so
// a test relying on an unregistered scheme fails visibly rather than reading empty role names as a
// successful lookup.
func resolveChannelRoles(channel *mmmodel.Channel) (string, string, string, *mmmodel.AppError) {
	if channel == nil || channel.SchemeId == nil || *channel.SchemeId == "" {
		return "", "", "", nil
	}
	roles, ok := rolesForScheme(*channel.SchemeId)
	if !ok {
		// Built as a literal, like the other stand-ins for this core error above: the id belongs to
		// core, and constructing it through NewAppError would enter the plugin's own translation
		// file as an untranslated string it never emits.
		return "", "", "", &mmmodel.AppError{Id: "app.scheme.get.app_error", StatusCode: http.StatusNotFound}
	}
	return roles.Guest, roles.User, roles.Admin, nil
}

// StubDefaultChannelScheme registers .Maybe() catch-all stubs resolving any channel not given its
// own specific stub (MustSeedChannelScheme, or a test's own registration) to the contribute preset
// scheme, live (DeleteAt 0) — the same default CreateSpace/MustCreateSpaceWithScheme use for a
// space's backing channel. Each channel id gets its OWN channel object carrying that id, so two
// spaces in one test never alias each other's scheme or metadata.
//
// Register this last: mock.Mock matches expectations in registration order, so a channel-specific
// stub must be registered before this catch-all to take precedence.
func StubDefaultChannelScheme(mockAPI *plugintest.API) {
	var mu sync.Mutex
	byID := map[string]*mmmodel.Channel{}
	defaultChannel := func(channelID string) *mmmodel.Channel {
		mu.Lock()
		defer mu.Unlock()
		if ch, ok := byID[channelID]; ok {
			return ch
		}
		schemeID := PresetSchemeID(mmmodel.SchemeNameSpaceContribute)
		ch := &mmmodel.Channel{Id: channelID, Type: mmmodel.ChannelTypeSpace, SchemeId: &schemeID}
		byID[channelID] = ch
		registerStubbedChannel(mockAPI, ch)
		return ch
	}

	mockAPI.On("GetChannelOfType", mock.AnythingOfType("string"), mock.Anything).
		Return(func(channelID string, _ mmmodel.ChannelType) (*mmmodel.Channel, *mmmodel.AppError) {
			return defaultChannel(channelID), nil
		}).Maybe()
	mockAPI.On("GetSchemeRolesForChannel", mock.AnythingOfType("string")).
		Return(func(channelID string) (string, string, string, *mmmodel.AppError) {
			return resolveChannelRoles(defaultChannel(channelID))
		}).Maybe()
	// A backing-channel metadata sync (any UpdateSpace that writes the channel, e.g. a view_access
	// flip) calls Channel.Update, and pluginapi's Update copies the returned channel back over the
	// passed one. Return the same object that was passed so the copy-back is a no-op and the
	// channel's SchemeId survives the sync.
	mockAPI.On("UpdateChannel", mock.AnythingOfType("*model.Channel")).
		Return(func(channel *mmmodel.Channel) (*mmmodel.Channel, *mmmodel.AppError) {
			return channel, nil
		}).Maybe()
}
