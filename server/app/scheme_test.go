// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// TestGetOrCreateSharedScheme_ConcurrentCreateAdoptsWinner covers the race guard: the pooled
// scheme's name is a pure function of the permission set, so two callers racing the same
// permission set collide on create. The loser must adopt the winner's scheme instead of failing
// the caller outright.
func TestGetOrCreateSharedScheme_ConcurrentCreateAdoptsWinner(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}
	permissions := []string{"create_page"}
	name := model.SharedSchemeNameForPermissions(permissions)

	// Shaped exactly as the racing plugin instance would have created it — name, scope, and
	// display name all derived from the permission set — since adoption verifies that shape.
	winner := &mmmodel.Scheme{
		Id:                      mmmodel.NewId(),
		Name:                    name,
		DisplayName:             model.SharedSchemeDisplayNameForPermissions(permissions),
		Scope:                   mmmodel.SchemeScopeChannel,
		DefaultChannelUserRole:  "winner_user_role",
		DefaultChannelAdminRole: "winner_admin_role",
		DefaultChannelGuestRole: "winner_guest_role",
	}

	// The first lookup misses: this caller has not seen the scheme yet.
	mockAPI.On("GetSchemeByName", name).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{Id: "app.scheme.get.app_error", StatusCode: 404}).Once()
	// This caller's own create loses the race.
	mockAPI.On("CreateScheme", mock.AnythingOfType("*model.Scheme")).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{Id: "app.scheme.create.app_error", StatusCode: 500}).Once()
	// The second lookup, taken after the failed create, finds the concurrent winner.
	mockAPI.On("GetSchemeByName", name).Return(winner, nil).Once()
	// The winner's roles are still empty: it created the scheme but its own configure has not
	// landed yet. Adoption accepts that and lets this caller's configure finish them.
	testutil.StubRole(mockAPI, "winner_user_role", nil)
	testutil.StubRole(mockAPI, "winner_admin_role", nil)
	testutil.StubRole(mockAPI, "winner_guest_role", nil)

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	schemeID, roles, err := svc.getOrCreateSharedScheme(permissions)
	require.NoError(t, err, "a create lost to a concurrent winner must not fail the caller")
	require.Equal(t, winner.Id, schemeID)
	require.NotNil(t, roles)
	require.Equal(t, "winner_user_role", roles.UserRoleName)
	require.Equal(t, "winner_admin_role", roles.AdminRoleName)
	require.Equal(t, "winner_guest_role", roles.GuestRoleName)
	mockAPI.AssertExpectations(t)
}

