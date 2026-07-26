// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// ActiveEditorTimeoutMs is the window within which a draft autosave keeps a user counted as an
// active editor. Presence is derived from the shared DOCS_Draft table: the editor's autosave is
// the heartbeat, so an editor with a draft updated inside this window is "active". Because the
// list comes from the master DB, it is correct across an HA cluster.
const ActiveEditorTimeoutMs int64 = 5 * 60 * 1000

// presenceBroadcastMinIntervalMs is the minimum time between autosave-triggered presence broadcasts
// for the same page. Delete and publish paths always broadcast regardless of this interval.
const presenceBroadcastMinIntervalMs int64 = 30 * 1000

// presenceBroadcastSweepIntervalMs is the minimum time between sweeps of the broadcast rate-limit
// map. Sweeping is opportunistic — it runs on the first autosave after the interval has elapsed — so
// an autosave performs a full scan of the map at most once per interval.
const presenceBroadcastSweepIntervalMs int64 = 5 * 60 * 1000

func activeEditorSince() int64 {
	return mmmodel.GetMillis() - ActiveEditorTimeoutMs
}

func presenceBroadcastKey(pageID, userID string) string {
	return pageID + ":" + userID
}

// clearPresenceThrottle removes the (pageID, userID) broadcast rate-limit entry, so the next
// broadcast for that editor is not suppressed and a later session starts unthrottled. Called
// whenever a draft session ends.
func (s *Service) clearPresenceThrottle(pageID, userID string) {
	s.presenceBroadcastTimes.Delete(presenceBroadcastKey(pageID, userID))
}

// sweepPresenceBroadcastTimes removes stale entries from the broadcast rate-limit map to bound its
// size. An entry is normally removed when its session ends (discard or publish); this sweep is the
// fallback for sessions abandoned without either.
//
// It removes entries older than the active-editor window (ActiveEditorTimeoutMs). Removing such an
// entry cannot change behavior: the map only suppresses a broadcast within
// presenceBroadcastMinIntervalMs of the stored time, and an entry this old is already far past that
// window, so the next autosave broadcasts whether or not the entry is still present. CompareAndDelete
// removes only an entry whose value is unchanged, so one a concurrent autosave just refreshed is
// left in place.
func (s *Service) sweepPresenceBroadcastTimes(now int64) {
	last := s.lastPresenceSweepAt.Load()
	if now-last < presenceBroadcastSweepIntervalMs {
		return
	}
	if !s.lastPresenceSweepAt.CompareAndSwap(last, now) {
		return
	}

	s.presenceBroadcastTimes.Range(func(key, value any) bool {
		if ts, ok := value.(int64); ok && now-ts >= ActiveEditorTimeoutMs {
			s.presenceBroadcastTimes.CompareAndDelete(key, value)
		}
		return true
	})
}

// getActiveEditors returns the user IDs currently editing pageID in spaceID — those with a draft
// updated within the active-editor window. The bool is true on success and false on a store
// failure; callers must skip the broadcast on failure to avoid publishing a spurious empty snapshot
// that would wrongly clear a valid presence indicator. Use this only where a best-effort answer is
// acceptable, not to back a REST read that must surface a store failure.
func (s *Service) getActiveEditors(pageID, spaceID string) ([]string, bool) {
	editors, err := s.store.GetPageActiveEditors(pageID, spaceID, activeEditorSince())
	if err != nil {
		s.log.Warn("failed to query active editors; skipping broadcast",
			"page_id", pageID, "err", err)
		return nil, false
	}
	return editors, true
}

// publishSelfPresence sends a presence snapshot to userID only. Used when the page is not yet
// published (no channel to broadcast to), so only the author's own UI learns of the session:
// editors is the author's own ID while the session is active, and empty when it ends.
func (s *Service) publishSelfPresence(userID, pageID, spaceID string, editors []string) {
	s.publishToUser(wsEventPagePresenceUpdated, map[string]any{
		"page_id":           pageID,
		"space_id":          spaceID,
		"active_editors":    editors,
		"snapshot_at":       mmmodel.GetMillis(),
		"active_timeout_ms": ActiveEditorTimeoutMs,
	}, userID)
}

// broadcastPagePresence fans a page_presence_updated event out to the space audience on channelID
// (the space's backing channel), carrying the current active-editor set, snapshot_at, and active_timeout_ms.
// Best-effort: failures are logged, never surfaced. The return value reports whether a snapshot was
// actually published, so a throttling caller can release its claimed slot on failure; a nil client
// (store-only unit tests) is a deliberate no-op, not a failure.
//
// Broadcasts fire only on user actions (autosave, discard, publish), never periodically, so a client
// that receives no newer snapshot cannot distinguish a still-active editor from one whose session
// ended abnormally. active_timeout_ms lets it expire the snapshot's editors on its own once
// snapshot_at + active_timeout_ms has passed.
func (s *Service) broadcastPagePresence(pageID, spaceID, channelID string) bool {
	if s.client == nil {
		return true
	}
	// Stamp snapshot_at before the editors query so it marks when the snapshot was taken, not when the
	// broadcast finished assembling — clients use it to discard out-of-order snapshots.
	snapshotAt := mmmodel.GetMillis()
	editors, ok := s.getActiveEditors(pageID, spaceID)
	if !ok {
		return false
	}
	s.publishToChannels(wsEventPagePresenceUpdated, map[string]any{
		"page_id":           pageID,
		"space_id":          spaceID,
		"active_editors":    editors,
		"snapshot_at":       snapshotAt,
		"active_timeout_ms": ActiveEditorTimeoutMs,
	}, channelID)
	return true
}

