// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// stubNonMember denies userID every backing-channel page permission, so the read resolver admits
// them through the open-space team fall-through rather than as a member — the only admission
// auto-join acts on. Must run before the harness constructor: mock.Mock matches expectations in
// registration order, and StubDefaultSpacePermissions' catch-all otherwise grants read_page first.
func stubNonMember(mockAPI *plugintest.API, userID string) {
	for _, p := range []*mmmodel.Permission{
		mmmodel.PermissionReadPage, mmmodel.PermissionCreatePage, mmmodel.PermissionCommentPage,
		mmmodel.PermissionEditPage, mmmodel.PermissionDeleteOwnPage, mmmodel.PermissionDeletePage,
	} {
		mockAPI.On("HasPermissionToChannel", userID, mock.Anything, p).Return(false).Maybe()
	}
}

// autoJoinHarness seeds one open space whose backing channel resolves to the contribute preset,
// and returns the harness plus that space. The caller stubs whichever of GetChannelMember /
// AddChannelMember its scenario reaches.
func autoJoinHarness(t *testing.T, mockAPI *plugintest.API, viewAccess model.ViewAccess) (*testHarness, *model.Space) {
	t.Helper()
	h := openTestServiceWithAPI(t, mockAPI)
	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := testutil.MustCreateSpace(t, h.store, channelID, mmmodel.NewId())
	if space.ViewAccess != viewAccess {
		updated, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &viewAccess}, space.UpdateAt, false)
		require.NoError(t, err)
		space = updated
	}
	return h, space
}

// readOnlyJoinHarness seeds one open space on the read-only preset, whose defaults confer nothing
// beyond the baseline read — the set JoinOpenSpace refuses to create a membership for.
func readOnlyJoinHarness(t *testing.T, mockAPI *plugintest.API) (*testHarness, *model.Space) {
	t.Helper()
	h := openTestServiceWithAPI(t, mockAPI)
	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceReadOnly)
	return h, testutil.MustCreateSpace(t, h.store, channelID, mmmodel.NewId())
}

// TestJoinOpenSpace_MemberIsNoOp covers idempotency at the admission boundary: a caller whose read
// already resolves through a membership is answered with server-resolved access rather than joined a
// second time, so a client that cannot tell whether it has joined may simply call it.
func TestJoinOpenSpace_MemberIsNoOp(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Deliberately no stubNonMember: the harness's default grants admit this caller as a member, so
	// the read resolves through the membership rather than the open-space fall-through.
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)
	mockAPI.On("GetChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil).Maybe()

	access, appErr := h.svc.JoinOpenSpace(space, userID)
	require.Nil(t, appErr)
	require.NotNil(t, access)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_JoinsWhenDefaultGrants covers the success path of the explicit self-join step that
// the client runs ahead of a write. It can add the caller to the backing channel so the later gate
// passes. A non-member admitted via the open-space fall-through, whose space default grants the
// permission, is added to the channel and the membership-added event is published.
func TestAutoJoin_JoinsWhenDefaultGrants(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// Not yet a member: no ChannelMembers row is seeded, so the master-side membership probe
	// misses and the join path runs.
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	// The record held by the caller can be older than the row re-read under the membership lock.
	// The response must project the locked row rather than replay metadata from that stale snapshot.
	stale := *space
	currentTitle := "Current title"
	fresh, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{Title: &currentTitle}, space.UpdateAt, false)
	require.NoError(t, err)

	access, appErr := h.svc.JoinOpenSpace(&stale, userID)
	require.Nil(t, appErr)
	require.Equal(t, fresh.Title, access.Title)
	require.Equal(t, fresh.UpdateAt, access.UpdateAt)
	mockAPI.AssertCalled(t, "AddChannelMember", space.ChannelId, userID)
}

// TestAutoJoin_ProvenanceMarkerLifecycle covers the auto-join provenance marker end to end: set on
// a successful auto-join and visible through GetSpaceMembers, then cleared by a deliberate admin
// permission change on the same member (SetSpaceMemberPermissions), which must supersede it.
func TestAutoJoin_ProvenanceMarkerLifecycle(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.Nil(t, appErr)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: userID}}, nil)

	members, _, appErr := h.svc.GetSpaceMembers(space, 0, 60, true)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.True(t, members[0].IsAutoJoined, "a member added by the auto-join pre-step must be marked auto-joined")

	// A deliberate admin act on this member's permissions supersedes how they got here. The
	// last-admin/target read is master-backed, not the plugin API's GetChannelMember, so seed the
	// stand-in row directly (the auto-join above only ran through the mocked pluginapi call, which
	// does not write this table).
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)

	_, appErr = h.svc.SetSpaceMemberPermissions(space, userID, []string{mmmodel.PermissionCommentPage.Id}, mmmodel.NewId())
	require.Nil(t, appErr)

	members, _, appErr = h.svc.GetSpaceMembers(space, 0, 60, true)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.False(t, members[0].IsAutoJoined, "a deliberate admin permission change must clear the auto-join marker")
}

