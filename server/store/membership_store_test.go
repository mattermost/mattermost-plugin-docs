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

func TestMemberSchemeFlags(t *testing.T) {
	s, db := testutil.OpenTestStore(t)

	t.Run("plain member reads as neither admin nor guest", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelMember(t, db, channelID, userID)

		schemeAdmin, schemeGuest, err := s.MemberSchemeFlags(channelID, userID)
		require.NoError(t, err)
		require.False(t, schemeAdmin)
		require.False(t, schemeGuest)
	})

	t.Run("admin row reads SchemeAdmin true", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelAdmin(t, db, channelID, userID)

		schemeAdmin, schemeGuest, err := s.MemberSchemeFlags(channelID, userID)
		require.NoError(t, err)
		require.True(t, schemeAdmin)
		require.False(t, schemeGuest)
	})

	t.Run("guest row reads SchemeGuest true", func(t *testing.T) {
		channelID := mmmodel.NewId()
		userID := mmmodel.NewId()
		testutil.MustAddChannelGuest(t, db, channelID, userID)

		schemeAdmin, schemeGuest, err := s.MemberSchemeFlags(channelID, userID)
		require.NoError(t, err)
		require.False(t, schemeAdmin)
		require.True(t, schemeGuest)
	})

	t.Run("absent row is not found", func(t *testing.T) {
		_, _, err := s.MemberSchemeFlags(mmmodel.NewId(), mmmodel.NewId())
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("empty ids are rejected", func(t *testing.T) {
		_, _, err := s.MemberSchemeFlags("", mmmodel.NewId())
		require.Error(t, err)

		_, _, err = s.MemberSchemeFlags(mmmodel.NewId(), "")
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
		"the soft-deleted team member and the row without a team membership must both read as inactive")

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

// TestIsAutoJoined covers the single-membership marker test the undo path uses. It must agree with
// AutoJoinedIDs on every state — a disagreement would either strand an auto-joined membership the
// undo should remove, or let the undo delete one an admin legitimized.
func TestIsAutoJoined(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)

	spaceID := mmmodel.NewId()
	marked := mmmodel.NewId()
	unmarked := mmmodel.NewId()

	autoJoined, err := s.IsAutoJoined(spaceID, marked)
	require.NoError(t, err)
	require.False(t, autoJoined, "an unmarked membership must not report as auto-joined")

	require.NoError(t, s.MarkAutoJoined(spaceID, marked))

	autoJoined, err = s.IsAutoJoined(spaceID, marked)
	require.NoError(t, err)
	require.True(t, autoJoined)

	autoJoined, err = s.IsAutoJoined(spaceID, unmarked)
	require.NoError(t, err)
	require.False(t, autoJoined, "the marker must be per member, not per space")

	// Scoped to the space as well as the member: the same user marked in another space must not
	// report as auto-joined here, or an undo in one space could remove a membership in another.
	otherSpace := mmmodel.NewId()
	require.NoError(t, s.MarkAutoJoined(otherSpace, unmarked))
	autoJoined, err = s.IsAutoJoined(spaceID, unmarked)
	require.NoError(t, err)
	require.False(t, autoJoined)

	require.NoError(t, s.ClearAutoJoined(spaceID, marked))
	autoJoined, err = s.IsAutoJoined(spaceID, marked)
	require.NoError(t, err)
	require.False(t, autoJoined, "clearing the marker must be observable here, since the undo gates on it")

	_, err = s.IsAutoJoined("", marked)
	require.Error(t, err, "an empty space id is invalid input, not a silent false")
	_, err = s.IsAutoJoined(spaceID, "")
	require.Error(t, err, "an empty user id is invalid input, not a silent false")
}
