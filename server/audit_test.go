// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// mutatingRoutePattern extracts the handler name from every POST/PATCH/DELETE route registration
// in api.go.
var mutatingRoutePattern = regexp.MustCompile(`p\.(handle\w+)\)\.Methods\(http\.Method(?:Post|Patch|Delete)\)`)

// handlerBody returns the source of the named handler, from its declaration to the next top-level
// function.
func handlerBody(t *testing.T, src, name string) string {
	t.Helper()
	marker := "func (p *Plugin) " + name + "("
	start := strings.Index(src, marker)
	require.NotEqual(t, -1, start, "handler %s not found in the api sources", name)
	rest := src[start+len(marker):]
	body, _, _ := strings.Cut(rest, "\nfunc ")
	return body
}

// TestAuditMutatingRouteCoverage is a completeness ratchet: every mutating route's handler must
// build an audit record, so a new POST/PATCH/DELETE route cannot ship unaudited by omission. The
// draft autosave heartbeat is the one pinned exemption (see audit.go); adding a route means either
// wiring makeAuditRecord or deliberately extending the exemption here.
func TestAuditMutatingRouteCoverage(t *testing.T) {
	routerSrc, err := os.ReadFile("api.go")
	require.NoError(t, err)
	matches := mutatingRoutePattern.FindAllStringSubmatch(string(routerSrc), -1)
	require.Len(t, matches, 21, "mutating route count changed; decide whether the new route is audited and update this ratchet")

	files, err := filepath.Glob("api_*.go")
	require.NoError(t, err)
	var sources strings.Builder
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Clean(file))
		require.NoError(t, readErr)
		sources.Write(src)
		sources.WriteString("\n")
	}
	allSrc := sources.String()

	exempt := map[string]bool{"handleUpdatePageDraft": true}
	seen := map[string]bool{}
	for _, match := range matches {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		body := handlerBody(t, allSrc, name)
		if exempt[name] {
			assert.NotContains(t, body, "makeAuditRecord", "%s is the pinned autosave exemption and must stay unaudited", name)
			continue
		}
		assert.Contains(t, body, "makeAuditRecord", "mutating handler %s emits no audit record", name)
	}
}

// TestAuditRecord_CreateSpace pins the shape of a success record: event name, status, actor,
// route parameters, api path, and the result-state snapshot.
func TestAuditRecord_CreateSpace(t *testing.T) {
	mockAPI := newEnabledMockAPI()
	mockAPI.On("GetTeamMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.TeamMember{}, nil)
	mockAPI.On("CreateChannel", mock.AnythingOfType("*model.Channel")).
		Return(&mmmodel.Channel{Id: mmmodel.NewId(), Type: mmmodel.ChannelTypeSpace}, nil)
	mockAPI.On("AddChannelMember", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(&mmmodel.ChannelMember{}, nil)
	h := openTestPlugin(t, mockAPI)

	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	rec := h.do(t, http.MethodPost, "/api/v1/teams/"+teamID+"/spaces", userID, map[string]any{"title": "Audited Space"})
	require.Equal(t, http.StatusCreated, rec.Code)

	records := h.auditRecordsNamed(auditEventCreateSpace)
	require.Len(t, records, 1, "one mutation, one audit record")
	audit := records[0]
	assert.Equal(t, mmmodel.AuditStatusSuccess, audit.Status)
	assert.Equal(t, userID, audit.Actor.UserId)
	assert.Equal(t, teamID, audit.EventData.Parameters["team_id"])
	assert.Equal(t, "/api/v1/teams/"+teamID+"/spaces", audit.Meta[mmmodel.AuditKeyAPIPath])
	assert.Equal(t, "space", audit.EventData.ObjectType)
	assert.NotEmpty(t, audit.EventData.ResultState["id"])
	assert.Equal(t, "Audited Space", audit.EventData.ResultState["title"])
}

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

	rec := h.do(t, http.MethodDelete, "/api/v1/spaces/"+space.Id, userID, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)

	records := h.auditRecordsNamed(auditEventDeleteSpace)
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

// TestAuditRecord_DraftAutosaveNotAudited pins the one exemption at runtime: the autosave
// heartbeat emits nothing, whatever its outcome.
func TestAuditRecord_DraftAutosaveNotAudited(t *testing.T) {
	h := openTestPlugin(t, nil)
	channelID := mmmodel.NewId()
	space := seedSpace(t, h.store, channelID)
	page := seedPage(t, h.store, space.Id, channelID, "")

	h.do(t, http.MethodPatch, "/api/v1/spaces/"+space.Id+"/pages/"+page.Id+"/draft", mmmodel.NewId(),
		map[string]any{"title": "t", "body": `{"type":"doc","content":[]}`})

	h.auditMu.Lock()
	defer h.auditMu.Unlock()
	assert.Empty(t, h.auditRecords, "the autosave heartbeat must not reach the audit log")
}
