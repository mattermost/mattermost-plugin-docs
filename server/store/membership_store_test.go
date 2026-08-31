// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// GetSpaceMemberRoles is the read that stands in for the plugin API's replica-backed member
// lookup, so it has to carry everything a permission projection needs: the capability roles in the
// explicit roles alongside the scheme flags, and a distinguishable miss for a non-member.
func TestGetSpaceMemberRoles(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	t.Run("returns the explicit roles and the scheme flags", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, userID)
		_, err := db.Exec(`UPDATE ChannelMembers SET Roles = $1 WHERE ChannelId = $2 AND UserId = $3`,
			mmmodel.SpacePageEditorRoleId, channelID, userID)
		require.NoError(t, err)

		member, err := s.GetSpaceMemberRoles(channelID, userID)
		require.NoError(t, err)
		require.Equal(t, mmmodel.SpacePageEditorRoleId, member.ExplicitRoles)
		require.True(t, member.SchemeAdmin)
		require.False(t, member.SchemeGuest)
	})

	t.Run("a NULL roles column reads as empty", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelGuest(t, db, channelID, userID)

		member, err := s.GetSpaceMemberRoles(channelID, userID)
		require.NoError(t, err)
		require.Empty(t, member.ExplicitRoles)
		require.False(t, member.SchemeAdmin)
		require.True(t, member.SchemeGuest)
	})

	t.Run("a non-member is a distinguishable miss", func(t *testing.T) {
		_, err := s.GetSpaceMemberRoles(mmmodel.NewId(), mmmodel.NewId())
		require.True(t, store.IsErrNotFound(err), "a non-member must be tellable from a lookup failure")
	})

	t.Run("empty ids are rejected", func(t *testing.T) {
		_, err := s.GetSpaceMemberRoles("", mmmodel.NewId())
		require.Error(t, err)
		_, err = s.GetSpaceMemberRoles(mmmodel.NewId(), "")
		require.Error(t, err)
	})
}

