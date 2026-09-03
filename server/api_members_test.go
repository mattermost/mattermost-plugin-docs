// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Handler-level coverage for DELETE .../members/{user} against an admin target. The escalation
// guard and last-admin invariant are exercised end-to-end through the router here, mirroring
// TestHandler_SetSpaceMemberPermissions_LastAdminConflict/_OtherAdminAllowsDemote in
// api_handler_test.go; TestHandler_RemoveSpaceMember* there covers the plain-member paths.
package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// TestHandler_RemoveSpaceMember_AdminTargetRequiresAdminOrSysadmin verifies a manage-tier caller
// (team manage_space, no channel admin_space, not sysadmin) is refused removing a SchemeAdmin
// target.
func TestHandler_RemoveSpaceMember_AdminTargetRequiresAdminOrSysadmin(t *testing.T) {
	channelID := mmmodel.NewId()
	targetAdminID := mmmodel.NewId()
	callerID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSpaceManage(mockAPI, callerID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, channelID)
	// The escalation guard reads the target's and caller's flags from the master DB.
	testutil.MustAddChannelAdmin(t, h.db, channelID, targetAdminID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, targetAdminID, 0)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetAdminID, callerID, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", channelID, targetAdminID)
}

// TestHandler_RemoveSpaceMember_LastAdminConflict verifies removing the space's sole authorized
// admin is rejected with 409, even for a sysadmin caller.
func TestHandler_RemoveSpaceMember_LastAdminConflict(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	// A sysadmin rather than a space admin: the escalation guard reads SchemeAdmin from the
	// master, so a space-admin caller would need a ChannelMembers row of its own — and that row
	// would itself be the "other admin" this scenario must not have.
	grantSysadmin(mockAPI, adminID)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, channelID)
	testutil.MustAddChannelAdmin(t, h.db, channelID, targetUserID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, targetUserID, 0)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID, adminID, nil)
	require.Equal(t, http.StatusConflict, rec.Code)

	var conflict struct {
		Error       *mmmodel.AppError `json:"error"`
		CurrentPage *model.Page       `json:"current_page"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.NotNil(t, conflict.Error)
	require.Equal(t, "app.space.member.last_admin.app_error", conflict.Error.Id)
	require.Nil(t, conflict.CurrentPage)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", channelID, targetUserID)
}

// TestHandler_RemoveSpaceMember_OtherAdminAllowsRemoval is the positive counterpart: removing one
// of two admins succeeds once another reachable admin remains.
func TestHandler_RemoveSpaceMember_OtherAdminAllowsRemoval(t *testing.T) {
	channelID := mmmodel.NewId()
	targetUserID := mmmodel.NewId()
	otherAdminID := mmmodel.NewId()
	adminID := mmmodel.NewId()

	mockAPI := newEnabledMockAPI()
	grantSysadmin(mockAPI, adminID)
	mockAPI.On("DeleteChannelMember", channelID, targetUserID).Return(nil)
	h := openTestPlugin(t, mockAPI)
	space := seedSpace(t, h.store, channelID)
	testutil.MustAddChannelAdmin(t, h.db, channelID, targetUserID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, targetUserID, 0)
	testutil.MustAddChannelAdmin(t, h.db, channelID, otherAdminID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, otherAdminID, 0)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/members/"+targetUserID, adminID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	mockAPI.AssertCalled(t, "DeleteChannelMember", channelID, targetUserID)
}
