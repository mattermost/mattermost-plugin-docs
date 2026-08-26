// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// TestResolveSpaceScheme_PoolsByPermissionSet covers what pooling is for: two spaces configured
// the same way must land on one scheme, and a space configured differently must not. Core owns the
// dedupe now — the plugin asks for a scheme expressing three role permission sets and gets the same
// one back every time — so this asserts the plugin passes a set that varies only with the requested
// permissions, which is what makes core's answer stable.
func TestResolveSpaceScheme_PoolsByPermissionSet(t *testing.T) {
	nonPreset := []string{mmmodel.PermissionCreatePage.Id, mmmodel.PermissionDeletePage.Id}

	t.Run("the same set resolves to the same scheme", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		testutil.StubPresetSchemes(mockAPI)
		testutil.StubSchemePool(mockAPI)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		first, _, err := svc.resolveSpaceScheme(nonPreset)
		require.NoError(t, err)
		second, _, err := svc.resolveSpaceScheme(nonPreset)
		require.NoError(t, err)
		require.Equal(t, first, second, "two spaces configured alike must share one scheme")

		// Order must not matter either: the same set spelled differently is the same configuration.
		reordered, _, err := svc.resolveSpaceScheme([]string{mmmodel.PermissionDeletePage.Id, mmmodel.PermissionCreatePage.Id})
		require.NoError(t, err)
		require.Equal(t, first, reordered)
	})

	t.Run("a different set resolves to a different scheme", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		testutil.StubPresetSchemes(mockAPI)
		testutil.StubSchemePool(mockAPI)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		first, _, err := svc.resolveSpaceScheme(nonPreset)
		require.NoError(t, err)
		other, _, err := svc.resolveSpaceScheme([]string{mmmodel.PermissionCreatePage.Id})
		require.NoError(t, err)
		require.NotEqual(t, first, other)
	})

	t.Run("carries the space tier model to core", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		testutil.StubPresetSchemes(mockAPI)
		pool := testutil.StubSchemePool(mockAPI)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		_, _, err := svc.resolveSpaceScheme(nonPreset)
		require.NoError(t, err)
		got, ok := pool.Last()
		require.True(t, ok)
		user, admin, guest := got.User, got.Admin, got.Guest

		// The plugin owns these three sets now; core validates only that they are channel-scoped.
		require.Contains(t, user, mmmodel.PermissionReadPage.Id, "every member reads")
		require.Contains(t, user, mmmodel.PermissionCreatePage.Id)
		require.Contains(t, user, mmmodel.PermissionDeletePage.Id)
		require.Equal(t, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions), admin)
		require.Equal(t, []string{mmmodel.PermissionReadPage.Id}, guest, "the guest tier reads and nothing more")
	})

	t.Run("a preset set resolves to the seeded preset instead of creating a plugin scheme", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		testutil.StubPresetSchemes(mockAPI)
		testutil.StubSchemePool(mockAPI)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		commentPreset, ok := model.DefaultPermissionsForSchemeName(mmmodel.SchemeNameSpaceComment)
		require.True(t, ok)
		schemeID, _, err := svc.resolveSpaceScheme(commentPreset)
		require.NoError(t, err)
		require.Equal(t, testutil.PresetSchemeID(mmmodel.SchemeNameSpaceComment), schemeID)
	})
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

// TestSchemeRolesFromChannel_EmptyRoleNamesAreUnsupported pins the generated-RPC zero-value
// answer: a channel with a scheme must resolve all three generated roles, and an absent plugin API
// reports three strings without an error rather than a conventional not-found response.
func TestSchemeRolesFromChannel_EmptyRoleNamesAreUnsupported(t *testing.T) {
	tests := map[string][3]string{
		"guest": {"", "user-role", "admin-role"},
		"user":  {"guest-role", "", "admin-role"},
		"admin": {"guest-role", "user-role", ""},
	}

	for name, roles := range tests {
		t.Run(name, func(t *testing.T) {
			s, _ := testutil.OpenTestStore(t)
			mockAPI := &plugintest.API{}
			channelID := mmmodel.NewId()
			schemeID := mmmodel.NewId()
			channel := &mmmodel.Channel{Id: channelID, Type: mmmodel.ChannelTypeSpace, SchemeId: &schemeID}
			mockAPI.On("GetSchemeRolesForChannel", channelID).
				Return(roles[0], roles[1], roles[2], nil)

			svc := New(s, nil, pluginapi.NewClient(mockAPI, nil))
			_, err := svc.schemeRolesFromChannel(channelID, channel)

			require.ErrorIs(t, err, errUnsupportedSchemeAPI)
		})
	}
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
