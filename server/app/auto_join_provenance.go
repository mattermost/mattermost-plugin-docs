// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import "slices"

// autoJoinKVKeyPrefix namespaces the auto-join provenance marker in the plugin KV store. The
// marker records which of a space's members were added by AutoJoinIfDefaultGranted rather than
// invited/added deliberately, so a membership review (e.g. after an open->private flip) can tell
// the two apart. It is kept in the KV store rather than a new SQL table: it carries no relational
// data, is read in bulk per space, and this repo otherwise has no KV usage to extend for one flag.
const autoJoinKVKeyPrefix = "auto_join_members_"

func autoJoinKVKey(spaceID string) string {
	return autoJoinKVKeyPrefix + spaceID
}

// autoJoinedIDs returns the user ids currently marked auto-joined to spaceID.
func (s *Service) autoJoinedIDs(spaceID string) ([]string, error) {
	var ids []string
	if err := s.client.KV.Get(autoJoinKVKey(spaceID), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// markAutoJoined records userID as auto-joined to spaceID. Called only from
// AutoJoinIfDefaultGranted, under the same space-keyed membership lock as the membership add
// itself, so the read-modify-write here needs no extra concurrency control.
func (s *Service) markAutoJoined(spaceID, userID string) error {
	ids, err := s.autoJoinedIDs(spaceID)
	if err != nil {
		return err
	}
	if slices.Contains(ids, userID) {
		return nil
	}
	_, err = s.client.KV.Set(autoJoinKVKey(spaceID), append(ids, userID))
	return err
}

// clearAutoJoined removes userID's auto-join marker for spaceID, if present. Called when the
// membership ends (UndoAutoJoin, RemoveSpaceMember) or when an admin deliberately sets the
// member's capabilities (SetSpaceMemberCapabilities), any of which makes a lingering marker stale.
// Callers run this under the same space-keyed membership lock as the mutation it follows.
func (s *Service) clearAutoJoined(spaceID, userID string) error {
	ids, err := s.autoJoinedIDs(spaceID)
	if err != nil {
		return err
	}
	filtered := slices.DeleteFunc(ids, func(id string) bool { return id == userID })
	if len(filtered) == len(ids) {
		return nil
	}
	if len(filtered) == 0 {
		return s.client.KV.Delete(autoJoinKVKey(spaceID))
	}
	_, err = s.client.KV.Set(autoJoinKVKey(spaceID), filtered)
	return err
}