// TestGetOrCreateSharedScheme_AdoptionGuard covers the adoption guard's two outcomes: a
// same-named scheme of another scope must fail the resolution (a channel could never reference
// it), while a channel-scoped scheme with a foreign display name — an operator rename is enough —
// is still adopted, since refusing would permanently brick the permission set.
func TestGetOrCreateSharedScheme_AdoptionGuard(t *testing.T) {
	permissions := []string{"create_page"}
	name := model.SharedSchemeNameForPermissions(permissions)

	t.Run("team scope refused", func(t *testing.T) {
		s, _ := testutil.OpenTestStore(t)
		mockAPI := &plugintest.API{}
		mockAPI.On("GetSchemeByName", name).Return(&mmmodel.Scheme{
			Id:          mmmodel.NewId(),
			Name:        name,
			DisplayName: model.SharedSchemeDisplayNameForPermissions(permissions),
			Scope:       mmmodel.SchemeScopeTeam,
		}, nil).Once()

		client := pluginapi.NewClient(mockAPI, nil)
		svc := New(s, nil, client)

		_, _, err := svc.getOrCreateSharedScheme(permissions)
		require.Error(t, err, "a non-channel scheme squatting the pooled name must not be adopted")
		mockAPI.AssertNotCalled(t, "PatchRole", mock.Anything, mock.Anything)
		mockAPI.AssertExpectations(t)
	})

	t.Run("renamed display name adopted", func(t *testing.T) {
		s, _ := testutil.OpenTestStore(t)
		mockAPI := &plugintest.API{}
		renamed := &mmmodel.Scheme{
			Id:                      mmmodel.NewId(),
			Name:                    name,
			DisplayName:             "Renamed By An Operator",
			Scope:                   mmmodel.SchemeScopeChannel,
			DefaultChannelUserRole:  "renamed_user_role",
			DefaultChannelAdminRole: "renamed_admin_role",
			DefaultChannelGuestRole: "renamed_guest_role",
		}
		mockAPI.On("GetSchemeByName", name).Return(renamed, nil).Once()
		testutil.StubRole(mockAPI, "renamed_user_role", nil)
		testutil.StubRole(mockAPI, "renamed_admin_role", nil)
		testutil.StubRole(mockAPI, "renamed_guest_role", nil)

		client := pluginapi.NewClient(mockAPI, nil)
		svc := New(s, nil, client)

		schemeID, roles, err := svc.getOrCreateSharedScheme(permissions)
		require.NoError(t, err, "a display-name rename must not brick the permission set")
		require.Equal(t, renamed.Id, schemeID)
		require.Equal(t, "renamed_user_role", roles.UserRoleName)
		mockAPI.AssertExpectations(t)
	})

	// The reason adoption is guarded at all: configureSharedScheme rewrites an adopted scheme's
	// roles, and those roles govern every channel referencing that scheme. A scheme whose roles
	// grant SPACE permissions the pooled name does not imply is therefore not ours — the realistic
	// case being a pooled scheme edited in the System Console to widen what its spaces grant — and
	// rewriting it would change authority on channels outside the caller's space.
	t.Run("roles granting unimplied space permissions refused rather than overwritten", func(t *testing.T) {
		s, _ := testutil.OpenTestStore(t)
		mockAPI := &plugintest.API{}
		foreign := &mmmodel.Scheme{
			Id:                      mmmodel.NewId(),
			Name:                    name,
			DisplayName:             model.SharedSchemeDisplayNameForPermissions(permissions),
			Scope:                   mmmodel.SchemeScopeChannel,
			DefaultChannelUserRole:  "foreign_user_role",
			DefaultChannelAdminRole: "foreign_admin_role",
			DefaultChannelGuestRole: "foreign_guest_role",
		}
		mockAPI.On("GetSchemeByName", name).Return(foreign, nil).Once()
		// admin_space on top of the implied set: authority the pooled name does not carry, on a role
		// this caller would otherwise rewrite. Mixed with channel permissions to prove the guard
		// reads past them rather than being tripped by them.
		testutil.StubRole(mockAPI, "foreign_user_role", append(
			[]string{"create_post", "read_channel", mmmodel.PermissionAdminSpace.Id},
			pooledUserRolePermissions(permissions)...))
		testutil.StubRole(mockAPI, "foreign_admin_role", nil)
		testutil.StubRole(mockAPI, "foreign_guest_role", nil)

		client := pluginapi.NewClient(mockAPI, nil)
		svc := New(s, nil, client)

		_, _, err := svc.getOrCreateSharedScheme(permissions)
		require.Error(t, err, "a scheme granting space permissions the pooled name does not imply must not be adopted")
		mockAPI.AssertNotCalled(t, "PatchRole", mock.Anything, mock.Anything)
	})

	// The converse, and the case that made the pool unusable before the comparison was filtered:
	// core does not leave a new channel scheme's roles empty. It seeds the User and Guest roles with
	// the moderated subset of the built-in role, and every role read merges in the non-moderated
	// channel permissions of the higher-scoped role. So a scheme this plugin created and has not yet
	// configured reads back carrying create_post, read_channel and friends — and judging a role by
	// those made every already-pooled set unadoptable, which surfaced as a 500 on the second space
	// asking for it.
	t.Run("channel permissions alone do not block adoption", func(t *testing.T) {
		s, _ := testutil.OpenTestStore(t)
		mockAPI := &plugintest.API{}
		unconfigured := &mmmodel.Scheme{
			Id:                      mmmodel.NewId(),
			Name:                    name,
			DisplayName:             model.SharedSchemeDisplayNameForPermissions(permissions),
			Scope:                   mmmodel.SchemeScopeChannel,
			DefaultChannelUserRole:  "unconfigured_user_role",
			DefaultChannelAdminRole: "unconfigured_admin_role",
			DefaultChannelGuestRole: "unconfigured_guest_role",
		}
		mockAPI.On("GetSchemeByName", name).Return(unconfigured, nil).Once()
		// What core actually leaves behind and then merges in, for all three roles.
		coreBaseline := []string{"create_post", "add_reaction", "use_channel_mentions", "read_channel", "upload_file"}
		testutil.StubRole(mockAPI, "unconfigured_user_role", coreBaseline)
		testutil.StubRole(mockAPI, "unconfigured_admin_role", coreBaseline)
		testutil.StubRole(mockAPI, "unconfigured_guest_role", coreBaseline)

		client := pluginapi.NewClient(mockAPI, nil)
		svc := New(s, nil, client)

		schemeID, roles, err := svc.getOrCreateSharedScheme(permissions)
		require.NoError(t, err, "a scheme carrying only core's own channel permissions must be adoptable")
		require.Equal(t, unconfigured.Id, schemeID)
		require.Equal(t, "unconfigured_user_role", roles.UserRoleName)
	})

	// The other side of the same guard: a scheme already holding exactly what its name implies is
	// the normal shared case. It must be adopted without writing anything, so resolving a pooled
	// scheme costs no role-cache invalidation on every node for every space sharing it.
	t.Run("conforming roles adopted with no write", func(t *testing.T) {
		s, _ := testutil.OpenTestStore(t)
		mockAPI := &plugintest.API{}
		conforming := &mmmodel.Scheme{
			Id:                      mmmodel.NewId(),
			Name:                    name,
			DisplayName:             model.SharedSchemeDisplayNameForPermissions(permissions),
			Scope:                   mmmodel.SchemeScopeChannel,
			DefaultChannelUserRole:  "ok_user_role",
			DefaultChannelAdminRole: "ok_admin_role",
			DefaultChannelGuestRole: "ok_guest_role",
		}
		mockAPI.On("GetSchemeByName", name).Return(conforming, nil).Once()
		testutil.StubRole(mockAPI, "ok_user_role", []string{mmmodel.PermissionReadPage.Id, mmmodel.PermissionCreatePage.Id})
		testutil.StubRole(mockAPI, "ok_admin_role", mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions))
		testutil.StubRole(mockAPI, "ok_guest_role", []string{mmmodel.PermissionReadPage.Id})

		client := pluginapi.NewClient(mockAPI, nil)
		svc := New(s, nil, client)

		schemeID, roles, err := svc.getOrCreateSharedScheme(permissions)
		require.NoError(t, err)
		require.Equal(t, conforming.Id, schemeID)
		require.Equal(t, "ok_user_role", roles.UserRoleName)

		changed, cfgErr := svc.configureSharedScheme(roles, permissions)
		require.NoError(t, cfgErr)
		require.False(t, changed, "an already-conforming scheme must not be rewritten")
		mockAPI.AssertNotCalled(t, "PatchRole", mock.Anything, mock.Anything)
	})
}