// maybeBroadcastDraftPresence performs the rate-limited presence broadcast that follows a
// successful autosave. The rate-limit bucket is keyed per (page, user) so each editor gets an
// independent limit — one editor's broadcast can't rate-limit another editor on the same page —
// and it covers both audiences below, so autosave cadence cannot flood either.
//
// The claimed slot is released again when the channel broadcast fails (a store error while
// assembling the snapshot), so the next autosave retries immediately instead of waiting out the
// full throttle window on an attempt that published nothing.
func (s *Service) maybeBroadcastDraftPresence(pageWasLive bool, pageID, userID, spaceID, channelID string) {
	presenceKey := presenceBroadcastKey(pageID, userID)
	now := mmmodel.GetMillis()
	s.sweepPresenceBroadcastTimes(now)
	existing, loaded := s.presenceBroadcastTimes.LoadOrStore(presenceKey, now)
	if loaded {
		lastTime, ok := existing.(int64)
		if !ok || now-lastTime < presenceBroadcastMinIntervalMs {
			return
		}
		if !s.presenceBroadcastTimes.CompareAndSwap(presenceKey, existing, now) {
			return
		}
	}
	// New-page drafts (no published page row yet) must not broadcast presence to the space channel:
	// that would expose the reserved page ID and the author's identity to all space members before
	// the page exists. Send the event only to the author so their own UI can track the session.
	if !pageWasLive {
		s.publishSelfPresence(userID, pageID, spaceID, []string{userID})
		return
	}
	if !s.broadcastPagePresence(pageID, spaceID, channelID) {
		s.presenceBroadcastTimes.CompareAndDelete(presenceKey, now)
	}
}

// endDraftPresenceSession clears the (pageID, userID) rate-limit entry — so the announcement below
// is never suppressed and a later session starts unthrottled — and announces the end of a draft
// session: channel-wide when the page is live, to the author alone otherwise (an unpublished
// new-page draft's session was never visible to the channel, so it ends the same way it was
// announced, with an empty editor set).
func (s *Service) endDraftPresenceSession(pageWasLive bool, pageID, userID, spaceID, channelID string) {
	s.clearPresenceThrottle(pageID, userID)
	if !pageWasLive {
		s.publishSelfPresence(userID, pageID, spaceID, []string{})
		return
	}
	s.broadcastPagePresence(pageID, spaceID, channelID)
}

// PageActiveEditors is the editor-presence snapshot returned by the REST active-editors endpoint. Its
// fields mirror the page_presence_updated WebSocket payload (active_editors, snapshot_at, active_timeout_ms)
// so a client sees the same presence contract whether it resyncs over REST or receives a live event.
type PageActiveEditors struct {
	ActiveEditors   []string `json:"active_editors"`
	SnapshotAt      int64    `json:"snapshot_at"`
	ActiveTimeoutMs int64    `json:"active_timeout_ms"`
}

// GetPageActiveEditors returns the editor-presence snapshot for the given page in the given space,
// after confirming the page exists in that space. Returns 404 if the page is unknown or belongs to
// another space; store failures are propagated (unlike the best-effort getActiveEditors, this backs
// a REST read that must not report "nobody editing" when the query actually failed).
func (s *Service) GetPageActiveEditors(pageID, spaceID string) (*PageActiveEditors, *mmmodel.AppError) {
	if !mmmodel.IsValidId(pageID) {
		return nil, mmmodel.NewAppError("GetPageActiveEditors", "app.page.presence.invalid_page_id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(spaceID) {
		return nil, mmmodel.NewAppError("GetPageActiveEditors", "app.page.presence.invalid_space_id.app_error", nil, "", http.StatusBadRequest)
	}
	exists, err := s.store.PageExistsInSpace(pageID, spaceID)
	if err != nil {
		return nil, storeAppError("GetPageActiveEditors", err)
	}
	if !exists {
		return nil, mmmodel.NewAppError("GetPageActiveEditors", "app.page.not_found.app_error", nil, "", http.StatusNotFound)
	}
	// Stamp snapshot_at before the query so it marks when the snapshot was taken, matching the WS event.
	snapshotAt := mmmodel.GetMillis()
	editors, storeErr := s.store.GetPageActiveEditors(pageID, spaceID, snapshotAt-ActiveEditorTimeoutMs)
	if storeErr != nil {
		return nil, storeAppError("GetPageActiveEditors", storeErr)
	}
	return &PageActiveEditors{
		ActiveEditors:   editors,
		SnapshotAt:      snapshotAt,
		ActiveTimeoutMs: ActiveEditorTimeoutMs,
	}, nil
}
