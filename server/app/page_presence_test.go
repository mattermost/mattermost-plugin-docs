// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestActiveEditorTimeoutMsValue pins the active-editor window to its contract value. The other
// presence tests assert response fields equal this const (verifying wiring), so this is the one
// place that guards the value itself.
func TestActiveEditorTimeoutMsValue(t *testing.T) {
	require.Equal(t, int64(5*60*1000), ActiveEditorTimeoutMs)
}

// TestSweepPresenceBroadcastTimesEvictsStaleEntriesOncePerWindow verifies sweepPresenceBroadcastTimes's
// two behaviors: it evicts entries older than ActiveEditorTimeoutMs while leaving fresh entries in
// place, and it runs at most once per presenceBroadcastSweepIntervalMs — a second call with the same
// `now` must be a no-op even if a new stale entry was added in between.
func TestSweepPresenceBroadcastTimesEvictsStaleEntriesOncePerWindow(t *testing.T) {
	svc := &Service{}

	now := int64(1_000_000_000)
	staleKey := "stale-page:stale-user"
	freshKey := "fresh-page:fresh-user"
	svc.presenceBroadcastTimes.Store(staleKey, now-2*ActiveEditorTimeoutMs)
	svc.presenceBroadcastTimes.Store(freshKey, now)

	// Open the gate: make the sweep consider itself overdue.
	svc.lastPresenceSweepAt.Store(now - presenceBroadcastSweepIntervalMs)

	svc.sweepPresenceBroadcastTimes(now)

	_, staleStillPresent := svc.presenceBroadcastTimes.Load(staleKey)
	require.False(t, staleStillPresent, "a stale entry must be evicted by the sweep")
	_, freshStillPresent := svc.presenceBroadcastTimes.Load(freshKey)
	require.True(t, freshStillPresent, "a fresh entry must survive the sweep")

	// Re-seed a stale entry and call again with the same `now`: the gate must be closed (at most one
	// sweep per ActiveEditorTimeoutMs), so this entry must NOT be evicted.
	svc.presenceBroadcastTimes.Store(staleKey, now-2*ActiveEditorTimeoutMs)
	svc.sweepPresenceBroadcastTimes(now)

	_, stillThere := svc.presenceBroadcastTimes.Load(staleKey)
	require.True(t, stillThere, "a second call within the sweep interval must be a no-op")
}
