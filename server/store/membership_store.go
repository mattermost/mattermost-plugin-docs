// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"

	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"
)

// This file reads core's membership tables (ChannelMembers, TeamMembers, Channels) directly on
// the plugin's master DB handle, following the ChannelMembers EXISTS in GetSpacesForTeam. The
// plugin API's membership reads are answered by core's store from a read replica, but the callers
// here back invariants — last-admin and last-reachable-member protection, auto-join idempotency,
// WS recipient filtering — that must observe a membership write committed on the primary an
// instant earlier; under replica lag a replica read can miss it even while the space membership
// lock serializes the writers. Reads only: membership writes keep going through the plugin API so
// core's caching and events stay correct.

// activeTeamMemberJoin is the TeamMembers join shared by every query in this file that counts
// only active team members, so the predicate cannot drift between copies. teamRef is the SQL
// expression supplying the team id — a bind placeholder or a joined column reference.
func activeTeamMemberJoin(teamRef string) string {
	return "TeamMembers tm ON tm.UserId = cm.UserId AND tm.TeamId = " + teamRef + " AND tm.DeleteAt = 0"
}

// MemberSchemeFlags returns the SchemeAdmin and SchemeGuest flags of userID's ChannelMembers row
// for channelID, answered from the master. Both flags are nullable in core's schema; NULL reads
// as false, matching core's own scan-time handling. Returns ErrNotFound when no row exists, so
// callers can distinguish a non-member from an ordinary member.
func (s *Store) MemberSchemeFlags(channelID, userID string) (schemeAdmin, schemeGuest bool, err error) {
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

// OtherAuthorizedMembers reports whether channelID has, disregarding excludeUserID, a member who
// is an active member of teamID (anyMember), and whether any such member is a channel scheme
// admin (anyAdmin). Former team members keep their channel-member rows, so the team join — with
// the same DeleteAt=0 predicate as the app layer's activeTeamMember — is what makes a row count.
// SchemeAdmin is nullable in core's schema; a NULL reads as not-admin, matching core's own
// scan-time handling.
func (s *Store) OtherAuthorizedMembers(channelID, teamID, excludeUserID string) (anyMember, anyAdmin bool, err error) {
	if channelID == "" || teamID == "" {
		return false, false, &ErrInvalidInput{Entity: "ChannelMember", Field: "channel_id", Value: channelID}
	}

	builder := s.getQueryBuilder().
		Select(
			"COUNT(*) > 0 AS AnyMember",
			"COALESCE(BOOL_OR(COALESCE(cm.SchemeAdmin, FALSE)), FALSE) AS AnyAdmin",
		).
		From("ChannelMembers cm").
		Join(activeTeamMemberJoin("?"), teamID).
		Where(sq.Eq{"cm.ChannelId": channelID}).
		Where(sq.NotEq{"cm.UserId": excludeUserID})

	var row struct {
		AnyMember bool `db:"anymember"`
		AnyAdmin  bool `db:"anyadmin"`
	}
	if qErr := s.getBuilder(s.db, &row, builder); qErr != nil {
		return false, false, errors.Wrap(qErr, "unable_to_count_authorized_members")
	}
	return row.AnyMember, row.AnyAdmin, nil
}

// InactiveTeamChannelMembers returns the members of channelID holding no active membership in the
// channel's own team — the rows that survive a team departure. The channel's team is resolved
// through the Channels row rather than taken as a parameter, so a caller holding only a channel
// id can use it.
func (s *Store) InactiveTeamChannelMembers(channelID string) ([]string, error) {
	if channelID == "" {
		return nil, &ErrInvalidInput{Entity: "ChannelMember", Field: "channel_id", Value: channelID}
	}

	builder := s.getQueryBuilder().
		Select("cm.UserId").
		From("ChannelMembers cm").
		Join("Channels c ON c.Id = cm.ChannelId").
		LeftJoin(activeTeamMemberJoin("c.TeamId")).
		Where(sq.Eq{"cm.ChannelId": channelID}).
		Where("tm.UserId IS NULL")

	var ids []string
	if err := s.selectBuilder(s.db, &ids, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_list_inactive_channel_members")
	}
	return ids, nil
}

// ActiveTeamChannelMembers returns the members of channelID who are active members of the
// channel's own team — the audience a space's events may reach.
func (s *Store) ActiveTeamChannelMembers(channelID string) ([]string, error) {
	if channelID == "" {
		return nil, &ErrInvalidInput{Entity: "ChannelMember", Field: "channel_id", Value: channelID}
	}

	builder := s.getQueryBuilder().
		Select("cm.UserId").
		From("ChannelMembers cm").
		Join("Channels c ON c.Id = cm.ChannelId").
		Join(activeTeamMemberJoin("c.TeamId")).
		Where(sq.Eq{"cm.ChannelId": channelID})

	var ids []string
	if err := s.selectBuilder(s.db, &ids, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_list_active_channel_members")
	}
	return ids, nil
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

// AutoJoinedIDs returns the user ids currently marked auto-joined to spaceID.
func (s *Store) AutoJoinedIDs(spaceID string) ([]string, error) {
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