// TestAutoJoin_ProvenanceMarkerFailureAddsNoMembership pins the write order: marking first means a
// marker-write failure can only leave a marker with no membership, which grants nothing, because
// authority comes from channel membership and roles rather than from this table. The reverse order
// would leave a membership no review can attribute.
//
// The assertion that carries this is AssertNotCalled on the add: restoring the add-then-mark order
// makes the add happen, so this test fails rather than merely reporting a different error.
func TestAutoJoin_ProvenanceMarkerFailureAddsNoMembership(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// Stubbed so an add would succeed if it were reached: the point is that it is not reached, and
	// an unstubbed call would fail as a panic rather than as the assertion below.
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil).Maybe()

	// The marker table is the only thing dropped, so every read ahead of the marker write still
	// answers and the join reaches exactly the step under test.
	_, dbErr := h.db.Exec(`DROP TABLE DOCS_SpaceAutoJoin`)
	require.NoError(t, dbErr)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.NotNil(t, appErr, "a marker write that failed must fail the join rather than proceed untracked")
	require.Equal(t, "app.space.auto_join.provenance_write_failed.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "AddChannelMember", space.ChannelId, userID)
}

// TestRequireSpacePublish_NonMemberIsRefusedNotJoined pins the property that purifying the write
// gates bought. A non-member reading an open space whose default grants create_page is refused —
// not joined into being allowed. The membership they need comes from JoinOpenSpace, which they ask
// for; never from a gate that was only asked whether they may write.
//
// The refusal must also be the shared existence-hiding 403, so it stays indistinguishable from the
// one a space they may not see would give them.
func TestRequireSpacePublish_NonMemberIsRefusedNotJoined(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Registered before the harness so it beats StubDefaultSpacePermissions' catch-all: no
	// backing-channel permission is ever reported for this user, which is what forces the
	// fall-through read.
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	appErr := h.svc.RequireSpacePublish("test", space, userID, true)

	require.NotNil(t, appErr, "a non-member must be refused rather than admitted")
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id,
		"the refusal must be the shared existence-hiding 403, not a distinguishable one")

	// The gate wrote nothing: no membership, and so no provenance describing one.
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
	marked, err := h.store.GetAutoJoinedIDs(space.Id)
	require.NoError(t, err)
	require.Empty(t, marked)
}

// TestPruneSelfJoinedMembers_RemovesOnlySelfJoined pins the discrimination the whole prune rests
// on: making a space private withdraws the memberships that were held on its open access alone, and
// leaves every deliberately added member in place.
//
// Both members are plain SchemeUser rows with no explicit roles — byte-identical on the membership
// itself — so nothing but the provenance marker can tell them apart. That is what the marker table
// is for, and this is the test that would fail if it were removed.
func TestPruneSelfJoinedMembers_RemovesOnlySelfJoined(t *testing.T) {
	mockAPI := &plugintest.API{}
	selfJoined := mmmodel.NewId()
	invited := mmmodel.NewId()
	stubNonMember(mockAPI, selfJoined)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("AddChannelMember", space.ChannelId, selfJoined).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: selfJoined}, nil)
	mockAPI.On("DeleteChannelMember", space.ChannelId, mock.Anything).Return(nil).Maybe()

	// One member of each kind: one who joined through the open access, one added deliberately.
	_, appErr := h.svc.JoinOpenSpace(space, selfJoined)
	require.Nil(t, appErr)
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, invited)

	removed, appErr := h.svc.PruneSelfJoinedMembers(space)
	require.Nil(t, appErr)
	require.Equal(t, []string{selfJoined}, removed)

	mockAPI.AssertCalled(t, "DeleteChannelMember", space.ChannelId, selfJoined)
	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, invited)

	// The marker goes with the membership, so a later re-join is recorded afresh rather than
	// inheriting a marker describing a membership that no longer exists.
	marked, err := h.store.GetAutoJoinedIDs(space.Id)
	require.NoError(t, err)
	require.Empty(t, marked)
}

// TestAddSpaceMember_ClearsProvenanceMarker covers the second clearing path: a deliberate admin add
// of a member the auto-join pre-step brought in supersedes how they got here, so the marker must go
// and the membership must stop reporting as auto-joined.
func TestAddSpaceMember_ClearsProvenanceMarker(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.Nil(t, appErr)

	// Added deliberately on top of the auto-join: an existing member is a no-op for core, so what
	// this exercises is the clear that rides with the add.
	_, appErr = h.svc.AddSpaceMember(space, userID)
	require.Nil(t, appErr)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: userID}}, nil)
	members, _, appErr := h.svc.GetSpaceMembers(space, 0, 60, true)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.False(t, members[0].IsAutoJoined,
		"a deliberate admin add must clear the auto-join marker rather than leave it standing")
}

// TestAddSpaceMember_FailedAddPreservesProvenanceMarker pins the mutation ordering: an admin add
// only makes a self-joined membership deliberate if core accepts the add. A failed add must leave
// the marker available for a later open-to-private prune.
func TestAddSpaceMember_FailedAddPreservesProvenanceMarker(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	addErr := mmmodel.NewAppError("AddChannelMember", "test.add.failed", nil, "", http.StatusInternalServerError)
	// Registered before the harness's catch-all expectations so this target-specific failure wins.
	mockAPI.On("AddChannelMember", mock.Anything, userID).Return((*mmmodel.ChannelMember)(nil), addErr).Once()
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)
	require.NoError(t, h.store.MarkAutoJoined(space.Id, userID))

	member, appErr := h.svc.AddSpaceMember(space, userID)
	require.Nil(t, member)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.add_member.failed.app_error", appErr.Id)

	marked, err := h.store.GetAutoJoinedIDs(space.Id)
	require.NoError(t, err)
	require.Equal(t, []string{userID}, marked)
}

