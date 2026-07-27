//go:build e2e

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
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
		_ = json.Unmarshal(respBody, out)
	}
	return resp.StatusCode, respBody, nil
}

// actor is a logged-in team member driving requests against the plugin.
type actor struct {
	id     string
	client *mmmodel.Client4
}

// createActor creates a real user, adds it to teamID, and logs it in — mirroring
// smoke-scenarios.sh's create_and_join_team + login_actor.
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

// spaceMembersResponse is the paginated member-list shape handleListSpaceMembers writes (see
// paginatedResponse in server/api.go) — redeclared here since that type is unexported.
type spaceMembersResponse struct {
	Items []*spaceMemberJSON `json:"items"`
}

// spaceMemberJSON is a minimal decode target for the member-list response; only user_id is needed
// here.
type spaceMemberJSON struct {
	UserId string `json:"user_id"`
}

// spaceHasMember reports whether userID currently appears in spaceID's member list, as resolved
// by the space admin actor — mirroring smoke-scenarios.sh's space_has_member.
func spaceHasMember(t *testing.T, ctx context.Context, admin actor, spaceID, userID string) bool {
	t.Helper()
	var resp spaceMembersResponse
	status, body, err := doPluginRequest(ctx, admin.client, http.MethodGet, "/spaces/"+spaceID+"/members", nil, &resp)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "list members of space %s: %s", spaceID, body)
	for _, m := range resp.Items {
		if m.UserId == userID {
			return true
		}
	}
	return false
}

// deleteSpace deletes spaceID via the space admin actor. Registered via t.Cleanup by every
// scenario that creates a space.
func deleteSpace(t *testing.T, ctx context.Context, admin actor, spaceID string) {
	t.Helper()
	status, body, err := doPluginRequest(ctx, admin.client, http.MethodDelete, "/spaces/"+spaceID, nil, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "cleanup: delete space %s: %s", spaceID, body)
}

// pageDocBody is a minimal valid page body (see model.Page.IsValid — only size-limited, not
// schema-validated), matching smoke-scenarios.sh's create_page_body/edit_page_body.
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
	var er struct {
		Id string `json:"id"`
	}
	if err := json.Unmarshal(body, &er); err != nil {
		return ""
	}
	return er.Id
}
