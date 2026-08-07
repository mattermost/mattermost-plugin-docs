// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"sync"

	"github.com/stretchr/testify/mock"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
)

// StubKVStore registers an in-memory stand-in for the plugin KV store behind KVGet and
// KVSetWithOptions, so app-layer code that persists a small marker (e.g. the auto-join provenance
// flag) works against a mockAPI the way it works against core. Keyed by mockAPI so two tests never
// see each other's keys.
func StubKVStore(mockAPI *plugintest.API) {
	var mu sync.Mutex
	byKey := map[string][]byte{}

	mockAPI.On("KVGet", mock.AnythingOfType("string")).
		Return(func(key string) ([]byte, *mmmodel.AppError) {
			mu.Lock()
			defer mu.Unlock()
			return byKey[key], nil
		}).Maybe()

	mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("model.PluginKVSetOptions")).
		Return(func(key string, value []byte, _ mmmodel.PluginKVSetOptions) (bool, *mmmodel.AppError) {
			mu.Lock()
			defer mu.Unlock()
			if len(value) == 0 {
				delete(byKey, key)
			} else {
				byKey[key] = value
			}
			return true, nil
		}).Maybe()
}
