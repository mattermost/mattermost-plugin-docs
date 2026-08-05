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

	// Granted, so the re-validation below is the only thing that can abort the join — the pre-step's
	// own admission test must not be what makes this pass.
	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionCreatePage.Id).Return(true)

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
	mockAPI.On("RolesGrantPermission", mock.Anything, mmmodel.PermissionCreatePage.Id).Return(true)

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

	_, appErr := h.svc.RequireSpaceDraftWrite("test", space, userID, app.ReadViaOpenFallthrough)

	require.NotNil(t, appErr)
	require.NotEqual(t, http.StatusForbidden, appErr.StatusCode,
		"a failed lookup must not be reported as an authorization denial")
	// One attempt, not two: the create_page failure short-circuits instead of falling through to
	// the edit_page retry. Each attempt runs the pre-step, which resolves team membership once.
	mockAPI.AssertNumberOfCalls(t, "GetTeamMember", 1)
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
			mockAPI.On("HasPermissionToChannel", userID, mock.Anything, mmmodel.PermissionDeletePage).Return(false).Maybe()
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

	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), teamID)

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

	// The default space fixture is open, and the harness stubs GetTeamMember to an active
	// membership, so the stranger clears the team gate and reaches the open-space fall-through.
	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), mmmodel.NewId())

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, strangerID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution,
		"compliance mode must suppress the open-space non-member fall-through even though the caller holds read_public_channel")
}

// teamManagerHarness seeds a space with the given view access whose acting user holds team
// manage_space but is not a member of the backing channel — the actor RequireSpaceAdminOrTeamPerm's
// read-gate conjunct exists to constrain.
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

	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), teamID)
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
	space := seedSpaceForTeam(t, h.store, h.db, mmmodel.NewId(), mmmodel.NewId())

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, guestID)
	require.Nil(t, appErr)
	require.Equal(t, app.ReadDenied, resolution,
		"a guest lacking team read_public_channel must not be admitted to an open space by the fall-through")
}

// TestResolveSpaceRead_InvalidUserIDIsBadRequest keeps a malformed user id reporting as a caller
// fault. Collapsing it into the existence-hiding 403 every genuine denial returns would make a
// plumbing bug indistinguishable from an ordinary authorization failure.
func TestResolveSpaceRead_InvalidUserIDIsBadRequest(t *testing.T) {
	mockAPI := &plugintest.API{}
	h, space := autoJoinHarness(t, mockAPI, model.ViewAccessOpen)

	resolution, appErr := h.svc.ResolveSpaceRead("test", space, "")

	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Equal(t, app.ReadDenied, resolution)
}