// TestSetSpaceMemberPermissions_FailedUpdatePreservesProvenanceMarker is the role-update half of
// the same ordering contract. A rejected core mutation must not turn a self-joined membership into
// a deliberate one in the plugin's provenance table.
func TestSetSpaceMemberPermissions_FailedUpdatePreservesProvenanceMarker(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	updateErr := mmmodel.NewAppError("UpdateChannelMemberRoles", "test.update.failed", nil, "", http.StatusInternalServerError)
	mockAPI.On("UpdateChannelMemberRoles", mock.Anything, userID, mock.Anything).
		Return((*mmmodel.ChannelMember)(nil), updateErr).Once()
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)
	require.NoError(t, h.store.MarkAutoJoined(space.Id, userID))

	member, appErr := h.svc.SetSpaceMemberPermissions(
		space,
		userID,
		[]string{mmmodel.PermissionCommentPage.Id},
		mmmodel.NewId(),
	)
	require.Nil(t, member)
	require.NotNil(t, appErr)
	require.Equal(t, "app.space.member.update_permissions_failed.app_error", appErr.Id)

	marked, err := h.store.GetAutoJoinedIDs(space.Id)
	require.NoError(t, err)
	require.Equal(t, []string{userID}, marked)
}

// TestRemoveSpaceMember_ClearsProvenanceMarker covers the third clearing path: removing an
// auto-joined member must clear their marker too, so a later deliberate re-add of the same user is
// not misreported as auto-joined.
func TestRemoveSpaceMember_ClearsProvenanceMarker(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	otherID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.Nil(t, appErr)

	// The target lookup and the reachability guard both read membership from the master DB; the
	// auto-join above only ran through the mocked pluginapi call, which does not write this table, so
	// userID's own row must be seeded too, alongside another active member so removing userID does
	// not trip the last-member guard.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, otherID)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, otherID, 0)
	mockAPI.On("DeleteChannelMember", space.ChannelId, userID).Return(nil)

	appErr = h.svc.RemoveSpaceMember(space, userID, mmmodel.NewId())
	require.Nil(t, appErr)

	// Re-added deliberately: if RemoveSpaceMember left the marker behind, this would incorrectly
	// resurface as auto-joined.
	_, appErr = h.svc.AddSpaceMember(space, userID)
	require.Nil(t, appErr)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: userID}}, nil)
	members, _, appErr := h.svc.GetSpaceMembers(space, 0, 60, true)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.False(t, members[0].IsAutoJoined,
		"RemoveSpaceMember must clear the provenance marker rather than leave it for a later deliberate add")
}

// TestJoinOpenSpace_ReadOnlyDefaultsRefused covers the admission test: the fall-through alone never joins.
// A space whose defaults confer nothing beyond the read every reader already has refuses the join
// outright: creating a membership that grants exactly what the caller had without one is not a
// no-op to be papered over, it is a request that cannot be satisfied.
func TestJoinOpenSpace_ReadOnlyDefaultsRefused(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := readOnlyJoinHarness(t, mockAPI)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.join.nothing_to_grant.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_PrivateFlipAbortsJoin is the concurrency guard: the admitting read happened against
// an open space, but the space is private by the time the pre-step re-reads it under the lock. The
// stale admission must not survive that flip — re-validation, not the caller's resolution, decides.
func TestAutoJoin_PrivateFlipAbortsJoin(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// The seeded contribute scheme already grants create_page, so the re-validation below is the
	// only thing that can abort the join — the pre-step's own admission test must not be what makes
	// this pass.

	// The caller still holds the open-space record it was admitted against; the stored row has
	// since flipped private, which is what the pre-step re-reads.
	stale := *space
	private := model.ViewAccessPrivate
	_, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &private}, space.UpdateAt, false)
	require.NoError(t, err)

	access, appErr := h.svc.JoinOpenSpace(&stale, userID)
	require.Nil(t, access)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestJoinOpenSpace_DeletedSpaceIsHidden covers the space-vanished branch. Unlike the pre-step this
