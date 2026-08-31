// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
)

// stubbedGuests records which users a mockAPI has been told are guests. The catch-all team grants
// below consult it when a call is matched rather than when they are registered, so
// StubGuestTeamDefaults and StubDefaultSpacePermissions may be called in either order.
var stubbedGuests = struct {
	sync.RWMutex
	byAPI map[*plugintest.API]map[string]bool
}{byAPI: map[*plugintest.API]map[string]bool{}}

func registerStubbedGuest(t *testing.T, mockAPI *plugintest.API, userID string) {
	stubbedGuests.Lock()
	defer stubbedGuests.Unlock()
	users, ok := stubbedGuests.byAPI[mockAPI]
	if !ok {
		users = map[string]bool{}
		stubbedGuests.byAPI[mockAPI] = users
		// Registered on the insert that creates the entry, so one cleanup covers every guest
		// later added under the same API.
		t.Cleanup(func() {
			stubbedGuests.Lock()
			defer stubbedGuests.Unlock()
			delete(stubbedGuests.byAPI, mockAPI)
		})
	}
	users[userID] = true
}

func isStubbedGuest(mockAPI *plugintest.API, userID string) bool {
	stubbedGuests.RLock()
	defer stubbedGuests.RUnlock()
	return stubbedGuests.byAPI[mockAPI][userID]
}

// StubDefaultSpacePermissions registers .Maybe() stubs on mockAPI granting every user the
// "ordinary contribute-preset space member" channel permissions plus the team-baseline
// permissions every active team member holds by default (read_space, create_space,
// read_public_channel) — mirroring production's default create posture (open + contribute) so
// tests that assume plain backing-channel membership is sufficient need no per-test permission
// wiring. admin_space/delete_page (channel) and manage_space/delete_space (team) and the system
// manage_system override default to false.
//
// A test exercising an elevation-gated route (manage/admin/sysadmin) registers its own specific
// HasPermissionTo*/HasPermissionToTeam/HasPermissionToChannel expectation for the acting user
// BEFORE calling the harness constructor that invokes this — mock.Mock matches expectations in
// registration order, so the more specific, earlier-registered stub takes precedence over these
// catch-alls.
//
// The team grants here are team_user's. A test whose actor is a guest registers
// StubGuestTeamDefaults for that user, in either order relative to this call: the create_space and
// read_public_channel grants below, neither of which core's team_guest holds, match only users no
// StubGuestTeamDefaults call has named.
func StubDefaultSpacePermissions(mockAPI *plugintest.API) {
	for _, p := range []*mmmodel.Permission{
		mmmodel.PermissionReadPage, mmmodel.PermissionCreatePage, mmmodel.PermissionCommentPage,
		mmmodel.PermissionEditPage, mmmodel.PermissionDeleteOwnPage,
	} {
		mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, p).Return(true).Maybe()
	}
	for _, p := range []*mmmodel.Permission{mmmodel.PermissionDeletePage, mmmodel.PermissionAdminSpace} {
		mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, p).Return(false).Maybe()
	}
	// read_space is team_guest's as well as team_user's, so it is granted to everyone. The other
	// two are team_user's alone, and are withheld from any user registered as a guest.
	mockAPI.On("HasPermissionToTeam", mock.Anything, mock.Anything, mmmodel.PermissionReadSpace).Return(true).Maybe()
	// The audience filter agrees with the read_space grant above: every listed member holds it. A
	// test exercising a member whose team scheme withholds it registers its own answer first.
	mockAPI.On("FilterUsersWithTeamPermission", mock.Anything, mock.Anything, mmmodel.PermissionReadSpace).
		Return(func(_ string, userIDs []string, _ *mmmodel.Permission) ([]string, *mmmodel.AppError) {
			return userIDs, nil
		}).Maybe()
	notGuest := mock.MatchedBy(func(userID string) bool { return !isStubbedGuest(mockAPI, userID) })
	for _, p := range []*mmmodel.Permission{mmmodel.PermissionCreateSpace, mmmodel.PermissionReadPublicChannel} {
		mockAPI.On("HasPermissionToTeam", notGuest, mock.Anything, p).Return(true).Maybe()
	}
	for _, p := range []*mmmodel.Permission{mmmodel.PermissionManageSpace, mmmodel.PermissionDeleteSpace} {
		mockAPI.On("HasPermissionToTeam", mock.Anything, mock.Anything, p).Return(false).Maybe()
	}
	mockAPI.On("HasPermissionTo", mock.Anything, mmmodel.PermissionManageSystem).Return(false).Maybe()
	// Every user is an active member of every team, so a test asserting create behaviour is not
	// also asserting team membership. A test exercising the non-member refusal registers its own
	// answer for that user first.
	mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).
		Return(func(teamID, userID string) (*mmmodel.TeamMember, *mmmodel.AppError) {
			return &mmmodel.TeamMember{TeamId: teamID, UserId: userID}, nil
		}).Maybe()
}

// StubGuestTeamDefaults narrows guestUserID's team grants to what core's team_guest actually holds:
// read_space, but neither create_space nor read_public_channel — the latter being what admits the
// open-space non-member read fall-through a guest must not receive. It may be called before or
// after StubDefaultSpacePermissions, whose grants for those two permissions exclude every user
// named here. The guest marking it records against mockAPI is dropped when t finishes.
func StubGuestTeamDefaults(t *testing.T, mockAPI *plugintest.API, guestUserID string) {
	registerStubbedGuest(t, mockAPI, guestUserID)
	for _, p := range []*mmmodel.Permission{mmmodel.PermissionCreateSpace, mmmodel.PermissionReadPublicChannel} {
		mockAPI.On("HasPermissionToTeam", guestUserID, mock.Anything, p).Return(false).Maybe()
	}
	mockAPI.On("HasPermissionToTeam", guestUserID, mock.Anything, mmmodel.PermissionReadSpace).Return(true).Maybe()
}
