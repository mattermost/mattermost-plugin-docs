//go:build e2e

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pluginAPIBase is the Docs plugin's own HTTP route prefix. It is served directly off the server
// root (<siteUrl>/plugins/<id>/api/v1/...), not under Client4's /api/v4 prefix, so none of
// Client4's exported Do* helpers (which all prepend APIURL) can address it directly.
const pluginAPIBase = "/plugins/" + pluginID + "/api/v1"

const actorPassword = "E2e-actor-pass1!" // #nosec G101 -- test-only fixture password, not a credential

// doPluginRequest issues an HTTP request against the Docs plugin's routes and decodes a non-empty
// JSON response body into out (when non-nil). It wraps Client4's lowest-level exported primitives
// (URL, AuthToken, AuthType, HTTPClient) directly — the same primitives Client4's own
// DoAPIRequestWithHeaders uses internally, minus the APIURL prefix that does not apply here. The
// raw response body is always returned too, so a caller can assert on it directly (e.g. comparing
// two error bodies byte-for-byte).
func doPluginRequest(ctx context.Context, client *mmmodel.Client4, method, path string, body, out any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(client.URL, "/")+pluginAPIBase+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if client.AuthToken != "" {
		req.Header.Set("Authorization", client.AuthType+" "+client.AuthToken)
	}

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, respBody, fmt.Errorf("failed to decode response body %q: %w", respBody, err)
		}
	}
	return resp.StatusCode, respBody, nil
}

// actor is a logged-in team member driving requests against the plugin.
type actor struct {
	id     string
	client *mmmodel.Client4
}

// createActor creates a real user, adds it to teamID, and logs it in.
func createActor(t *testing.T, ctx context.Context, env *testEnv, teamID, username string) actor {
	t.Helper()

	user := &mmmodel.User{
		Username: username,
		Email:    username + "@example.com",
		Password: actorPassword,
	}
	created, _, err := env.adminClient.CreateUser(ctx, user)
	require.NoError(t, err, "failed to create actor %s", username)

	_, _, err = env.adminClient.AddTeamMember(ctx, teamID, created.Id)
	require.NoError(t, err, "failed to add actor %s to team", username)

	client := mmmodel.NewAPIv4Client(env.baseURL)
	_, _, err = client.Login(ctx, username, actorPassword)
	require.NoError(t, err, "failed to log in actor %s", username)

	return actor{id: created.Id, client: client}
}

// addSpaceMember adds userID as a plain (default-only) space member via the space admin actor,
// failing loudly if the add did not round-trip with the expected 201.
func addSpaceMember(t *testing.T, ctx context.Context, admin actor, spaceID, userID string) {
	t.Helper()
	status, body, err := doPluginRequest(ctx, admin.client, http.MethodPost, "/spaces/"+spaceID+"/members",
		map[string]string{"user_id": userID}, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status, "add member %s to space %s: %s", userID, spaceID, body)
}

// spaceMembersResponse is the paginated member-list shape handleGetSpaceMembers writes (see
// paginatedResponse in server/api.go) — redeclared here since that type is unexported.
type spaceMembersResponse struct {
	Items   []*spaceMemberJSON `json:"items"`
	HasMore bool               `json:"has_more"`
}

// spaceMemberJSON is a minimal decode target for the member-list response: the identity, plus the
// three fields the route's two projections differ on.
type spaceMemberJSON struct {
	UserId             string   `json:"user_id"`
	Permissions        []string `json:"permissions"`
	GrantedPermissions []string `json:"granted_permissions"`
	IsAdmin            bool     `json:"is_admin"`
}

