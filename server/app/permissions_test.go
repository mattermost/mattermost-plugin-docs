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
		mmmodel.PermissionEditPage, mmmodel.PermissionDeleteOwnPage,
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

// TestAutoJoin_JoinsWhenDefaultGrants is the successful pre-step: a non-member admitted via the
// open-space fall-through, whose space default grants the permission, is added to the backing
// channel and the membership-added event is published.
func TestAutoJoin_JoinsWhenDefaultGrants(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionCreatePage.Id).Return(true)
	// Not yet a member: the join path runs only when the membership probe misses.
	mockAPI.On("GetChannelMember", space.ChannelId, userID).Return((*mmmodel.ChannelMember)(nil), &mmmodel.AppError{StatusCode: 404})
	mockAPI.On("AddChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

	joined, appErr := h.svc.AutoJoinIfDefaultGranted(space, userID, app.ReadViaOpenFallthrough, mmmodel.PermissionCreatePage, nil)
	require.Nil(t, appErr)
	require.True(t, joined)
	mockAPI.AssertCalled(t, "AddChannelMember", space.ChannelId, userID)
}

// TestAutoJoin_DefaultDoesNotGrant covers the admission test: the fall-through alone never joins.
// A space whose default capability set withholds the permission leaves the caller a non-member, so
// the write gate that follows denies them rather than silently granting access by joining first.
func TestAutoJoin_DefaultDoesNotGrant(t *testing.T) {
	mockAPI := &plugintest.API{}
	userID := mmmodel.NewId()
	stubNonMember(mockAPI, userID)
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionCreatePage.Id).Return(false)

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

	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionDeleteOwnPage.Id).Return(true)

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

	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionDeleteOwnPage.Id).Return(true)

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

	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionCreatePage.Id).Return(true)
	mockAPI.On("GetChannelMember", space.ChannelId, userID).
		Return(&mmmodel.ChannelMember{ChannelId: space.ChannelId, UserId: userID}, nil)

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

	appErr := h.svc.RequireSpaceDraftWrite("test", space, userID, app.ReadViaOpenFallthrough)

	require.NotNil(t, appErr)
	require.NotEqual(t, http.StatusForbidden, appErr.StatusCode,
		"a failed lookup must not be reported as an authorization denial")
	// One attempt, not two: the create_page failure short-circuits instead of falling through to
	// the edit_page retry. Each attempt runs the pre-step, which resolves team membership once.
	mockAPI.AssertNumberOfCalls(t, "GetTeamMember", 1)
}
