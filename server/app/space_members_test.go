// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// RemoveSpaceMember's admin-target path: the escalation guard and the last-admin invariant, both
// exercised only when the target holds SchemeAdmin. TestServiceRemoveSpaceMember_* in space_test.go
// covers the plain-member paths (last active member, former team member); this file covers the
// admin-target paths those tests never reach. It also covers AddSpaceMember's no-op re-add path.

package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
)

// TestServiceRemoveSpaceMember_AdminTargetRequiresAdminOrSysadmin verifies a caller who holds
// neither channel admin_space nor sysadmin is refused removing a SchemeAdmin target, with the same
// existence-hiding 403 every other escalation-guarded write returns.
func TestServiceRemoveSpaceMember_AdminTargetRequiresAdminOrSysadmin(t *testing.T) {
	mockAPI := &plugintest.API{}
	callerID := mmmodel.NewId()
	// Registered before the harness so it wins over StubDefaultSpacePermissions' manage_space
	// catch-all (default false): the caller holds team manage_space but neither channel admin_space
	// nor sysadmin, which RequireSpaceAdminOrSysadmin reads independently of the manage tier.
	mockAPI.On("HasPermissionToTeam", callerID, mock.Anything, mmmodel.PermissionManageSpace).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	targetAdminID := mmmodel.NewId()
	testutil.MustAddChannelAdmin(t, h.db, space.ChannelId, targetAdminID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, targetAdminID, 0)

	appErr := h.svc.RemoveSpaceMember(space, targetAdminID, callerID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, targetAdminID)
}

// TestServiceRemoveSpaceMember_SoleAdminLastAdminConflict verifies removing the space's sole
// authorized admin is rejected with 409, even for a sysadmin caller who clears the escalation
// guard. The fixture has no other member at all, so this also pins that the last-admin check is
// evaluated before the last-member check: an inverted or dropped last-admin conjunct would instead
// report the sibling last_member conflict.
func TestServiceRemoveSpaceMember_SoleAdminLastAdminConflict(t *testing.T) {
	mockAPI := &plugintest.API{}
	sysadminID := mmmodel.NewId()
	mockAPI.On("HasPermissionTo", sysadminID, mmmodel.PermissionManageSystem).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	soleAdminID := mmmodel.NewId()
	testutil.MustAddChannelAdmin(t, h.db, space.ChannelId, soleAdminID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, soleAdminID, 0)

	appErr := h.svc.RemoveSpaceMember(space, soleAdminID, sysadminID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
	require.Equal(t, "app.space.member.last_admin.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, soleAdminID)
}

// TestServiceRemoveSpaceMember_OtherAdminAllowsRemoval is the positive counterpart: removing one of
// two admins succeeds once another reachable admin remains.
func TestServiceRemoveSpaceMember_OtherAdminAllowsRemoval(t *testing.T) {
	mockAPI := &plugintest.API{}
	sysadminID := mmmodel.NewId()
	mockAPI.On("HasPermissionTo", sysadminID, mmmodel.PermissionManageSystem).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	targetAdminID := mmmodel.NewId()
	otherAdminID := mmmodel.NewId()
	testutil.MustAddChannelAdmin(t, h.db, space.ChannelId, targetAdminID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, targetAdminID, 0)
	testutil.MustAddChannelAdmin(t, h.db, space.ChannelId, otherAdminID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, otherAdminID, 0)
	mockAPI.On("DeleteChannelMember", space.ChannelId, targetAdminID).Return(nil)

	appErr := h.svc.RemoveSpaceMember(space, targetAdminID, sysadminID)
	require.Nil(t, appErr)
	mockAPI.AssertCalled(t, "DeleteChannelMember", space.ChannelId, targetAdminID)
}

// TestServiceAddSpaceMember_ReAddSkipsEvent verifies AddSpaceMember does not publish
// space_member_added when the target already holds the membership: core's AddMember is a no-op
// for an existing member, so publishing on that path would deliver a phantom "member added" event
// for a membership that did not change.
func TestServiceAddSpaceMember_ReAddSkipsEvent(t *testing.T) {
	mockAPI := &plugintest.API{}
	h := openTestServiceWithAPI(t, mockAPI)
	space, _ := createSpaceForMemberTests(t, h, mockAPI)

	existingID := mmmodel.NewId()
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, existingID)
	mockAPI.On("AddChannelMember", space.ChannelId, existingID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: existingID}, nil)

	_, appErr := h.svc.AddSpaceMember(space, existingID)
	require.Nil(t, appErr)
	mockAPI.AssertNotCalled(t, "PublishWebSocketEvent", "space_member_added", mock.Anything, mock.Anything)
}
