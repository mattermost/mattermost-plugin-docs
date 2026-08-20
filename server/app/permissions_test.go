// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"errors"
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
// and returns the harness plus that space. The caller stubs whichever of RolesGrantPermission /
// GetChannelMember / AddChannelMember its scenario reaches.
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

// TestAutoJoin_NotFallthroughIsNoOp covers the guard that makes auto-join apply to exactly one
// admission path: a caller admitted as a member (or as sysadmin, or denied outright) must never be
// joined, since joining is only ever a consequence of the open-space non-member fall-through.
func TestAutoJoin_NotFallthroughIsNoOp(t *testing.T) {
	for _, resolution := range []app.ReadResolution{app.ReadDenied, app.ReadViaSysadmin, app.ReadViaMember} {
		mockAPI := &plugintest.API{}
		h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

		joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, mmmodel.NewId(), resolution, mmmodel.PermissionCreatePage, nil)
		require.Nil(t, appErr)
		require.False(t, joined, "resolution %v must not auto-join", resolution)
		mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
	}
}

// TestAutoJoin_JoinsWhenDefaultGrants covers the success path of the auto-join pre-step — the step
// that runs ahead of a write gate and can add the caller to the backing channel so the gate then
// passes. A non-member admitted via the open-space fall-through, whose space default grants the
// permission, is added to the channel and the membership-added event is published.
func TestAutoJoin_JoinsWhenDefaultGrants(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(true)
	// Not yet a member: no ChannelMembers row is seeded, so the master-side membership probe
	// misses and the join path runs.
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)
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

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: userID}}, nil)

	members, _, appErr := h.svc.GetSpaceMembers(space, 0, 60)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.True(t, members[0].AutoJoined, "a member added by the auto-join pre-step must be marked auto-joined")

	// A deliberate admin act on this member's permissions supersedes how they got here. The
	// last-admin/target read is master-backed, not the plugin API's GetChannelMember, so seed the
	// stand-in row directly (the auto-join above only ran through the mocked pluginapi call, which
	// does not write this table).
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)

	_, appErr = h.svc.SetSpaceMemberPermissions(space, userID, []string{mmmodel.PermissionCommentPage.Id}, mmmodel.NewId())
	require.Nil(t, appErr)

	members, _, appErr = h.svc.GetSpaceMembers(space, 0, 60)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.False(t, members[0].AutoJoined, "a deliberate admin permission change must clear the auto-join marker")
}

// TestAutoJoin_ProvenanceMarkerFailureAddsNoMembership pins the write order the undo path depends
// on. UndoAutoJoin removes a membership only while its marker exists, and reads a missing marker as
// "an admin legitimized this member" — so a membership that outlives its marker is one nothing ever
// reclaims. Marking first is what rules that out: the failure can then only leave a marker with no
// membership, which grants nothing, because authority comes from channel membership and roles.
//
// The assertion that carries this is AssertNotCalled on the add: restoring the add-then-mark order
// makes the add happen, so this test fails rather than merely reporting a different error.
func TestAutoJoin_ProvenanceMarkerFailureAddsNoMembership(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)

	// Stubbed so an add would succeed if it were reached: the point is that it is not reached, and
	// an unstubbed call would fail as a panic rather than as the assertion below.
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil).Maybe()

	// The marker table is the only thing dropped, so every read ahead of the marker write still
	// answers and the join reaches exactly the step under test.
	_, dbErr := h.db.Exec(`DROP TABLE DOCS_SpaceAutoJoin`)
	require.NoError(t, dbErr)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.NotNil(t, appErr, "a marker write that failed must fail the join rather than proceed untracked")
	require.Equal(t, "app.space.auto_join.provenance_write_failed.app_error", appErr.Id)
	require.False(t, joined)
	mockAPI.AssertNotCalled(t, "AddChannelMember", space.ChannelId, userID)
}

