// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
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
// across tests. Guarded because parallel tests in this package register into it concurrently.
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

// registerSchemeRoles records the generated role names schemeID resolves to, so a channel later
// repointed at it resolves that scheme's roles through GetSchemeRolesForChannel. The three presets
// are registered already; a test creating a custom scheme registers its own.
func registerSchemeRoles(schemeID, guestRole, userRole, adminRole string) {
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

// PresetUserRoleName returns the generated user-role name StubPresetSchemes stubbed for the named
// preset — exported for callers pinning the exact role argument DefaultRolesGrantPermission passes
// to RolesGrantPermission, rather than accepting any role via mock.Anything.
func PresetUserRoleName(name string) string {
	return presetSchemeFixtures[name].userRole
}

// StubPresetSchemes registers .Maybe() stubs on mockAPI resolving each of the three preset space
// schemes by name, plus their three generated roles by name — the shape
// getSchemeByName/getRolePermissionsByName resolve through the plugin API in production. Call
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
		stubRole(mockAPI, fx.userRole, mmmodel.PermissionIDs(fx.userPerms))
		stubRole(mockAPI, fx.adminRole, mmmodel.PermissionIDs(fx.adminPerms))
		stubRole(mockAPI, fx.guestRole, mmmodel.PermissionIDs(fx.guestPerms))
	}
}

// stubPluginID stands in for the calling plugin's manifest id. Core derives the scheme namespace
// from it at the RPC boundary; the mock intercepts above that, so any stable value keys the pool
// the same way a real plugin id would.
const stubPluginID = "com.mattermost.plugin-docs"

func isPluginChannelSchemeRoleName(name string) bool {
	return strings.HasPrefix(name, mmmodel.PluginChannelSchemeNamePrefix)
}

// StubSchemePool simulates core creating plugin channel schemes, with state: one set of three role
// permission sets resolves to one scheme however many times it is asked for, and that scheme's
// generated roles read back with exactly the permissions supplied at creation.
//
// Both halves matter to what the tests assert. The first is the property pooling exists for — two
// spaces configured alike share a scheme rather than creating two identical ones. The second is
// that a scheme arrives complete: there is no configure step after creation, so a role that read
// back empty here would be a stub artefact rather than a state production can reach.
//
// The generated role names are derived from the scheme name so a standing stub can answer them;
// registering new expectations from inside a running mock call would race the mock's own lock.
func StubSchemePool(mockAPI *plugintest.API) *SchemePoolRecorder {
	var mu sync.Mutex
	byName := map[string]*mmmodel.Scheme{}
	rolesByName := map[string]*mmmodel.Role{}
	recorder := &SchemePoolRecorder{}

	mockAPI.On("GetOrCreatePluginChannelScheme", mock.Anything, mock.Anything, mock.Anything).
		Return(func(user, admin, guest []string) (*mmmodel.Scheme, *mmmodel.AppError) {
			mu.Lock()
			defer mu.Unlock()

			name := mmmodel.PluginChannelSchemeName(stubPluginID, user, admin, guest)
			if scheme, ok := byName[name]; ok {
				recorder.record(user, admin, guest, scheme.Id)
				return scheme, nil
			}

			scheme := &mmmodel.Scheme{
				Id:                      mmmodel.NewId(),
				Name:                    name,
				DisplayName:             name,
				Scope:                   mmmodel.SchemeScopeChannel,
				DefaultChannelUserRole:  name + "_user",
				DefaultChannelAdminRole: name + "_admin",
				DefaultChannelGuestRole: name + "_guest",
			}
			byName[name] = scheme

			for roleName, permissions := range map[string][]string{
				scheme.DefaultChannelUserRole:  user,
				scheme.DefaultChannelAdminRole: admin,
				scheme.DefaultChannelGuestRole: guest,
			} {
				role := &mmmodel.Role{Id: mmmodel.NewId(), Name: roleName, Permissions: slices.Clone(permissions)}
				rolesByName[roleName] = role
			}
			registerSchemeRoles(scheme.Id, scheme.DefaultChannelGuestRole, scheme.DefaultChannelUserRole, scheme.DefaultChannelAdminRole)
			recorder.record(user, admin, guest, scheme.Id)
			return scheme, nil
		}).Maybe()

	mockAPI.On("GetRoleByName", mock.MatchedBy(isPluginChannelSchemeRoleName)).
		Return(func(roleName string) (*mmmodel.Role, *mmmodel.AppError) {
			mu.Lock()
			defer mu.Unlock()
			if role, ok := rolesByName[roleName]; ok {
				return role, nil
			}
			// A role of a scheme this pool never created. Answered as not-found rather than as an
			// empty role, so a test wiring the wrong scheme fails on the lookup instead of on a
			// silently permission-less space.
			return nil, &mmmodel.AppError{Id: "app.role.get_by_name.app_error", StatusCode: http.StatusNotFound}
		}).Maybe()

	return recorder
}

// SchemePoolRequest is one resolution the pool answered: the three role permission sets the plugin
// asked for, and the scheme they resolved to.
type SchemePoolRequest struct {
	User     []string
	Admin    []string
	Guest    []string
	SchemeID string
}

// SchemePoolRecorder records what the pool was asked for. A test asserting on the sets the plugin
// sends reads them from here rather than registering a second expectation for the same call, which
// would compete with the stub's own for the match.
type SchemePoolRecorder struct {
	mu       sync.Mutex
	requests []SchemePoolRequest
}

func (r *SchemePoolRecorder) record(user, admin, guest []string, schemeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, SchemePoolRequest{
		User: slices.Clone(user), Admin: slices.Clone(admin), Guest: slices.Clone(guest), SchemeID: schemeID,
	})
}

// Last returns the most recent resolution, reporting false when the pool was never asked.
func (r *SchemePoolRecorder) Last() (SchemePoolRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return SchemePoolRequest{}, false
	}
	return r.requests[len(r.requests)-1], true
}

// Count reports how many resolutions the pool answered, so a test can pin that a repeat
// configuration reused a scheme rather than creating another one.
func (r *SchemePoolRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// stubRole registers a GetRoleByName stub returning one shared *Role carrying permissions.
// Returns the shared Role.
func stubRole(mockAPI *plugintest.API, roleName string, permissions []string) *mmmodel.Role {
	role := &mmmodel.Role{Id: mmmodel.NewId(), Name: roleName, Permissions: permissions}
	mockAPI.On("GetRoleByName", roleName).Return(role, nil).Maybe()
	return role
}

// MustSeedChannelScheme registers mock stubs so channelID resolves to the named seeded space scheme
// preset: GetChannelOfType returns a channel pointing at the preset's id, and
// GetSchemeRolesForChannel resolves whichever scheme that channel currently points at — so a test
// that repoints the channel sees the new scheme's roles without re-stubbing, the way core resolves
// them. Production sets this up through the real backing channel's SchemeId when
// CreateSpace/SetSpaceDefaultPermissions points a space at a scheme.
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
	stubChannelScheme(mockAPI, channelID, channel)
	return channel
}

// stubChannelScheme wires the three calls that resolve one channel's scheme: the channel read, the
// scheme-role resolution that follows the channel's current SchemeId, and the metadata-sync write.
//
// UpdateChannel must return the SAME shared object GetChannelOfType hands out, because pluginapi's
// Channel.Update copies the returned channel back over the passed one — returning a fresh channel
// would wipe SchemeId and 404 the next scheme-resolving read.
func stubChannelScheme(mockAPI *plugintest.API, channelID string, channel *mmmodel.Channel) {
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
