// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"
)

// This file reads core's ChannelMembers, Channels, and Users tables directly on the plugin's
// master DB handle, following the ChannelMembers EXISTS in GetSpacesForTeam. The plugin API's membership
// reads are answered by core's store from a read replica, but the callers here back invariants —
// last-admin and last-reachable-member protection, auto-join idempotency, WS recipient filtering —
// that must observe a membership write committed on the primary an instant earlier; under replica
// lag a replica read can miss it even while the space membership lock serializes the writers.
// Reads only, and structural only: membership writes keep going through the plugin API so core's
// caching and events stay correct, and whether a member still holds team read_space is asked of
// core rather than derived from TeamMembers here.

// GetMemberSchemeFlags returns the SchemeAdmin and SchemeGuest flags of userID's ChannelMembers row
// for channelID, answered from the master. Both flags are nullable in core's schema; NULL reads
// as false, matching core's own scan-time handling. Returns ErrNotFound when no row exists, so
// callers can distinguish a non-member from an ordinary member.
func (s *Store) GetMemberSchemeFlags(channelID, userID string) (schemeAdmin, schemeGuest bool, err error) {
	if channelID == "" || userID == "" {
		return false, false, &ErrInvalidInput{Entity: "ChannelMember", Field: "id", Value: channelID + "/" + userID}
	}

	builder := s.getQueryBuilder().
		Select(
			"COALESCE(SchemeAdmin, FALSE) AS SchemeAdmin",
			"COALESCE(SchemeGuest, FALSE) AS SchemeGuest",
		).
		From("ChannelMembers").
		Where(sq.Eq{"ChannelId": channelID, "UserId": userID})

	var row struct {
		SchemeAdmin bool `db:"schemeadmin"`
		SchemeGuest bool `db:"schemeguest"`
	}
	if qErr := s.getBuilder(s.db, &row, builder); qErr != nil {
		if errors.Is(qErr, sql.ErrNoRows) {
			return false, false, &ErrNotFound{EntityName: "ChannelMember", ID: channelID + "/" + userID}
		}
		return false, false, errors.Wrap(qErr, "unable_to_get_member_scheme_flags")
	}
	return row.SchemeAdmin, row.SchemeGuest, nil
}

// IsChannelMember reports whether userID has a ChannelMembers row for channelID, answered from
// the master.
func (s *Store) IsChannelMember(channelID, userID string) (bool, error) {
	if channelID == "" || userID == "" {
		return false, &ErrInvalidInput{Entity: "ChannelMember", Field: "id", Value: channelID + "/" + userID}
	}

	builder := s.getQueryBuilder().
		Select("COUNT(*) > 0").
		From("ChannelMembers").
		Where(sq.Eq{"ChannelId": channelID, "UserId": userID})

	var isMember bool
	if err := s.getBuilder(s.db, &isMember, builder); err != nil {
		return false, errors.Wrap(err, "unable_to_check_channel_membership")
	}
	return isMember, nil
}

// ChannelMemberRef is one backing-channel membership row reduced to what audience resolution and
// the last-admin guard need.
type ChannelMemberRef struct {
	UserID      string
	SchemeAdmin bool

	// Deactivated reports that the member's user account is deactivated (Users.DeleteAt set) or
	// that no Users row exists for it. Deactivation clears sessions but leaves TeamMembers and
	// ChannelMembers rows in place, so without this flag a deactivated account still looks like a
	// member who can reach the space.
	Deactivated bool
}

// ChannelMembership is a backing channel's whole membership together with the channel's team,
// which the caller needs next to ask core which of the members hold team read_space.
type ChannelMembership struct {
	TeamID  string
	Members []ChannelMemberRef
}