// TestRequireSpacePublish_JoinedThenDeniedLeavesNoMembership covers the property that makes the
// auto-join safe to run ahead of the gate it enables: when the gate refuses after the join has
// committed, the caller is told it joined, so the membership can be undone and the rejected request
// leaves nothing behind.
//
// It also pins the direction of the one window this design accepts. The post-join re-check reads
// membership through the plugin API, which answers from a replica, so a replica lagging behind the
// add reports a non-member and the write is refused — modelled here by a channel check that never
// reports the permission. Refusing is the safe direction; what must not happen is refusing *and*
// keeping the membership.
func TestRequireSpacePublish_JoinedThenDeniedLeavesNoMembership(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	// Registered before the harness so it beats StubDefaultSpacePermissions' catch-all: no
	// backing-channel permission is ever reported for this user, which is both what forces the
	// fall-through read and what stands in for a re-check that cannot see the fresh membership.
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// The space default does grant create_page, so the auto-join is authorized and proceeds.
	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)
	mockAPI.On("DeleteChannelMember", space.ChannelId, userID).Return(nil)

	joined, appErr := h.svc.RequireSpacePublish("test", space, userID, app.ReadViaOpenFallthrough, true)

	require.True(t, joined, "the caller must learn a membership was created, or it cannot undo one")
	require.NotNil(t, appErr, "a re-check that cannot see the membership must refuse, not admit")
	require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	require.Equal(t, "app.space.access.forbidden.app_error", appErr.Id,
		"the refusal must be the shared existence-hiding 403, not a distinguishable one")

	// What the API layer does with that pair, and the half that matters: no membership survives a
	// request the server refused.
	h.svc.UndoAutoJoin(joined, space, userID)
	mockAPI.AssertCalled(t, "DeleteChannelMember", space.ChannelId, userID)

	members, err := h.store.AutoJoinedIDs(space.Id)
	require.NoError(t, err)
	require.NotContains(t, members, userID, "the undo must clear the provenance marker with the membership")
}

// TestUndoAutoJoin_ClearsProvenanceMarker covers the other half of the marker's lifecycle: undoing
// a join a rejected write triggered must clear the marker too, so a later deliberate re-add of the
// same user is not misreported as auto-joined.
func TestUndoAutoJoin_ClearsProvenanceMarker(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)

	mockAPI.On("DeleteChannelMember", space.ChannelId, userID).Return(nil)
	h.svc.UndoAutoJoin(joined, space, userID)
	mockAPI.AssertCalled(t, "DeleteChannelMember", space.ChannelId, userID)

	// Re-added deliberately, not through auto-join: if UndoAutoJoin left the marker behind, this
	// membership would incorrectly resurface as auto-joined.
	_, appErr = h.svc.AddSpaceMember(space, userID)
	require.Nil(t, appErr)

	mockAPI.On("GetChannelMembers", space.ChannelId, 0, 60).
		Return(mmmodel.ChannelMembers{{ChannelId: space.ChannelId, UserId: userID}}, nil)
	members, _, appErr := h.svc.GetSpaceMembers(space, 0, 60)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.False(t, members[0].AutoJoined,
		"UndoAutoJoin must clear the provenance marker rather than leave it for a later deliberate add")
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

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)

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
	members, _, appErr := h.svc.GetSpaceMembers(space, 0, 60)
	require.Nil(t, appErr)
	require.Len(t, members, 1)
	require.False(t, members[0].AutoJoined,
		"RemoveSpaceMember must clear the provenance marker rather than leave it for a later deliberate add")
}

// TestUndoAutoJoin_SkipsRemovalWhenLegitimizedConcurrently is the marker guard's positive case: an
// admin permission grant lands, clearing the marker, in the window between the auto-join and the
// undo of the rejected write it admitted. The undo must leave the now-deliberate membership intact.
func TestUndoAutoJoin_SkipsRemovalWhenLegitimizedConcurrently(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)

	// A deliberate admin act legitimizes the membership before the guarded write's own rejection
	// reaches UndoAutoJoin — clearing the marker, exactly as SetSpaceMemberPermissions does. The
	// target read is master-backed; the auto-join above only ran through the mocked pluginapi call,
	// which does not write this table, so seed it directly.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)
	_, appErr = h.svc.SetSpaceMemberPermissions(space, userID, []string{mmmodel.PermissionCommentPage.Id}, mmmodel.NewId())
	require.Nil(t, appErr)

	h.svc.UndoAutoJoin(joined, space, userID)

	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, userID)
}

// TestUndoAutoJoin_SkipsRemovalWhenLegitimizedByDeliberateReAdd is
// TestUndoAutoJoin_SkipsRemovalWhenLegitimizedConcurrently's counterpart for the other legitimizing
// act: a deliberate re-add via AddSpaceMember (not just a permission grant) also clears the marker,
// and does so atomically with the add under the space-keyed lock, so UndoAutoJoin can only ever see
// the clear wholly before or wholly after its own locked marker check — never in between.
func TestUndoAutoJoin_SkipsRemovalWhenLegitimizedByDeliberateReAdd(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	roleName := testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)
	mockAPI.On("RolesGrantPermission", []string{roleName}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)

	// An admin deliberately re-adds the same user before the guarded write's own rejection reaches
	// UndoAutoJoin. Re-adding an existing member is a no-op for core; AddSpaceMember clears the marker.
	_, appErr = h.svc.AddSpaceMember(space, userID)
	require.Nil(t, appErr)

	h.svc.UndoAutoJoin(joined, space, userID)

	mockAPI.AssertNotCalled(t, "DeleteChannelMember", space.ChannelId, userID)
}