// replaced — which ran inside someone else's write and could only decline to join — this is the
// caller's own request, so it answers. A space deleted under it must report as the shared
// existence-hiding 403, indistinguishable from one they were never allowed to see.
func TestJoinOpenSpace_DeletedSpaceIsHidden(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	require.NoError(t, h.store.DeleteSpace(space.Id))

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestResolveSpaceRemovePage_NeverJoins pins the property that made the delete gate safe to
// simplify: resolving a delete-class operation is a question, so a non-member whose space default
// would grant them the permission is still refused rather than joined into being able to answer it.
// Membership is created only by JoinOpenSpace, which the caller asks for.
func TestResolveSpaceRemovePage_NeverJoins(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// Owner matches and the contribute default grants delete_own_page, so under the pre-step this
	// call would have joined the caller. It must now simply deny.
	ownOnly, admitted, appErr := h.svc.ResolveSpaceRemovePage(space, userID,
		"api.page.delete", "api.page.delete_own", true)
	require.Nil(t, appErr)
	require.False(t, admitted)
	require.False(t, ownOnly)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_AlreadyMemberIsNoOp covers idempotency: an existing member is not re-added, so a
// repeated write by an already-joined caller does not churn membership or republish the event.
func TestAutoJoin_AlreadyMemberIsNoOp(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// The membership already exists on the master DB. No GetChannelMember stub: the probe must
	// read the master through the plugin store, never the replica-backed plugin API — a stale
	// replica miss here would adopt this existing membership as a fresh join, and the undo paired
	// with it would then remove a membership the caller already held.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.Nil(t, appErr)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestRequireSpaceDraftWrite_LookupFailureIsNotADenial pins the error discipline of the two-attempt
// draft gate: a failure of the create_page check must surface as itself, not be retried into the
// edit_page attempt and reported as a 403 — a backend outage must never read as "not authorized".
//
// Driven through the guest clamp, which is the one fallible lookup left inside a write gate now
// that the gates no longer resolve a space's defaults.
func TestRequireSpaceDraftWrite_LookupFailureIsNotADenial(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Registered before the harness, whose constructor installs a catch-all GetUser returning an
	// ordinary user: mock.Mock matches expectations in registration order, so a later stub for the
	// same call is never reached.
	mockAPI.On("GetUser", userID).
		Return((*mmmodel.User)(nil), &mmmodel.AppError{StatusCode: 500, Id: "user.lookup.failed"})

	// No stubNonMember: the caller must clear the channel-permission check so the guest clamp
	// behind it is reached at all.
	h := openTestServiceWithAPI(t, mockAPI)
	channelID := mmmodel.NewId()
	testutil.MustSeedChannelScheme(t, mockAPI, channelID, mmmodel.SchemeNameSpaceContribute)
	space := testutil.MustCreateSpace(t, h.store, channelID, mmmodel.NewId())

	appErr := h.svc.RequireSpaceDraftWrite("test", space, userID)

	require.NotNil(t, appErr)
	require.NotEqual(t, http.StatusForbidden, appErr.StatusCode,
		"a failed lookup must not be reported as an authorization denial")
	// One attempt, not two: the create_page failure short-circuits instead of falling through to
	// the edit_page retry, which would read the user a second time.
	mockAPI.AssertNumberOfCalls(t, "GetUser", 1)
}

// TestRequireSpaceDraftWrite_EitherPermissionAdmits pins the OR-semantics of the two-attempt draft
// gate: holding either create_page or edit_page is enough, and holding neither is refused. The
// edit-only case is the one that matters — edit_page without create_page is an ordinary permission
// grant, so dropping the fallback would silently revoke draft access from every editor-only member
// while every other test in the suite still passed.
func TestRequireSpaceDraftWrite_EitherPermissionAdmits(t *testing.T) {
	for name, granted := range map[string]*mmmodel.Permission{
		"edit_page only":   mmmodel.PermissionEditPage,
		"create_page only": mmmodel.PermissionCreatePage,
	} {
		t.Run(name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			userID := mmmodel.NewId()
			channelID := mmmodel.NewId()
			// Registered before the harness so these decide, not its catch-alls.
			for _, p := range []*mmmodel.Permission{mmmodel.PermissionCreatePage, mmmodel.PermissionEditPage} {
				mockAPI.On("HasPermissionToChannel", userID, channelID, p).Return(p.Id == granted.Id).Maybe()
			}
			h := openTestServiceWithAPI(t, mockAPI)
			space := testutil.MustCreateSpace(t, h.store, channelID, mmmodel.NewId())

			appErr := h.svc.RequireSpaceDraftWrite("test", space, userID)
			require.Nil(t, appErr, "%s must admit a draft write", granted.Id)
		})
	}

	t.Run("neither permission is refused", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		userID := mmmodel.NewId()
		channelID := mmmodel.NewId()
		for _, p := range []*mmmodel.Permission{mmmodel.PermissionCreatePage, mmmodel.PermissionEditPage} {
			mockAPI.On("HasPermissionToChannel", userID, channelID, p).Return(false).Maybe()
		}
		h := openTestServiceWithAPI(t, mockAPI)
		space := testutil.MustCreateSpace(t, h.store, channelID, mmmodel.NewId())

		appErr := h.svc.RequireSpaceDraftWrite("test", space, userID)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	})
}

// TestResolveSpaceRemovePage_CheckFailureIsNotADenial pins the same error discipline on the
// remove-page gate that TestRequireSpaceDraftWrite_LookupFailureIsNotADenial pins on the draft gate:
// a failure of either attempt surfaces as itself.
//
// The distinction the gate draws is between "neither permission grants this" — reported as
// admitted=false with no error, which the caller turns into its own 403 — and "the check could not
// be carried out", which must reach the caller as the failure it is.
//
// Both ownerMatches values are covered because an owner match is what opens the second attempt,
// and the failure must not be converted into a grant by it. Only the any-attempt's guard is
// exercised either way: both attempts resolve through the same client, so an any-attempt that
// reached a verdict at all guarantees the own-attempt can only deny or grant.
func TestResolveSpaceRemovePage_CheckFailureIsNotADenial(t *testing.T) {
	for name, ownerMatches := range map[string]bool{
		"owner does not match": false,
		"owner matches":        true,
	} {
		t.Run(name, func(t *testing.T) {
			h := openTestService(t)
			// An unwired client makes both permission attempts fail as a 500 rather than deny.
			h.svc = app.New(h.store, nil, nil)
			space := testutil.MustCreateSpace(t, h.store, mmmodel.NewId(), mmmodel.NewId())

			ownOnly, admitted, appErr := h.svc.ResolveSpaceRemovePage(
				space, mmmodel.NewId(), "any", "own", ownerMatches)

			require.NotNil(t, appErr, "a failed check must not be reported as an ordinary denial")
			require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
			require.False(t, admitted)
			require.False(t, ownOnly)
		})
	}
}

// TestSpaceFallthroughAdmitsReadOnly is the escalation guard on the open-space fall-through: it
// exists to admit reads, and must never admit a write. A non-member who has not used the explicit
// self-join route reaches the gate still holding only the fall-through, and every write permission
// has to be refused there. Without the read-permission condition on that branch the fall-through
// would grant create/edit/delete outright, since the caller already holds the team
// read_public_channel the branch keys on.
func TestSpaceFallthroughAdmitsReadOnly(t *testing.T) {
	for _, perm := range []*mmmodel.Permission{
		mmmodel.PermissionCreatePage, mmmodel.PermissionEditPage,
		mmmodel.PermissionDeletePage, mmmodel.PermissionDeleteOwnPage,
	} {
		t.Run(perm.Id, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			userID := mmmodel.NewId()
			stubNonMember(mockAPI, userID)
			h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)
			// The write gate does not join the caller, which is the premise of the assertion below.

			appErr := h.svc.RequireSpacePageWrite("test", space, userID, perm)

			require.NotNil(t, appErr, "%s must not be admitted by the open-space read fall-through", perm.Id)
			require.Equal(t, http.StatusForbidden, appErr.StatusCode)
		})
	}
}

