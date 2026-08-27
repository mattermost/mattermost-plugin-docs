// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	docsmodel "github.com/mattermost/mattermost-plugin-docs/server/model"
)

func TestConfigurationNewSpaceDefaultPermissions(t *testing.T) {
	tests := map[string]string{
		newSpaceDefaultPresetContribute: mmmodel.SchemeNameSpaceContribute,
		newSpaceDefaultPresetComment:    mmmodel.SchemeNameSpaceComment,
		newSpaceDefaultPresetReadOnly:   mmmodel.SchemeNameSpaceReadOnly,
	}
	for preset, schemeName := range tests {
		t.Run(preset, func(t *testing.T) {
			expected, ok := docsmodel.DefaultPermissionsForSchemeName(schemeName)
			require.True(t, ok)
			configured := (&configuration{NewSpaceDefaultPreset: preset}).newSpaceDefaultPermissions()
			require.Equal(t, expected, configured)
		})
	}
}

func TestOnConfigurationChange(t *testing.T) {
	t.Run("empty setting uses contribute", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("GetConfig").Return(&mmmodel.Config{}).Maybe()
		mockAPI.On("LoadPluginConfiguration", mock.Anything).Return(nil)
		plugin := &Plugin{}
		plugin.API = mockAPI

		require.NoError(t, plugin.OnConfigurationChange())
		require.Equal(t, newSpaceDefaultPresetContribute, plugin.getConfiguration().NewSpaceDefaultPreset)
		mockAPI.AssertExpectations(t)
	})

	t.Run("invalid setting is rejected without replacing live configuration", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("GetConfig").Return(&mmmodel.Config{}).Maybe()
		mockAPI.On("LoadPluginConfiguration", mock.Anything).
			Run(func(arguments mock.Arguments) {
				arguments.Get(0).(*configuration).NewSpaceDefaultPreset = "invalid"
			}).
			Return(nil)
		plugin := &Plugin{configuration: &configuration{NewSpaceDefaultPreset: newSpaceDefaultPresetComment}}
		plugin.API = mockAPI

		require.ErrorContains(t, plugin.OnConfigurationChange(), "invalid plugin configuration")
		require.Equal(t, newSpaceDefaultPresetComment, plugin.getConfiguration().NewSpaceDefaultPreset)
		mockAPI.AssertExpectations(t)
	})
}
