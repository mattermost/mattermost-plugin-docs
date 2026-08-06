// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testutil

import (
	"github.com/stretchr/testify/mock"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
)

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
// The team grants here are team_user's. A test whose actor is a guest must register
// StubGuestTeamDefaults for that user first: the catch-all below matches any user and grants both
// create_space and read_public_channel, neither of which core's team_guest holds.
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
	for _, p := range []*mmmodel.Permission{
		mmmodel.PermissionReadSpace, mmmodel.PermissionCreateSpace, mmmodel.PermissionReadPublicChannel,
	} {
		mockAPI.On("HasPermissionToTeam", mock.Anything, mock.Anything, p).Return(true).Maybe()
	}
	for _, p := range []*mmmodel.Permission{mmmodel.PermissionManageSpace, mmmodel.PermissionDeleteSpace} {
		mockAPI.On("HasPermissionToTeam", mock.Anything, mock.Anything, p).Return(false).Maybe()
	}
	mockAPI.On("HasPermissionTo", mock.Anything, mmmodel.PermissionManageSystem).Return(false).Maybe()
	// teamPermGranted consults the resolved team roles before falling back to HasPermissionToTeam.
	// The stubbed team memberships carry no roles, so this arm decides nothing and the
	// HasPermissionToTeam expectations above remain the ones that express a test's team grants.
	//
	// Scoped to the team permissions rather than registered as a catch-all: RolesGrantPermission is
	// also how DefaultRolesGrantPermission resolves a space's *page* defaults, and a catch-all here
	// would match those first and shadow the per-test expectations that drive the auto-join paths.
	for _, p := range []*mmmodel.Permission{
		mmmodel.PermissionReadSpace, mmmodel.PermissionCreateSpace, mmmodel.PermissionReadPublicChannel,
		mmmodel.PermissionManageSpace, mmmodel.PermissionDeleteSpace,
	} {
		mockAPI.On("RolesGrantPermission", mock.Anything, p.Id).Return(false).Maybe()
	}
}

// StubGuestTeamDefaults narrows guestUserID's team grants to what core's team_guest actually holds:
// read_space, but neither create_space nor read_public_channel. Register it BEFORE
// StubDefaultSpacePermissions, whose catch-alls match any user and grant the read_public_channel
// that admits the open-space non-member read fall-through — a guest must not receive it.
func StubGuestTeamDefaults(mockAPI *plugintest.API, guestUserID string) {
	for _, p := range []*mmmodel.Permission{mmmodel.PermissionCreateSpace, mmmodel.PermissionReadPublicChannel} {
		mockAPI.On("HasPermissionToTeam", guestUserID, mock.Anything, p).Return(false).Maybe()
	}
	mockAPI.On("HasPermissionToTeam", guestUserID, mock.Anything, mmmodel.PermissionReadSpace).Return(true).Maybe()
}