// GetChannelMembership lists every ChannelMembers row of channelID with its SchemeAdmin flag, the
// account-deactivation state resolved through the Users row, and the channel's team resolved
// through the Channels row so a caller holding only a channel id can use it. Both joins are outer
// so a membership row is still listed when its channel or user row is missing; a missing channel
// row makes the team read as empty and core admits nobody for it, and a missing user row reads as
// deactivated — both fail closed. SchemeAdmin is nullable in core's schema; a NULL reads as
// not-admin, matching core's own scan-time handling.
//
// Structural only: whether a listed member may still reach the space — an active team membership
// whose team scheme grants read_space — is core's decision, asked per audience through the plugin
// API, never a TeamMembers predicate here. The Users join reports account liveness, which is not
// part of that permission decision: core resolves the permission from the membership and scheme
// rows, which deactivation leaves in place.
//
// Deliberately unpaginated, and it must stay that way. The result feeds a WebSocket omit list
// (publishToChannels), so a partial answer does not degrade a listing — it delivers the event to
// members the read gate rejects, which is the leak the omit list exists to prevent. The row count
// is bounded by the channel's own membership (the predicate is cm.ChannelId), not by team size, so
// it is the same order as the broadcast's own fan-out.
func (s *Store) GetChannelMembership(channelID string) (*ChannelMembership, error) {
	if channelID == "" {
		return nil, &ErrInvalidInput{Entity: "ChannelMember", Field: "channel_id", Value: channelID}
	}

	builder := s.getQueryBuilder().
		Select(
			"COALESCE(c.TeamId, '') AS TeamId",
			"cm.UserId AS UserId",
			"COALESCE(cm.SchemeAdmin, FALSE) AS SchemeAdmin",
			"(u.DeleteAt IS NULL OR u.DeleteAt <> 0) AS Deactivated",
		).
		From("ChannelMembers cm").
		LeftJoin("Channels c ON c.Id = cm.ChannelId").
		LeftJoin("Users u ON u.Id = cm.UserId").
		Where(sq.Eq{"cm.ChannelId": channelID})

	var rows []struct {
		TeamID      string `db:"teamid"`
		UserID      string `db:"userid"`
		SchemeAdmin bool   `db:"schemeadmin"`
		Deactivated bool   `db:"deactivated"`
	}
	if err := s.selectBuilder(s.db, &rows, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_list_channel_membership")
	}
	membership := &ChannelMembership{Members: make([]ChannelMemberRef, 0, len(rows))}
	for _, row := range rows {
		membership.TeamID = row.TeamID
		membership.Members = append(membership.Members, ChannelMemberRef{UserID: row.UserID, SchemeAdmin: row.SchemeAdmin, Deactivated: row.Deactivated})
	}
	return membership, nil
}

// MarkAutoJoined records userID as auto-joined to spaceID. Marking an already-marked pair is a
// no-op, so the write needs no prior read.
func (s *Store) MarkAutoJoined(spaceID, userID string) error {
	if spaceID == "" || userID == "" {
		return &ErrInvalidInput{Entity: "SpaceAutoJoin", Field: "id", Value: spaceID + "/" + userID}
	}

	builder := s.getQueryBuilder().
		Insert("DOCS_SpaceAutoJoin").
		Columns("SpaceId", "UserId").
		Values(spaceID, userID).
		Suffix("ON CONFLICT (SpaceId, UserId) DO NOTHING")

	if _, err := s.execBuilder(s.db, builder); err != nil {
		return errors.Wrap(err, "unable_to_mark_auto_joined")
	}
	return nil
}

// ClearAutoJoined removes userID's auto-join marker for spaceID. Clearing an absent marker is a
// no-op, so the delete needs no prior read.
func (s *Store) ClearAutoJoined(spaceID, userID string) error {
	if spaceID == "" || userID == "" {
		return &ErrInvalidInput{Entity: "SpaceAutoJoin", Field: "id", Value: spaceID + "/" + userID}
	}

	builder := s.getQueryBuilder().
		Delete("DOCS_SpaceAutoJoin").
		Where(sq.Eq{"SpaceId": spaceID, "UserId": userID})

	if _, err := s.execBuilder(s.db, builder); err != nil {
		return errors.Wrap(err, "unable_to_clear_auto_joined")
	}
	return nil
}

// Auto-join provenance markers are written by JoinOpenSpace and normally cleared by deliberate
// membership changes. Consumers treat a row as provenance recorded by the plugin, not proof of a
// live membership: marker cleanup after a membership is gone is best-effort. The markers live in
// DOCS_SpaceAutoJoin on the master DB handle and select which backing memberships must be removed
// before an open space can become private. No access gate consults them.
// Per-membership rows make every update a single-row write, so no update can overwrite another's
// outcome.

// GetAutoJoinedIDs returns the user ids currently marked auto-joined to spaceID, for callers
// projecting the marker onto a set of members they already hold.
func (s *Store) GetAutoJoinedIDs(spaceID string) ([]string, error) {
	if spaceID == "" {
		return nil, &ErrInvalidInput{Entity: "SpaceAutoJoin", Field: "space_id", Value: spaceID}
	}

	builder := s.getQueryBuilder().
		Select("UserId").
		From("DOCS_SpaceAutoJoin").
		Where(sq.Eq{"SpaceId": spaceID})

	var ids []string
	if err := s.selectBuilder(s.db, &ids, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_list_auto_joined")
	}
	return ids, nil
}