// TestSpaceFallthroughAdmitsRead is the positive half of the pair above: the very same non-member
// does get read_page, so the refusals there come from the permission being a write, not from the
// fall-through being closed altogether.
func TestSpaceFallthroughAdmitsRead(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	appErr := h.svc.RequireSpacePagePermission("test", space, userID, mmmodel.PermissionReadPage)

	require.Nil(t, appErr)
}

// TestRequireSpacePagePermission_FormerTeamMemberDenied verifies the same team-membership guard
// evaluatePagePermission applies as readResolutionFrom (see
// TestResolveSpaceRead_FormerTeamMemberDenied): a user who left the space's team is denied even
// though their backing-channel ChannelMember row still exists, because active is false and the
// `active &&` conjunct on the channel-permission check short-circuits before it is ever consulted.
func TestRequireSpacePagePermission_FormerTeamMemberDenied(t *testing.T) {
	mockAPI := &plugintest.API{}
	teamID := mmmodel.NewId()
	userID := mmmodel.NewId()
	// Registered before the harness, whose GetTeamMember catch-all returns an active membership:
	// mock.Mock matches expectations in registration order.
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: userID, DeleteAt: 1}, nil)
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)

	appErr := h.svc.RequireSpacePagePermission("test", space, userID, mmmodel.PermissionReadPage)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	// The team gate blocks before any channel-scoped permission is consulted, so the lingering
	// ChannelMember row is irrelevant.
	mockAPI.AssertNotCalled(t, "HasPermissionToChannel", mock.Anything, mock.Anything, mock.Anything)
}

// TestResolveSpaceRead_ComplianceModeSuppressesOpenFallthrough verifies that the open-space
// non-member read fall-through is suppressed under ComplianceSettings.Enable, even though the
// caller holds the team read_public_channel grant hasOpenTeamFallthrough otherwise admits on.
func TestResolveSpaceRead_ComplianceModeSuppressesOpenFallthrough(t *testing.T) {
	mockAPI := &plugintest.API{}
	strangerID := mmmodel.NewId()
	// Registered before the harness so it takes precedence over StubDefaultSpacePermissions'
	// permissive catch-all: mock.Mock matches expectations in registration order.
	mockAPI.On("HasPermissionToChannel", strangerID, mock.Anything, mmmodel.PermissionReadPage).Return(false)
	mockAPI.On("GetConfig").
		Return(&mmmodel.Config{ComplianceSettings: mmmodel.ComplianceSettings{Enable: mmmodel.NewPointer(true)}}).Once()
	h := openTestServiceWithAPI(t, mockAPI)
	// Asserted on this mock — the one wired into the service — so the test fails if the
	// resolution stops consulting the config at all.
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })

	// The default space fixture is open, and the harness stubs GetTeamMember to an active
	// membership, so the stranger clears the team gate and reaches the open-space fall-through.
	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), mmmodel.NewId())

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, strangerID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution,
		"compliance mode must suppress the open-space non-member fall-through even though the caller holds read_public_channel")
}

// TestResolveSpaceRead_UnreadableConfigSuppressesOpenFallthrough covers the fail-closed direction of
// the compliance check: a config that cannot be read at all suppresses the open-space non-member
// fall-through rather than admitting it. Reading an absent config as "compliance off" would widen
// access on exactly the signal that says the access policy is unknown.
func TestResolveSpaceRead_UnreadableConfigSuppressesOpenFallthrough(t *testing.T) {
	mockAPI := &plugintest.API{}
	strangerID := mmmodel.NewId()
	// Both registered before the harness, whose catch-alls would otherwise answer first: mock.Mock
	// matches expectations in registration order.
	mockAPI.On("HasPermissionToChannel", strangerID, mock.Anything, mmmodel.PermissionReadPage).Return(false)
	mockAPI.On("GetConfig").Return((*mmmodel.Config)(nil)).Once()
	h := openTestServiceWithAPI(t, mockAPI)
	// Asserted on this mock — the one wired into the service — so the test fails if the
	// resolution stops consulting the config at all.
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })

	// The default space fixture is open, and the harness stubs GetTeamMember to an active
	// membership, so the stranger clears the team gate and reaches the open-space fall-through.
	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), mmmodel.NewId())

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, strangerID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution,
		"an unreadable config must suppress the open-space non-member fall-through, not admit it")
}

