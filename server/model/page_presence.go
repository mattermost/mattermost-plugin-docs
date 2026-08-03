// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

// PageActiveEditors is the editor-presence snapshot for a page: the users counted as editing it,
// when the snapshot was taken, and how long an entry stays current without a further save. A client
// that receives no newer snapshot expires the list itself once ActiveTimeoutMs has elapsed since
// SnapshotAt.
//
// The REST active-editors endpoint and the page_presence_updated WebSocket payload carry these same
// fields, so a client reads one presence contract whether it resyncs or receives a live event.
type PageActiveEditors struct {
	ActiveEditors   []string `json:"active_editors"`
	SnapshotAt      int64    `json:"snapshot_at"`
	ActiveTimeoutMs int64    `json:"active_timeout_ms"`
}
