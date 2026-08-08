// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
)

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

func TestOtherAuthorizedMembers(t *testing.T) {
	s, db := testutil.OpenTestStore(t)
	teamID := mmmodel.NewId()

	// Each case seeds its own channel in the shared store, so cases cannot see each other's rows.
	t.Run("empty channel yields neither member nor admin", func(t *testing.T) {
		channelID := mmmodel.NewId()
		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, mmmodel.NewId())
		require.NoError(t, err)
		require.False(t, anyMember)
		require.False(t, anyAdmin)
	})

	t.Run("active plain member counts as member only; NULL SchemeAdmin reads as not admin", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, userID)
		testutil.MustAddTeamMember(t, db, teamID, userID, 0)

		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, mmmodel.NewId())
		require.NoError(t, err)
		require.True(t, anyMember)
		require.False(t, anyAdmin)
	})

	t.Run("active admin counts as both", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, userID)
		testutil.MustAddTeamMember(t, db, teamID, userID, 0)

		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, mmmodel.NewId())
		require.NoError(t, err)
		require.True(t, anyMember)
		require.True(t, anyAdmin)
	})

	t.Run("the excluded user teaches nothing", func(t *testing.T) {
		channelID := mmmodel.NewId()
		excluded := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, excluded)
		testutil.MustAddTeamMember(t, db, teamID, excluded, 0)

		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, excluded)
		require.NoError(t, err)
		require.False(t, anyMember)
		require.False(t, anyAdmin)
	})

	t.Run("a former team member does not count", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, userID)
		testutil.MustAddTeamMember(t, db, teamID, userID, 12345)

		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, mmmodel.NewId())
		require.NoError(t, err)
		require.False(t, anyMember)
		require.False(t, anyAdmin)
	})

	t.Run("a channel member with no team row does not count", func(t *testing.T) {
		channelID := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, mmmodel.NewId())

		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, mmmodel.NewId())
		require.NoError(t, err)
		require.False(t, anyMember)
		require.False(t, anyAdmin)
	})

	t.Run("an admin surviving alongside an excluded member is still found", func(t *testing.T) {
		channelID := mmmodel.NewId()
		excluded := mmmodel.NewId()
		admin := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, excluded)
		testutil.MustAddTeamMember(t, db, teamID, excluded, 0)
		testutil.MustAddChannelAdmin(t, db, channelID, admin)
		testutil.MustAddTeamMember(t, db, teamID, admin, 0)

		anyMember, anyAdmin, err := s.OtherAuthorizedMembers(channelID, teamID, excluded)
		require.NoError(t, err)
		require.True(t, anyMember)
		require.True(t, anyAdmin)
	})
}

func TestTeamChannelMemberAudience(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	teamID := mmmodel.NewId()
	channelID := mmmodel.NewId()
	testutil.MustAddChannel(t, db, channelID, teamID)

	active := mmmodel.NewId()
	former := mmmodel.NewId()
	noTeamRow := mmmodel.NewId()

	testutil.MustAddChannelMember(t, db, channelID, active)
	testutil.MustAddTeamMember(t, db, teamID, active, 0)

	testutil.MustAddChannelMember(t, db, channelID, former)
	testutil.MustAddTeamMember(t, db, teamID, former, 12345)

	testutil.MustAddChannelMember(t, db, channelID, noTeamRow)

	inactive, err := s.InactiveTeamChannelMembers(channelID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{former, noTeamRow}, inactive,
		"both the soft-deleted team member and the row without a team membership must be omitted")

	activeIDs, err := s.ActiveTeamChannelMembers(channelID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{active}, activeIDs,
		"only the active team member is part of the audience")

	// A membership of some other team must not authorize this channel's audience.
	otherTeamOnly := mmmodel.NewId()
	testutil.MustAddChannelMember(t, db, channelID, otherTeamOnly)
	testutil.MustAddTeamMember(t, db, mmmodel.NewId(), otherTeamOnly, 0)

	inactive, err = s.InactiveTeamChannelMembers(channelID)
	require.NoError(t, err)
	require.Contains(t, inactive, otherTeamOnly,
		"an active membership of a different team must not count for this channel's team")
}

func TestAutoJoinProvenance(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)

	spaceID := mmmodel.NewId()
	userA := mmmodel.NewId()
	userB := mmmodel.NewId()

	ids, err := s.AutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.Empty(t, ids)

	require.NoError(t, s.MarkAutoJoined(spaceID, userA))
	require.NoError(t, s.MarkAutoJoined(spaceID, userA), "re-marking must be a no-op, not an error")
	require.NoError(t, s.MarkAutoJoined(spaceID, userB))

	ids, err = s.AutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userA, userB}, ids)

	require.NoError(t, s.ClearAutoJoined(spaceID, userA))
	require.NoError(t, s.ClearAutoJoined(spaceID, userA), "clearing an absent marker must be a no-op")

	ids, err = s.AutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userB}, ids)

	// Markers are per space: another space's set is untouched.
	otherSpace := mmmodel.NewId()
	require.NoError(t, s.MarkAutoJoined(otherSpace, userA))
	ids, err = s.AutoJoinedIDs(spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userB}, ids)
}