// TestConfigureSharedScheme_PartialFailureLeavesPriorWritesInPlace covers the non-atomicity of the
// three sequential role writes: a mid-loop failure leaves the roles patched before it holding their
// new permissions rather than rolling back, and never reaches the roles after it. This is the
// behavior the doc comments on configureSharedScheme and setRolePermissions describe as self-healed
// by every later resolution re-running the same idempotent write, not by any compensating rollback.
func TestConfigureSharedScheme_PartialFailureLeavesPriorWritesInPlace(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}

	userRole := testutil.StubRole(mockAPI, "partial_user_role", nil)
	adminRole := testutil.StubRole(mockAPI, "partial_admin_role", nil)
	guestRole := testutil.StubRole(mockAPI, "partial_guest_role", nil)

	mockAPI.On("PatchRole", adminRole.Id, mock.AnythingOfType("*model.RolePatch")).
		Return(func(roleID string, patch *mmmodel.RolePatch) (*mmmodel.Role, *mmmodel.AppError) {
			adminRole.Permissions = *patch.Permissions
			return adminRole, nil
		})
	mockAPI.On("PatchRole", guestRole.Id, mock.AnythingOfType("*model.RolePatch")).
		Return((*mmmodel.Role)(nil), &mmmodel.AppError{Message: "boom", StatusCode: 500})

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	roles := &schemeRoles{UserRoleName: userRole.Name, AdminRoleName: adminRole.Name, GuestRoleName: guestRole.Name}
	changed, err := svc.configureSharedScheme(roles, []string{"create_page"})
	require.Error(t, err, "the guest role's failed patch must surface")
	require.True(t, changed, "the admin role write that landed before the failure must be reported")
	require.ElementsMatch(t, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions), adminRole.Permissions,
		"the role patched before the mid-loop failure keeps its new permissions rather than rolling back")
	require.Empty(t, userRole.Permissions, "a role ordered after the failed one is never reached")
	mockAPI.AssertNotCalled(t, "PatchRole", userRole.Id, mock.Anything)
}