// teamManagerHarness seeds a space with the given view access whose acting user holds team
// manage_space but is not a member of the backing channel — exactly the caller
// RequireSpaceAdminOrTeamPerm's read-gate conjunct exists to constrain.
func teamManagerHarness(t *testing.T, viewAccess model.ViewAccess) (*testHarness, *model.Space, string) {
	t.Helper()
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	// Both registered before the harness, whose catch-alls would otherwise answer first:
	// mock.Mock matches expectations in registration order.
	stubNonMember(mockAPI, userID)
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionAdminSpace).Return(false).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionManageSpace).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)
	if space.ViewAccess != viewAccess {
		updated, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &viewAccess}, space.UpdateAt, false)
		require.NoError(t, err)
		space = updated
	}
	return h, space, userID
}

// TestRequireSpaceAdminOrTeamPerm_TeamPermNeedsReadOnPrivateSpace pins the read-gate conjunct on the
// team-permission branch: a team-wide manage_space grant authorizes managing only spaces the caller
// can already read. Here the caller holds manage_space on the team but is not a backing-channel
// member of a private space, so the read resolver denies them and the team grant must not admit
// them anyway. Without the conjunct, any team manage_space holder could patch or delete every
// private space in the team, including ones they cannot open.
func TestRequireSpaceAdminOrTeamPerm_TeamPermNeedsReadOnPrivateSpace(t *testing.T) {
	h, space, userID := teamManagerHarness(t, model.ViewAccessPrivate)

	appErr := h.svc.RequireSpaceAdminOrTeamPerm("test", space, userID, mmmodel.PermissionManageSpace)
	require.NotNil(t, appErr, "team manage_space must not admit a caller who cannot read the space")
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id,
		"the denial must be the shared existence-hiding 403, not a distinguishable one")
}

// TestRequireSpaceAdminOrTeamPerm_TeamPermAdmitsOnOpenSpace is the positive half of the pair above:
// the same caller, the same team grant, on an open space the read resolver admits them to via the
// non-member fall-through. Together the two pin the conjunct to the read gate specifically — this
// case failing would mean the gate rejects callers it should serve, rather than the conjunct being
// absent.
func TestRequireSpaceAdminOrTeamPerm_TeamPermAdmitsOnOpenSpace(t *testing.T) {
	h, space, userID := teamManagerHarness(t, model.ViewAccessOpen)

	appErr := h.svc.RequireSpaceAdminOrTeamPerm("test", space, userID, mmmodel.PermissionManageSpace)
	require.Nil(t, appErr, "team manage_space must admit a caller the read gate already admits")
}

// formerAdminHarness seeds an open space whose acting user left the team but whose backing-channel
// state still grants them everything the elevated gates check: admin_space on the channel and
// manage_space on the team. Every grant the gates consult is stubbed true, so a denial can only come
// from the team-active conjunct — which is what these tests exist to pin.
func formerAdminHarness(t *testing.T) (*testHarness, *model.Space, string, *plugintest.API) {
	t.Helper()
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	// All registered before the harness, whose catch-alls would otherwise answer first: mock.Mock
	// matches expectations in registration order.
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: userID, DeleteAt: 1}, nil)
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionAdminSpace).Return(true).Maybe()
	mockAPI.On("HasPermissionToTeam", userID, teamID, mmmodel.PermissionManageSpace).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)
	return h, space, userID, mockAPI
}

// TestRequireSpaceAdminOrTeamPerm_FormerTeamMemberDenied pins the team-active conjunct on the
// channel-admin branch, the counterpart to TestRequireSpacePagePermission_FormerTeamMemberDenied on
// the page gate. Leaving a team does not remove the user's backing-channel rows, so a former member
// can still hold admin_space there. Without the conjunct that stale grant would keep admitting them
// to the manage tier — patch and delete on a space in a team they no longer belong to.
func TestRequireSpaceAdminOrTeamPerm_FormerTeamMemberDenied(t *testing.T) {
	h, space, userID, mockAPI := formerAdminHarness(t)

	appErr := h.svc.RequireSpaceAdminOrTeamPerm("test", space, userID, mmmodel.PermissionManageSpace)
	require.NotNil(t, appErr, "a former team member must not reach the manage tier through a stale channel admin_space grant")
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id,
		"the denial must be the shared existence-hiding 403, not a distinguishable one")
	// The team gate blocks before either elevated branch is consulted, so neither the lingering
	// channel grant nor the team manage_space grant is ever reached.
	mockAPI.AssertNotCalled(t, "HasPermissionToChannel", mock.Anything, mock.Anything, mock.Anything)
}

