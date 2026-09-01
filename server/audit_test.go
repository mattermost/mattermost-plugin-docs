// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// TestAuditRecord_FailedMutationIsRecordedAsFail proves the fail-by-default posture: a request
// refused at the membership gate still leaves a record naming the actor and the resources.
func TestAuditRecord_FailedMutationIsRecordedAsFail(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).
		Return((*mmmodel.ChannelMember)(nil), mmmodel.NewAppError("GetChannelMember", "test.not_member", nil, "", http.StatusNotFound)).Maybe()
	mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil).Maybe()
	h := openTestPlugin(t, mockAPI)

	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	userID := mmmodel.NewId()

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/pages/"+mmmodel.NewId()+"/comments/"+mmmodel.NewId(), userID, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)

	records := h.auditRecordsNamed(auditEventDeletePageComment)
	require.Len(t, records, 1)
	assert.Equal(t, mmmodel.AuditStatusFail, records[0].Status)
	assert.Equal(t, userID, records[0].Actor.UserId)
	assert.Equal(t, space.Id, records[0].EventData.Parameters["space_id"])
}

// TestAuditRecord_CommentResultExcludesMessage pins the Auditable contract on the wire that
// matters most: comment text is user content and must never reach the audit log.
func TestAuditRecord_CommentResultExcludesMessage(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(
		func(post *mmmodel.Post) (*mmmodel.Post, *mmmodel.AppError) {
			created := post.Clone()
			created.Id = mmmodel.NewId()
			now := mmmodel.GetMillis()
			created.CreateAt, created.UpdateAt = now, now
			return created, nil
		}, nil).Once()
	h := openTestPlugin(t, mockAPI)

	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	userID := mmmodel.NewId()

	rec := h.do(t, http.MethodPost, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/comments", userID,
		map[string]any{"message": "confidential comment text"})
	require.Equal(t, http.StatusCreated, rec.Code)

	records := h.auditRecordsNamed(auditEventCreatePageComment)
	require.Len(t, records, 1)
	audit := records[0]
	assert.Equal(t, mmmodel.AuditStatusSuccess, audit.Status)
	assert.Equal(t, "page_comment", audit.EventData.ObjectType)
	assert.NotEmpty(t, audit.EventData.ResultState["id"])
	assert.NotContains(t, audit.EventData.ResultState, "message")
	for _, value := range audit.EventData.ResultState {
		assert.NotEqual(t, "confidential comment text", value)
	}
}

// TestAuditRecord_DeletePageComment covers a delete: no result state, but the route ids identify
// what was destroyed.
func TestAuditRecord_DeletePageComment(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetChannelMember", mock.Anything, mock.Anything).Return(&mmmodel.ChannelMember{}, nil)
	mockAPI.On("GetTeamMember", mock.Anything, mock.Anything).Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("DeletePost", mock.AnythingOfType("string")).Return(nil).Once()
	h := openTestPlugin(t, mockAPI)

	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")
	author := mmmodel.NewId()
	root := seedCommentPost(t, h, channelID, page.Id, "", author, 1000)

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/comments/"+root.Id, author, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	records := h.auditRecordsNamed(auditEventDeletePageComment)
	require.Len(t, records, 1)
	audit := records[0]
	assert.Equal(t, mmmodel.AuditStatusSuccess, audit.Status)
	assert.Equal(t, space.Id, audit.EventData.Parameters["space_id"])
	assert.Equal(t, page.Id, audit.EventData.Parameters["page_id"])
	assert.Equal(t, root.Id, audit.EventData.Parameters["comment_id"])
}