// TestConfigureSharedScheme_UserRoleWrittenLast pins the write order the no-op recovery shortcut in
// SetSpaceDefaultPermissions depends on: Admin and Guest must land before User, because that
// shortcut's "already configured" projection reads the User role alone
// (spaceDefaultPermissionsFromChannel). Writing User first would let a failure stranding Admin at
// core's broader channel defaults and Guest above read_page still read as fully configured on the
// next resolution — this test fails if that order regresses.
func TestConfigureSharedScheme_UserRoleWrittenLast(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}

	userRole := testutil.StubRole(mockAPI, "order_user_role", nil)
	adminRole := testutil.StubRole(mockAPI, "order_admin_role", nil)
	guestRole := testutil.StubRole(mockAPI, "order_guest_role", nil)

	mockAPI.On("PatchRole", adminRole.Id, mock.AnythingOfType("*model.RolePatch")).
		Return(func(roleID string, patch *mmmodel.RolePatch) (*mmmodel.Role, *mmmodel.AppError) {
			adminRole.Permissions = *patch.Permissions
			return adminRole, nil
		})
	mockAPI.On("PatchRole", guestRole.Id, mock.AnythingOfType("*model.RolePatch")).
		Return(func(roleID string, patch *mmmodel.RolePatch) (*mmmodel.Role, *mmmodel.AppError) {
			guestRole.Permissions = *patch.Permissions
			return guestRole, nil
		})
	mockAPI.On("PatchRole", userRole.Id, mock.AnythingOfType("*model.RolePatch")).
		Return((*mmmodel.Role)(nil), &mmmodel.AppError{Message: "boom", StatusCode: 500})

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	roles := &schemeRoles{UserRoleName: userRole.Name, AdminRoleName: adminRole.Name, GuestRoleName: guestRole.Name}
	changed, err := svc.configureSharedScheme(roles, []string{"create_page"})
	require.Error(t, err, "the user role's failed patch must surface")
	require.True(t, changed, "the admin and guest writes that landed before the failure must be reported")
	require.ElementsMatch(t, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions), adminRole.Permissions,
		"admin must be written before the user-role failure, not stranded at core's broader channel default")
	require.ElementsMatch(t, []string{"read_page"}, guestRole.Permissions,
		"guest must be written before the user-role failure, not stranded above read_page")
	require.Empty(t, userRole.Permissions, "the user role, ordered last, is never reached")
}

// TestSchemeRolesFromChannel_GenericRolesLookupErrorPropagates mirrors
// TestRequireSpaceDraftWrite_LookupFailureIsNotADenial's coverage of getSchemeRolesForChannel's own
// GetChannelOfType generic-error branch, but for the second lookup inside it: a non-404 failure of
// Scheme.GetRolesForChannel must surface as itself, not collapse into the same not-found translation
// its 404 case gets.
func TestSchemeRolesFromChannel_GenericRolesLookupErrorPropagates(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}

	channelID := mmmodel.NewId()
	schemeID := mmmodel.NewId()
	channel := &mmmodel.Channel{Id: channelID, Type: mmmodel.ChannelTypeSpace, SchemeId: &schemeID}
	mockAPI.On("GetSchemeRolesForChannel", channelID).
		Return("", "", "", &mmmodel.AppError{Message: "boom", StatusCode: 500})

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	_, err := svc.schemeRolesFromChannel(channelID, channel)
	require.Error(t, err)
	require.False(t, store.IsErrNotFound(err), "a generic lookup failure must not be reported as not-found")
}

// TestSchemeRolesFromChannel_MissingSchemeVariants covers both shapes of "no scheme" the code
// treats as distinct cases: a nil SchemeId and a non-nil SchemeId pointing at an empty string.
// Either must resolve to not-found rather than falling through to a scheme lookup.
func TestSchemeRolesFromChannel_MissingSchemeVariants(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	svc := New(s, nil, nil)

	t.Run("nil SchemeId", func(t *testing.T) {
		channel := &mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace}
		_, err := svc.schemeRolesFromChannel(channel.Id, channel)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("empty string SchemeId", func(t *testing.T) {
		empty := ""
		channel := &mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace, SchemeId: &empty}
		_, err := svc.schemeRolesFromChannel(channel.Id, channel)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err))
	})
}