// TestRequireSpaceAdminOrSysadmin_FormerTeamMemberDenied is the same guard on the stricter gate:
// the space-wide exposure knobs (ViewAccess, default permissions) and admin-affecting member
// changes. This gate has no team-permission branch at all, so its channel-admin branch is the only
// non-sysadmin way in — making the team-active conjunct the whole of its membership requirement.
func TestRequireSpaceAdminOrSysadmin_FormerTeamMemberDenied(t *testing.T) {
	h, space, userID, mockAPI := formerAdminHarness(t)

	appErr := h.svc.RequireSpaceAdminOrSysadmin("test", space, userID)
	require.NotNil(t, appErr, "a former team member must not reach the admin tier through a stale channel admin_space grant")
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id,
		"the denial must be the shared existence-hiding 403, not a distinguishable one")
	mockAPI.AssertNotCalled(t, "HasPermissionToChannel", mock.Anything, mock.Anything, mock.Anything)
}

// TestRequireSpaceAdminOrSysadmin_ReadsSchemeAdminFromMaster pins WHERE this gate gets its answer,
// not just what the answer is. Every other test here can be satisfied by either source, so without
// this one nothing fails if the master read is swapped back for the cached permission composition.
//
// The gate is re-run inside the space-membership lock precisely to catch a demotion that landed
// while the caller waited for it. HasPermissionToChannel answers from GetAllChannelMembersForUser
// with allowFromCache set, on a context that is not pinned to the master, so it can still report
// the pre-demotion roles — the re-check would then admit exactly the actor it exists to exclude.
// Reading the SchemeAdmin flag off ChannelMembers on the master is what makes it observe the write.
//
// The demotion itself is not interleaved here: a cached grant and an absent row are the same
// divergence this asserts, and expressing it as "no row" makes the test deterministic.
func TestRequireSpaceAdminOrSysadmin_ReadsSchemeAdminFromMaster(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	// An active team member, so the gate reaches its admin branch rather than stopping at the
	// membership guard. Registered before the harness, whose catch-alls match otherwise.
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: userID}, nil).Maybe()
	// The cached composition says yes. The master says nothing — there is no ChannelMembers row.
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionAdminSpace).Return(true).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)

	appErr := h.svc.RequireSpaceAdminOrSysadmin("test", space, userID)
	require.NotNil(t, appErr,
		"the gate must read SchemeAdmin from the master; a cached admin_space grant with no ChannelMembers row must not admit")
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id,
		"the denial must be the shared existence-hiding 403, not a distinguishable one")
}

// TestRequireSpaceAdminOrSysadmin_SchemeAdminRowAdmits is the positive half of the pair above: the
// same gate, the same absent cached grant, and a real SchemeAdmin row is all it takes. Together the
// two pin the source of truth in both directions, so neither swapping the read back nor dropping
// the branch entirely can pass.
func TestRequireSpaceAdminOrSysadmin_SchemeAdminRowAdmits(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	teamID := mmmodel.NewId()
	mockAPI.On("GetTeamMember", teamID, userID).
		Return(&mmmodel.TeamMember{TeamId: teamID, UserId: userID}, nil).Maybe()
	// Deliberately denied on the cached path, to show the row alone carries the admission.
	mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionAdminSpace).Return(false).Maybe()
	h := openTestServiceWithAPI(t, mockAPI)

	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), teamID)
	testutil.MustAddChannelAdmin(t, h.db, space.ChannelId, userID)

	require.Nil(t, h.svc.RequireSpaceAdminOrSysadmin("test", space, userID),
		"a SchemeAdmin row on the master is the whole signal for admin_space and must admit on its own")
}

// TestResolveSpaceRead_GuestDeniedOpenFallthrough covers the guest exclusion from the open-space
// non-member read. Core's team_guest holds read_space but not read_public_channel, so a guest who
// is not a backing-channel member has no path into an open space they were never added to —
// unlike a plain team member, whom the fall-through admits.
func TestResolveSpaceRead_GuestDeniedOpenFallthrough(t *testing.T) {
	mockAPI := &plugintest.API{}
	guestID := mmmodel.NewId()
	// Both registered before the harness: StubDefaultSpacePermissions grants read_page on the
	// channel and read_public_channel on the team to any user, and mock.Mock matches expectations
	// in registration order.
	stubNonMember(mockAPI, guestID)
	testutil.StubGuestTeamDefaults(t, mockAPI, guestID)
	h := openTestServiceWithAPI(t, mockAPI)

	// The default fixture is open and the harness stubs an active team membership, so the guest
	// clears the team gate and reaches the fall-through — where team_guest's missing
	// read_public_channel is the only thing left to deny them.
	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), mmmodel.NewId())

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, guestID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution,
		"a guest lacking team read_public_channel must not be admitted to an open space by the fall-through")
}

// TestResolveSpaceRead_WithoutTeamReadSpaceDenied covers the outer gate on the direct read: team
// read_space is what the admin console offers as "View spaces", so revoking it has to close the
// space to a caller who reaches it by id, not merely hide it from the team listing. Both admission
// paths are checked, since the caller here holds backing-channel read_page and the fall-through's
// read_public_channel — everything the two branches would otherwise need.
func TestResolveSpaceRead_WithoutTeamReadSpaceDenied(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Registered before the harness: StubDefaultSpacePermissions grants read_space to every user,
	// and mock.Mock matches expectations in registration order.
	mockAPI.On("HasPermissionToTeam", userID, mock.Anything, mmmodel.PermissionReadSpace).Return(false).Maybe()
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, userID)

	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution,
		"a caller without team read_space must not read a space by id")
}