func TestMemberSchemeFlags(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	t.Run("plain member reads as neither admin nor guest", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, userID)

		schemeAdmin, schemeGuest, err := s.GetMemberSchemeFlags(channelID, userID)
		require.NoError(t, err)
		require.False(t, schemeAdmin)
		require.False(t, schemeGuest)
	})

	t.Run("admin row reads SchemeAdmin true", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, userID)

		schemeAdmin, schemeGuest, err := s.GetMemberSchemeFlags(channelID, userID)
		require.NoError(t, err)
		require.True(t, schemeAdmin)
		require.False(t, schemeGuest)
	})

	t.Run("guest row reads SchemeGuest true", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelGuest(t, db, channelID, userID)

		schemeAdmin, schemeGuest, err := s.GetMemberSchemeFlags(channelID, userID)
		require.NoError(t, err)
		require.False(t, schemeAdmin)
		require.True(t, schemeGuest)
	})

	t.Run("absent row is not found", func(t *testing.T) {
		_, _, err := s.GetMemberSchemeFlags(mmmodel.NewId(), mmmodel.NewId())
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("empty ids are rejected", func(t *testing.T) {
		_, _, err := s.GetMemberSchemeFlags("", mmmodel.NewId())
		require.Error(t, err)

		_, _, err = s.GetMemberSchemeFlags(mmmodel.NewId(), "")
		require.Error(t, err)
	})
}

func TestIsChannelMember(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	channelID := mmmodel.NewId()
	memberID := mmmodel.NewId()
	testutil.MustAddChannelMember(t, db, channelID, memberID)

	isMember, err := s.IsChannelMember(channelID, memberID)
	require.NoError(t, err)
	require.True(t, isMember, "seeded member must be found")

	isMember, err = s.IsChannelMember(channelID, mmmodel.NewId())
	require.NoError(t, err)
	require.False(t, isMember, "unseeded user must not be found")

	_, err = s.IsChannelMember("", memberID)
	require.Error(t, err, "empty channel id must be rejected")
}

func TestGetChannelMembership(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	t.Run("empty channel lists nobody", func(t *testing.T) {
		membership, err := s.GetChannelMembership(mmmodel.NewId())
		require.NoError(t, err)
		require.Empty(t, membership.Members)
		require.Empty(t, membership.TeamID)
	})

	t.Run("every row is listed with its admin flag and the channel's team", func(t *testing.T) {
		teamID := mmmodel.NewId()
		channelID := mmmodel.NewId()
		testutil.MustAddChannel(t, db, channelID, teamID)
		plain := mmmodel.NewId()
		admin := mmmodel.NewId()
		formerTeamMember := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, plain)
		testutil.MustAddChannelAdmin(t, db, channelID, admin)
		testutil.MustAddChannelMember(t, db, channelID, formerTeamMember)
		testutil.MustAddTeamMember(t, db, teamID, formerTeamMember, 12345)
		// A member of some other channel is not part of this one.
		testutil.MustAddChannelMember(t, db, mmmodel.NewId(), mmmodel.NewId())

		membership, err := s.GetChannelMembership(channelID)
		require.NoError(t, err)
		require.Equal(t, teamID, membership.TeamID)
		// A NULL SchemeAdmin reads as not-admin; team membership is not consulted here — the
		// former team member is listed like any other row, and core decides whether it is admitted.
		require.ElementsMatch(t, []store.ChannelMemberRef{
			{UserID: plain, SchemeAdmin: false},
			{UserID: admin, SchemeAdmin: true},
			{UserID: formerTeamMember, SchemeAdmin: false},
		}, membership.Members)
	})

	t.Run("account liveness is resolved through Users and fails closed", func(t *testing.T) {
		teamID := mmmodel.NewId()
		channelID := mmmodel.NewId()
		testutil.MustAddChannel(t, db, channelID, teamID)
		active := mmmodel.NewId()
		deactivated := mmmodel.NewId()
		unknown := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, active)
		testutil.MustAddChannelAdmin(t, db, channelID, deactivated)
		testutil.MustDeactivateUser(t, db, deactivated)
		// A ChannelMembers row with no Users row reads as deactivated: liveness cannot be
		// confirmed, so the listing must not report the member as reachable.
		_, err := db.Exec(`INSERT INTO ChannelMembers (ChannelId, UserId) VALUES ($1, $2)`, channelID, unknown)
		require.NoError(t, err)

		membership, err := s.GetChannelMembership(channelID)
		require.NoError(t, err)
		require.Equal(t, teamID, membership.TeamID)
		require.ElementsMatch(t, []store.ChannelMemberRef{
			{UserID: active},
			{UserID: deactivated, SchemeAdmin: true, Deactivated: true},
			{UserID: unknown, Deactivated: true},
		}, membership.Members)
	})

	t.Run("a missing channel row still lists the members, with no team", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, userID)

		membership, err := s.GetChannelMembership(channelID)
		require.NoError(t, err)
		require.Empty(t, membership.TeamID)
		require.Equal(t, []store.ChannelMemberRef{{UserID: userID}}, membership.Members)
	})

	t.Run("an empty channel id is rejected", func(t *testing.T) {
		_, err := s.GetChannelMembership("")
		var invErr *store.ErrInvalidInput
		require.ErrorAs(t, err, &invErr)
	})
}

func TestAutoJoinProvenance(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)

	spaceID := mmmodel.NewId()
	userA := mmmodel.NewId()
	userB := mmmodel.NewId()

	ids, err := s.GetAutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.Empty(t, ids)

	require.NoError(t, s.MarkAutoJoined(spaceID, userA))
	require.NoError(t, s.MarkAutoJoined(spaceID, userA), "re-marking must be a no-op, not an error")
	require.NoError(t, s.MarkAutoJoined(spaceID, userB))

	ids, err = s.GetAutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userA, userB}, ids)

	require.NoError(t, s.ClearAutoJoined(spaceID, userA))
	require.NoError(t, s.ClearAutoJoined(spaceID, userA), "clearing an absent marker must be a no-op")

	ids, err = s.GetAutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userB}, ids)

	// Markers are per space: another space's set is untouched.
	otherSpace := mmmodel.NewId()
	require.NoError(t, s.MarkAutoJoined(otherSpace, userA))
	ids, err = s.GetAutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userB}, ids)
}