// listSpaceMembersAs reads spaceID's roster as who, requiring the 200 the route answers every
// reader of the space with, and returns the decoded page. what names the call site in a failure.
func listSpaceMembersAs(t *testing.T, ctx context.Context, who actor, spaceID, what string) spaceMembersResponse {
	t.Helper()
	var resp spaceMembersResponse
	status, body, err := doPluginRequest(ctx, who.client, http.MethodGet, "/spaces/"+spaceID+"/members", nil, &resp)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "%s: %s", what, body)
	require.False(t, resp.HasMore, "%s: the roster runs past one page, so this read is partial", what)
	return resp
}

// requireRedactedRoster asserts the projection a caller without the manage tier receives: the
// roster itself, with every per-member permission field emptied. A 403 used to carry that
// authority boundary; the redaction carries it now, so asserting only the 200 would leave the
// permission matrix — the part worth protecting — untested.
func requireRedactedRoster(t *testing.T, ctx context.Context, who actor, spaceID, what string) {
	t.Helper()
	resp := listSpaceMembersAs(t, ctx, who, spaceID, what)
	// Without this the loop below passes vacuously on a roster that came back empty for an
	// unrelated reason, which is the one way this assertion could silently stop testing anything.
	require.NotEmpty(t, resp.Items, "%s: the roster came back empty, so the redaction below proves nothing", what)
	for _, m := range resp.Items {
		require.Empty(t, m.Permissions, "%s: member %s's effective permissions reached a caller without the manage tier", what, m.UserId)
		require.Empty(t, m.GrantedPermissions, "%s: member %s's granted permissions reached a caller without the manage tier", what, m.UserId)
	}
}

// spaceHasMember reports whether userID currently appears in spaceID's member list, as resolved
// by the space admin actor.
func spaceHasMember(t *testing.T, ctx context.Context, admin actor, spaceID, userID string) bool {
	t.Helper()
	var resp spaceMembersResponse
	status, body, err := doPluginRequest(ctx, admin.client, http.MethodGet, "/spaces/"+spaceID+"/members", nil, &resp)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "list members of space %s: %s", spaceID, body)
	// The request asks for the default page, so a member past it would read as absent here. The
	// scenarios use a handful of actors; this fails loudly if one ever outgrows a page.
	require.False(t, resp.HasMore, "space %s holds more members than one page; this helper only reads the first", spaceID)
	for _, m := range resp.Items {
		if m.UserId == userID {
			return true
		}
	}
	return false
}

// deleteSpace deletes spaceID via the space admin actor. Called at the end of TestScenarios via a
// shared t.Cleanup over every space id the scenarios accumulated in spacesToClean.
// deleteSpace removes a space during teardown. A failure is reported and marks the test failed, but
// does not abort the calling goroutine: teardown deletes a list of spaces, and require's FailNow
// would skip every space after the first failure, leaving the rest behind.
func deleteSpace(t *testing.T, ctx context.Context, admin actor, spaceID string) {
	t.Helper()
	status, body, err := doPluginRequest(ctx, admin.client, http.MethodDelete, "/spaces/"+spaceID, nil, nil)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, http.StatusOK, status, "cleanup: delete space %s: %s", spaceID, body)
}

// pageDocBody is a minimal valid page body (see model.Page.IsValid — only size-limited, not
// schema-validated).
const pageDocBody = `{"type":"doc","content":[]}`

// createPageReq builds a minimal valid page-create body.
func createPageReq(title string) map[string]any {
	return map[string]any{"title": title, "body": pageDocBody}
}

// editPageReq builds a minimal valid page-update body; body and search_text must both be present
// together (see PagePatch.IsValid's cross-check).
func editPageReq(baseEditAt int64, searchText string) map[string]any {
	return map[string]any{"base_edit_at": baseEditAt, "body": pageDocBody, "search_text": searchText}
}

// appErrorID extracts the AppError id from a plugin JSON error body, for asserting on the specific
// error rather than just the status code.
func appErrorID(body []byte) string {
	var appErr *mmmodel.AppError
	if !errors.As(mmmodel.AppErrorFromJSON(bytes.NewReader(body)), &appErr) {
		return ""
	}
	return appErr.Id
}
