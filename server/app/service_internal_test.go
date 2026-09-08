// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// TestStoreAppError_LockTimeout pins storeAppError's translation of a WithSpaceMembershipLock
// acquisition timeout: distinct from the default CAS/unique-constraint conflict below it, it keeps
// its own message key and status code rather than collapsing into app.store.conflict.app_error.
func TestStoreAppError_LockTimeout(t *testing.T) {
	err := &store.ErrConflict{Resource: "Space membership lock space_id=test", Reason: store.ReasonLockTimeout}

	appErr := storeAppError("TestStoreAppError_LockTimeout", err)

	require.NotNil(t, appErr)
	require.Equal(t, "app.space.lock_timeout.app_error", appErr.Id)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
}

// TestStoreAppError_OrdinaryConflict is the negative half of the pair above: an ErrConflict with no
// ReasonLockTimeout maps to the shared conflict key instead.
func TestStoreAppError_OrdinaryConflict(t *testing.T) {
	err := &store.ErrConflict{Resource: "Space id=test"}

	appErr := storeAppError("TestStoreAppError_OrdinaryConflict", err)

	require.NotNil(t, appErr)
	require.Equal(t, "app.store.conflict.app_error", appErr.Id)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
}
