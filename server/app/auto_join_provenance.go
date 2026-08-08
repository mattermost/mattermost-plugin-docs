// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

// Auto-join provenance markers record which of a space's members were added by
// AutoJoinIfDefaultGranted rather than invited/added deliberately, so a membership review (e.g.
// after an open->private flip) and UndoAutoJoin can tell the two apart. They live in the plugin's
// own DOCS_SpaceAutoJoin table, read and written on the master DB handle: UndoAutoJoin deletes a
// membership on the strength of the marker, so it must observe a concurrent legitimization —
// which clears the marker — the moment it commits, and the space membership lock can only
// guarantee that when the serialized reads are answered by the primary. Per-membership rows also
// make every update a single-row write, so no update can overwrite another's outcome.

// autoJoinedIDs returns the user ids currently marked auto-joined to spaceID.
func (s *Service) autoJoinedIDs(spaceID string) ([]string, error) {
	return s.store.AutoJoinedIDs(spaceID)
}

// markAutoJoined records userID as auto-joined to spaceID. Called only from
// AutoJoinIfDefaultGranted, under the same space-keyed membership lock as the membership add
// itself.
func (s *Service) markAutoJoined(spaceID, userID string) error {
	return s.store.MarkAutoJoined(spaceID, userID)
}

// clearAutoJoined removes userID's auto-join marker for spaceID, if present. Called when the
// membership ends (UndoAutoJoin, RemoveSpaceMember) or when an admin deliberately sets the
// member's capabilities (SetSpaceMemberCapabilities), any of which makes a lingering marker
// stale. Callers run this under the same space-keyed membership lock as the mutation it follows.
func (s *Service) clearAutoJoined(spaceID, userID string) error {
	return s.store.ClearAutoJoined(spaceID, userID)
}
