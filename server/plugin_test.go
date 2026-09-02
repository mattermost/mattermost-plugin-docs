// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeHTTP(t *testing.T) {
	assert := assert.New(t)
	plugin := Plugin{}
	plugin.router = plugin.initRouter()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)

	plugin.ServeHTTP(nil, w, r)

	result := w.Result()
	assert.NotNil(result)
	defer func() { _ = result.Body.Close() }()
	assert.Equal(http.StatusNotFound, result.StatusCode)
}

// TestImportWorkerDelay pins the worker's pacing. RunImportWork reports worked=true whenever it selected a job,
// error or not, so a loop that drained on "worked" would retry a persistently failing job as fast as the
// database could answer it.
func TestImportWorkerDelay(t *testing.T) {
	t.Run("found work and succeeded: drain immediately", func(t *testing.T) {
		delay, drain := importWorkerDelay(true, nil)
		require.True(t, drain, "a healthy pass that did work should go straight to the next unit")
		require.Zero(t, delay)
	})

	t.Run("no work: idle briefly", func(t *testing.T) {
		delay, drain := importWorkerDelay(false, nil)
		require.False(t, drain)
		require.Equal(t, importWorkerIdleInterval, delay)
	})

	t.Run("failed while holding work: back off, do not drain", func(t *testing.T) {
		delay, drain := importWorkerDelay(true, errors.New("backend unavailable"))
		require.False(t, drain, "a failed pass must never drain, however much work it reported")
		require.Equal(t, importWorkerErrorBackoff, delay)
		require.Greater(t, delay, importWorkerIdleInterval,
			"a failing worker should wait longer than an idle one, not the same")
	})

	t.Run("failed with no work: back off", func(t *testing.T) {
		delay, drain := importWorkerDelay(false, errors.New("backend unavailable"))
		require.False(t, drain)
		require.Equal(t, importWorkerErrorBackoff, delay)
	})
}