// TestPageGates_WithoutTeamReadSpaceDenied is the other half of the pair above: the page gates
// resolve team standing through the same helper as the space read, so revoking read_space closes
// a space's pages together with the space. Before the check moved into requireActiveMemberGate,
// the space answered 403 while its pages still answered 200.
func TestPageGates_WithoutTeamReadSpaceDenied(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	mockAPI.On("HasPermissionToTeam", userID, mock.Anything, mmmodel.PermissionReadSpace).Return(false).Maybe()
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	for name, gate := range map[string]func() *mmmodel.AppError{
		"page read": func() *mmmodel.AppError {
			return h.svc.RequireSpacePagePermission("test", space, userID, mmmodel.PermissionReadPage)
		},
		"page write": func() *mmmodel.AppError {
			return h.svc.RequireSpacePageWrite("test", space, userID, mmmodel.PermissionEditPage)
		},
		"manage": func() *mmmodel.AppError {
			return h.svc.RequireSpaceAdminOrTeamPerm("test", space, userID, mmmodel.PermissionManageSpace)
		},
	} {
		t.Run(name, func(t *testing.T) {
			appErr := gate()
			require.NotNil(t, appErr, "a caller without team read_space must be refused by every gate")
			require.Equal(t, http.StatusForbidden, appErr.StatusCode)
		})
	}
}

// TestResolveSpaceRead_InvalidUserIDIsBadRequest keeps a malformed user id reporting as a caller
// fault. Collapsing it into the existence-hiding 403 every genuine denial returns would make a
// caller-input bug indistinguishable from an ordinary authorization failure.
func TestResolveSpaceRead_InvalidUserIDIsBadRequest(t *testing.T) {
	mockAPI := &plugintest.API{}
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, "")

	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, app.ReadDenied, resolution)
}

// TestRequireSpacePagePermission_DemotedGuestDeniedWrites covers the grant that outlives a
// demotion. Core clears SchemeUser/SchemeAdmin when a user becomes a guest but leaves the atomic
// permission roles a prior grant wrote into ExplicitRoles, and it composes those into the member's
// channel permissions whatever the member's guest standing — so the channel check still reports
// the write permission, as the harness's default channel grants model here. The gate must deny it
// anyway, since a guest is read-only in a space.
func TestRequireSpacePagePermission_DemotedGuestDeniedWrites(t *testing.T) {
	for _, perm := range []*mmmodel.Permission{
		mmmodel.PermissionCreatePage, mmmodel.PermissionEditPage,
		mmmodel.PermissionCommentPage, mmmodel.PermissionDeleteOwnPage,
	} {
		t.Run(perm.Id, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			guestID := mmmodel.NewId()
			mockAPI.On("GetUser", guestID).
				Return(&mmmodel.User{Id: guestID, Roles: mmmodel.SystemGuestRoleId}, nil)
			testutil.StubGuestTeamDefaults(t, mockAPI, guestID)
			h := openTestServiceWithAPI(t, mockAPI)
			space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), mmmodel.NewId())

			appErr := h.svc.RequireSpacePagePermission("test", space, guestID, perm)

			require.NotNil(t, appErr, "a guest must not hold %s even with the role still in ExplicitRoles", perm.Id)
			require.Equal(t, http.StatusForbidden, appErr.StatusCode)
		})
	}
}

// TestRequireSpacePagePermission_DemotedGuestKeepsRead is the positive half of the pair above: the
// same guest still reads the space as a backing-channel member, so the refusals there come from
// the permission being a write rather than from guests being shut out of the space altogether.
func TestRequireSpacePagePermission_DemotedGuestKeepsRead(t *testing.T) {
	mockAPI := &plugintest.API{}
	guestID := mmmodel.NewId()
	testutil.StubGuestTeamDefaults(t, mockAPI, guestID)
	h := openTestServiceWithAPI(t, mockAPI)
	space := seedSpaceForTeam(t, h.store, mmmodel.NewId(), mmmodel.NewId())

	appErr := h.svc.RequireSpacePagePermission("test", space, guestID, mmmodel.PermissionReadPage)

	require.Nil(t, appErr)
	// The read arm is decided by the channel grant alone; the user is never read for it.
	mockAPI.AssertNotCalled(t, "GetUser", mock.Anything)
}

// TestAutoJoin_PublishOmitsFormerTeamMembers pins the WS omit list: a channel-scoped event must
// not reach backing-channel members who are no longer active members of the space's team — their
// rows survive the departure, and core's hub applies no team check of its own.
func TestAutoJoin_PublishOmitsFormerTeamMembers(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// The omit query resolves the channel's team through its Channels row; one member left the team.
	former := mmmodel.NewId()
	testutil.MustAddChannel(t, h.db, space.ChannelId, space.TeamId)
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, former)
	testutil.MustAddTeamMember(t, h.db, space.TeamId, former, 1)

	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	_, appErr := h.svc.JoinOpenSpace(space, userID)
	require.Nil(t, appErr)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_added",
		map[string]any{"space_id": space.Id, "user_id": userID},
		mock.MatchedBy(func(b *mmmodel.WebsocketBroadcast) bool {
			return b.ChannelId == space.ChannelId && b.OmitUsers[former]
		}))
}
