// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// TestGetOrCreateSharedScheme_ConcurrentCreateAdoptsWinner covers the race guard: the pooled
// scheme's name is a pure function of the capability set, so two callers racing the same
// capability set collide on create. The loser must adopt the winner's scheme instead of failing
// the caller outright.
func TestGetOrCreateSharedScheme_ConcurrentCreateAdoptsWinner(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	mockAPI := &plugintest.API{}
	capabilities := []string{"create_page"}
	name := model.SharedSchemeNameForCapabilities(capabilities)

	winner := &mmmodel.Scheme{
		Id:                      mmmodel.NewId(),
		Name:                    name,
		DefaultChannelUserRole:  "winner_user_role",
		DefaultChannelAdminRole: "winner_admin_role",
		DefaultChannelGuestRole: "winner_guest_role",
	}

	// The first lookup misses: this caller has not seen the scheme yet.
	mockAPI.On("GetSchemeByName", name).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{Id: "app.scheme.get.app_error", StatusCode: 404}).Once()
	// This caller's own create loses the race.
	mockAPI.On("CreateScheme", mock.AnythingOfType("*model.Scheme")).
		Return((*mmmodel.Scheme)(nil), &mmmodel.AppError{Id: "app.scheme.create.app_error", StatusCode: 500}).Once()
	// The second lookup, taken after the failed create, finds the concurrent winner.
	mockAPI.On("GetSchemeByName", name).Return(winner, nil).Once()

	client := pluginapi.NewClient(mockAPI, nil)
	svc := New(s, nil, client)

	schemeID, roles, err := svc.getOrCreateSharedScheme(capabilities)
	require.NoError(t, err, "a create lost to a concurrent winner must not fail the caller")
	require.Equal(t, winner.Id, schemeID)
	require.NotNil(t, roles)
	require.Equal(t, "winner_user_role", roles.UserRoleName)
	require.Equal(t, "winner_admin_role", roles.AdminRoleName)
	require.Equal(t, "winner_guest_role", roles.GuestRoleName)
	mockAPI.AssertExpectations(t)
}

// TestSchemeRolesFromChannel_MissingSchemeVariants covers both shapes of "no scheme" the code
// treats as distinct cases: a nil SchemeId and a non-nil SchemeId pointing at an empty string.
// Either must resolve to not-found rather than falling through to a scheme lookup.
func TestSchemeRolesFromChannel_MissingSchemeVariants(t *testing.T) {
	s, _ := testutil.OpenTestStore(t)
	svc := New(s, nil, nil)

	t.Run("nil SchemeId", func(t *testing.T) {
		channel := &mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace}
		_, err := svc.schemeRolesFromChannel(channel.Id, channel)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err))
	})

	t.Run("empty string SchemeId", func(t *testing.T) {
		empty := ""
		channel := &mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace, SchemeId: &empty}
		_, err := svc.schemeRolesFromChannel(channel.Id, channel)
		require.Error(t, err)
		require.True(t, store.IsErrNotFound(err))
	})
}
