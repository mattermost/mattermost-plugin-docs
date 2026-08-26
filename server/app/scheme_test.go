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

func TestSchemeAPIUnsupportedIsNormalized(t *testing.T) {
	t.Run("channel scheme lookup", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		channelID := mmmodel.NewId()
		mockAPI.On("GetSchemeForChannel", channelID).Return(nil, nil, nil, nil, nil)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		_, err := svc.getSchemeRolesForChannel(channelID)
		require.ErrorIs(t, err, errUnsupportedSchemeAPI)
	})

	t.Run("scheme lookup by name", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("GetSchemeByName", "missing-api").Return(nil, nil)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		_, err := svc.getSchemeByName("missing-api")
		require.ErrorIs(t, err, errUnsupportedSchemeAPI)
	})

	t.Run("pooled scheme lookup", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("GetOrCreatePluginChannelScheme", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		svc := &Service{client: pluginapi.NewClient(mockAPI, nil)}

		_, _, err := svc.resolveSpaceScheme([]string{mmmodel.PermissionCreatePage.Id})
		require.ErrorIs(t, err, errUnsupportedSchemeAPI)
	})
}

func TestGetSchemeRolesForChannel_GenericLookupErrorPropagates(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}

	channelID := mmmodel.NewId()
	mockAPI.On("GetSchemeForChannel", channelID).
		Return(nil, nil, nil, nil, &mmmodel.AppError{Message: "boom", StatusCode: 500})

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	_, err := svc.getSchemeRolesForChannel(channelID)
	require.Error(t, err)
	require.False(t, store.IsErrNotFound(err), "a generic lookup failure must not be reported as not-found")
}

func TestGetSchemeRolesForChannel_IncompleteAggregateIsUnsupported(t *testing.T) {
	scheme := &mmmodel.Scheme{Id: mmmodel.NewId()}
	guest := &mmmodel.Role{Name: "guest-role"}
	user := &mmmodel.Role{Name: "user-role"}
	admin := &mmmodel.Role{Name: "admin-role"}
	tests := map[string][4]any{
		"scheme": {nil, guest, user, admin},
		"guest":  {scheme, nil, user, admin},
		"user":   {scheme, guest, nil, admin},
		"admin":  {scheme, guest, user, nil},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			s, _ := testutil.OpenTestStore(t)
			mockAPI := &plugintest.API{}
			channelID := mmmodel.NewId()
			mockAPI.On("GetSchemeForChannel", channelID).
				Return(values[0], values[1], values[2], values[3], nil)

			svc := New(s, nil, pluginapi.NewClient(mockAPI, nil))
			_, err := svc.getSchemeRolesForChannel(channelID)

			require.ErrorIs(t, err, errUnsupportedSchemeAPI)
		})
	}
}

func TestGetSchemeRolesForChannel_MissingDirectSchemeIsNotFound(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}
	channelID := mmmodel.NewId()
	mockAPI.On("GetSchemeForChannel", channelID).
		Return(nil, nil, nil, nil, &mmmodel.AppError{StatusCode: 404})
	svc := New(s, nil, pluginapi.NewClient(mockAPI, nil))

	_, err := svc.getSchemeRolesForChannel(channelID)
	require.Error(t, err)
	require.True(t, store.IsErrNotFound(err))
}