// TestAutoJoin_DefaultDoesNotGrant covers the admission test: the fall-through alone never joins.
// A space whose default permission set withholds the permission leaves the caller a non-member, so
// the write gate that follows denies them rather than silently granting access by joining first.
func TestAutoJoin_DefaultDoesNotGrant(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(false)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.False(t, joined)
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

	// Granted, so the re-validation below is the only thing that can abort the join — the pre-step's
	// own admission test must not be what makes this pass.
	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(true)

	// The caller still holds the open-space record it was admitted against; the stored row has
	// since flipped private, which is what the pre-step re-reads.
	stale := *space
	private := model.ViewAccessPrivate
	_, err := h.store.UpdateSpace(space.Id, &model.SpacePatch{ViewAccess: &private}, space.UpdateAt, false)
	require.NoError(t, err)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(&stale, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.False(t, joined, "a space that flipped private must not auto-join a stale open-read admission")
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_DeletedSpaceIsNoOp covers the space-vanished branch: a space deleted between the
// admitting read and the pre-step yields no join and no error, rather than a 500 on a race the
// caller cannot act on.
func TestAutoJoin_DeletedSpaceIsNoOp(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	// Granted, so the deleted-space branch below is the only thing that can prevent the join.
	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(true)

	require.NoError(t, h.store.DeleteSpace(space.Id))

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.False(t, joined)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_OwnerCheckGatesJoin covers the delete_own_page contract: the ownerCheck must hold
// before the join, so a caller who does not own the target page is never made a member on the
// strength of an own-page permission that cannot apply to it.
func TestAutoJoin_OwnerCheckGatesJoin(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionDeleteOwnPage.Id).Return(true)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough,
		mmmodel.PermissionDeleteOwnPage, func() (bool, error) { return false, nil })
	require.Nil(t, appErr)
	require.False(t, joined)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_OwnerCheckFailurePropagates distinguishes a failed ownership lookup from a negative
// one: the first is an outage and must surface as an error, not as a silent no-join the caller
// would report to the user as a denial.
func TestAutoJoin_OwnerCheckFailurePropagates(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionDeleteOwnPage.Id).Return(true)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough,
		mmmodel.PermissionDeleteOwnPage, func() (bool, error) { return false, errors.New("lookup failed") })
	require.NotNil(t, appErr)
	require.False(t, joined)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestAutoJoin_AlreadyMemberIsNoOp covers idempotency: an existing member is not re-added, so a
// repeated write by an already-joined caller does not churn membership or republish the event.
func TestAutoJoin_AlreadyMemberIsNoOp(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(true)
	// The membership already exists on the master DB. No GetChannelMember stub: the probe must
	// read the master through the plugin store, never the replica-backed plugin API — a stale
	// replica miss here would adopt this existing membership as a fresh join, and the undo paired
	// with it would then remove a membership the caller already held.
	testutil.MustAddChannelMember(t, h.db, space.ChannelId, userID)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.False(t, joined)
	mockAPI.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// TestRequireSpaceDraftWrite_LookupFailureIsNotADenial pins the error discipline of the two-attempt
// draft gate: a failure of the create_page check must surface as itself, not be retried into the
// edit_page attempt and reported as a 403 — a backend outage must never read as "not authorized".
func TestRequireSpaceDraftWrite_LookupFailureIsNotADenial(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	// The scheme lookup behind the auto-join pre-step fails outright. That is a failure of the
	// check, not a denial, so it must not be swallowed and retried as the edit_page attempt.
	channelID := mmmodel.NewId()
	mockAPI.On("GetChannelOfType", channelID, mmmodel.ChannelTypeSpace).
		Return((*mmmodel.Channel)(nil), &mmmodel.AppError{StatusCode: 500, Id: "channel.lookup.failed"})
	h := openTestServiceWithAPI(t, mockAPI)
	space := testutil.MustCreateSpace(t, h.store, channelID, mmmodel.NewId())

	_, appErr := h.svc.RequireSpaceDraftWrite("test", space, userID, app.ReadViaOpenFallthrough)

	require.NotNil(t, appErr)
	require.NotEqual(t, http.StatusForbidden, appErr.StatusCode,
		"a failed lookup must not be reported as an authorization denial")
	// One attempt, not two: the create_page failure short-circuits instead of falling through to
	// the edit_page retry. Each attempt runs the pre-step, which resolves team membership once.
	mockAPI.AssertNumberOfCalls(t, "GetTeamMember", 1)
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

			joined, appErr := h.svc.RequireSpaceDraftWrite("test", space, userID, app.ReadViaMember)
			require.Nil(t, appErr, "%s must admit a draft write", granted.Id)
			require.False(t, joined, "a member admission never auto-joins")
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

		_, appErr := h.svc.RequireSpaceDraftWrite("test", space, userID, app.ReadViaMember)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusForbidden, appErr.StatusCode)
	})
}

