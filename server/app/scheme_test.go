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
// scheme's name is a pure function of the capability set, so two callers racing the same
// capability set collide on create. The loser must adopt the winner's scheme instead of failing
// the caller outright.
func TestGetOrCreateSharedScheme_ConcurrentCreateAdoptsWinner(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}
	capabilities := []string{"create_page"}
	name := model.SharedSchemeNameForCapabilities(capabilities)

	winner := &mmmodel.Scheme{
		Id:                      mmmodel.NewId(),
		Name:                    name,
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

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	schemeID, roles, err := svc.getOrCreateSharedScheme(capabilities)
	require.NoError(t, err, "a create lost to a concurrent winner must not fail the caller")
	require.Equal(t, winner.Id, schemeID)
	require.NotNil(t, roles)
	require.Equal(t, "winner_user_role", roles.UserRoleName)
	require.Equal(t, "winner_admin_role", roles.AdminRoleName)
	require.Equal(t, "winner_guest_role", roles.GuestRoleName)
	mockAPI.AssertExpectations(t)
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
// SetSpaceDefaultCapabilities depends on: Admin and Guest must land before User, because that
// shortcut's "already configured" projection reads the User role alone
// (spaceDefaultCapabilitiesFromChannel). Writing User first would let a failure stranding Admin at
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