// TestResolveSpacePageOwnOrAny_CheckFailureIsNotADenial pins the same error discipline on the
// own/any gate that TestRequireSpaceDraftWrite_LookupFailureIsNotADenial pins on the draft gate:
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
func TestResolveSpacePageOwnOrAny_CheckFailureIsNotADenial(t *testing.T) {
	for name, ownerMatches := range map[string]bool{
		"owner does not match": false,
		"owner matches":        true,
	} {
		t.Run(name, func(t *testing.T) {
			h := openTestService(t)
			// An unwired client makes both permission attempts fail as a 500 rather than deny.
			h.svc = app.New(h.store, nil, nil)
			space := testutil.MustCreateSpace(t, h.store, mmmodel.NewId(), mmmodel.NewId())

			ownOnly, admitted, appErr := h.svc.ResolveSpacePageOwnOrAny(
				space, mmmodel.NewId(),
				"any", mmmodel.PermissionDeletePage,
				"own", mmmodel.PermissionDeleteOwnPage,
				ownerMatches, app.ReadViaMember)

			require.NotNil(t, appErr, "a failed check must not be reported as an ordinary denial")
			require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
			require.False(t, admitted)
			require.False(t, ownOnly)
		})
	}
}

// TestRequireSpacePagePermissionFrom_FallthroughAdmitsReadOnly is the escalation guard on the
// open-space fall-through: it exists to admit reads, and must never admit a write. A non-member
// whose space default withholds the write permission — so the auto-join pre-step does not join
// them — reaches the gate still holding only the fall-through, and every write permission has to
// be refused there. Without the read-permission condition on that branch the fall-through would
// grant create/edit/delete outright, since the caller already holds the team read_public_channel
// the branch keys on.
func TestRequireSpacePagePermissionFrom_FallthroughAdmitsReadOnly(t *testing.T) {
	for _, perm := range []*mmmodel.Permission{
		mmmodel.PermissionCreatePage, mmmodel.PermissionEditPage,
		mmmodel.PermissionDeletePage, mmmodel.PermissionDeleteOwnPage,
	} {
		t.Run(perm.Id, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			userID := mmmodel.NewId()
			stubNonMember(mockAPI, userID)
			h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

			appErr := h.svc.RequireSpacePagePermissionFrom("test", space, userID, perm, app.ReadViaOpenFallthrough)

			require.NotNil(t, appErr, "%s must not be admitted by the open-space read fall-through", perm.Id)
			require.Equal(t, http.StatusForbidden, appErr.StatusCode)
		})
	}
}

// TestRequireSpacePagePermissionFrom_FallthroughAdmitsRead is the positive half of the pair above:
// the very same non-member and resolution do get read_page, so the refusals there come from the
// permission being a write, not from the fall-through being closed altogether.
func TestRequireSpacePagePermissionFrom_FallthroughAdmitsRead(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	appErr := h.svc.RequireSpacePagePermissionFrom("test", space, userID, mmmodel.PermissionReadPage, app.ReadViaOpenFallthrough)

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
	testutil.StubGuestTeamDefaults(mockAPI, guestID)
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
			testutil.StubGuestTeamDefaults(mockAPI, guestID)
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
	testutil.StubGuestTeamDefaults(mockAPI, guestID)
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

	mockAPI.On("RolesGrantPermission", []string{testutil.PresetUserRoleName(mmmodel.SchemeNameSpaceContribute)}, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)

	mockAPI.AssertCalled(t, "PublishWebSocketEvent", "space_member_added",
		map[string]any{"space_id": space.Id, "user_id": userID},
		mock.MatchedBy(func(b *mmmodel.WebsocketBroadcast) bool {
			return b.ChannelId == space.ChannelId && b.OmitUsers[former]
		}))
}
