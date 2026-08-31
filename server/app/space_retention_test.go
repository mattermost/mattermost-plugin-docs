// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app_test

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// stubRetentionEnrolment wires AddChannelsToRetentionPolicy and
// RemoveChannelsFromRetentionPolicy to the RetentionPoliciesChannels stand-in table; failEnrol
// makes the add error without enrolling. The add mirrors core's semantics faithfully: a plain
// batch insert whose conflict on the channel-keyed primary key makes the whole statement a
// silent no-op — core swallows the DB error and reports success.
func stubRetentionEnrolment(t *testing.T, mockAPI *plugintest.API, db *sql.DB, failEnrol *bool) *[][]string {
	t.Helper()
	var calls [][]string
	mockAPI.On("AddChannelsToRetentionPolicy", mock.AnythingOfType("string"), mock.AnythingOfType("[]string")).Return(
		func(policyID string, channelIDs []string) *mmmodel.AppError {
			calls = append(calls, channelIDs)
			if failEnrol != nil && *failEnrol {
				return mmmodel.NewAppError("AddChannelsToRetentionPolicy", "test.enrol_failure", nil, "", http.StatusInternalServerError)
			}
			_, _ = db.Exec(`INSERT INTO RetentionPoliciesChannels (PolicyId, ChannelId)
				SELECT $1, id FROM unnest($2::varchar[]) AS ids(id)`, policyID, pq.Array(channelIDs))
			return nil
		}).Maybe()
	mockAPI.On("RemoveChannelsFromRetentionPolicy", mock.AnythingOfType("string"), mock.AnythingOfType("[]string")).Return(
		func(policyID string, channelIDs []string) *mmmodel.AppError {
			_, err := db.Exec(`DELETE FROM RetentionPoliciesChannels WHERE PolicyId = $1 AND ChannelId = ANY($2)`, policyID, pq.Array(channelIDs))
			require.NoError(t, err)
			return nil
		}).Maybe()
	return &calls
}

func TestServiceCreateSpaceRetentionEnrolment(t *testing.T) {
	newSpace := func() *model.Space { return &model.Space{TeamId: mmmodel.NewId(), Title: "Test Space"} }

	stubChannelCreate := func(mockAPI *plugintest.API, teamID string) string {
		backingChannelID := mmmodel.NewId()
		mockAPI.On("CreateChannel", mock.MatchedBy(func(ch *mmmodel.Channel) bool {
			return ch.Type == mmmodel.ChannelTypeSpace && ch.TeamId == teamID
		})).Return(&mmmodel.Channel{Id: backingChannelID, TeamId: teamID, Type: mmmodel.ChannelTypeSpace}, nil)
		mockAPI.On("AddChannelMember", backingChannelID, mock.AnythingOfType("string")).Return(&mmmodel.ChannelMember{}, nil)
		return backingChannelID
	}

	enrolled := func(t *testing.T, db *sql.DB, policyID, channelID string) bool {
		t.Helper()
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM RetentionPoliciesChannels WHERE PolicyId = $1 AND ChannelId = $2`, policyID, channelID).Scan(&count))
		return count == 1
	}

	t.Run("a configured policy enrols the backing channel in the same create", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		calls := stubRetentionEnrolment(t, mockAPI, h.db, nil)
		h.svc.SetRetentionPolicyID("policy0000000000000000000x")

		space := newSpace()
		channelID := stubChannelCreate(mockAPI, space.TeamId)

		saved, appErr := h.svc.CreateSpace(space, mmmodel.NewId())
		require.Nil(t, appErr)
		assert.True(t, enrolled(t, h.db, "policy0000000000000000000x", channelID))
		assert.Equal(t, channelID, saved.ChannelId)
		require.Len(t, *calls, 1)
	})

	t.Run("no policy configured means no enrolment call", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		calls := stubRetentionEnrolment(t, mockAPI, h.db, nil)

		space := newSpace()
		stubChannelCreate(mockAPI, space.TeamId)

		_, appErr := h.svc.CreateSpace(space, mmmodel.NewId())
		require.Nil(t, appErr)
		assert.Empty(t, *calls)
	})

	t.Run("an enrolment failure fails the create and archives the orphan channel", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		failEnrol := true
		stubRetentionEnrolment(t, mockAPI, h.db, &failEnrol)
		h.svc.SetRetentionPolicyID("policy0000000000000000000x")

		space := newSpace()
		channelID := stubChannelCreate(mockAPI, space.TeamId)
		mockAPI.On("DeleteChannel", channelID).Return(nil).Once()

		_, appErr := h.svc.CreateSpace(space, mmmodel.NewId())
		require.NotNil(t, appErr)
		assert.Equal(t, "app.space.create.retention_enrol_failed.app_error", appErr.Id)
		mockAPI.AssertCalled(t, "DeleteChannel", channelID)
	})
}

func TestServiceReconcileSpaceRetention(t *testing.T) {
	policyID := "policy0000000000000000000x"

	t.Run("enrols every unenrolled space channel and is idempotent", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		calls := stubRetentionEnrolment(t, mockAPI, h.db, nil)
		h.svc.SetRetentionPolicyID(policyID)

		spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
		spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())

		require.NoError(t, h.svc.ReconcileSpaceRetention())
		require.Len(t, *calls, 1)
		assert.ElementsMatch(t, []string{spaceA.ChannelId, spaceB.ChannelId}, (*calls)[0])

		// A second run finds nothing to do.
		require.NoError(t, h.svc.ReconcileSpaceRetention())
		assert.Len(t, *calls, 1)
	})

	t.Run("a policy change re-homes channels off the old policy", func(t *testing.T) {
		// Core keys the assignment on the channel alone, and its keyed insert silently no-ops
		// on conflict — so an add-only sweep would leave every space on the old policy forever.
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		stubRetentionEnrolment(t, mockAPI, h.db, nil)

		spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
		spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())

		h.svc.SetRetentionPolicyID(policyID)
		require.NoError(t, h.svc.ReconcileSpaceRetention())

		newPolicyID := "policy0000000000000000000y"
		h.svc.SetRetentionPolicyID(newPolicyID)
		require.NoError(t, h.svc.ReconcileSpaceRetention())

		for _, channelID := range []string{spaceA.ChannelId, spaceB.ChannelId} {
			var got string
			require.NoError(t, h.db.QueryRow(`SELECT PolicyId FROM RetentionPoliciesChannels WHERE ChannelId = $1`, channelID).Scan(&got))
			assert.Equal(t, newPolicyID, got, "the channel must carry exactly the new policy")
		}
	})

	t.Run("a deleted space is swept too", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		stubRetentionEnrolment(t, mockAPI, h.db, nil)
		h.svc.SetRetentionPolicyID(policyID)

		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		require.NoError(t, h.store.DeleteSpace(space.Id))

		require.NoError(t, h.svc.ReconcileSpaceRetention())
		var count int
		require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM RetentionPoliciesChannels WHERE PolicyId = $1 AND ChannelId = $2`, policyID, space.ChannelId).Scan(&count))
		assert.Equal(t, 1, count, "a restorable space's content needs the policy while it waits")
	})

	t.Run("no policy configured is a no-op", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		calls := stubRetentionEnrolment(t, mockAPI, h.db, nil)

		mustCreateSpace(t, h.store, mmmodel.NewId())
		require.NoError(t, h.svc.ReconcileSpaceRetention())
		assert.Empty(t, *calls)
	})

	t.Run("an enrolment that lands nothing stops instead of looping", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		// Report success while enrolling nothing — the state the non-convergence guard exists for.
		var calls [][]string
		mockAPI.On("AddChannelsToRetentionPolicy", mock.AnythingOfType("string"), mock.AnythingOfType("[]string")).Return(
			func(string, []string) *mmmodel.AppError {
				calls = append(calls, nil)
				return nil
			}).Maybe()
		h.svc.SetRetentionPolicyID(policyID)

		mustCreateSpace(t, h.store, mmmodel.NewId())
		err := h.svc.ReconcileSpaceRetention()
		require.Error(t, err)
		assert.Len(t, calls, 1, "the non-convergence guard fires after one fruitless enrolment")
	})
}

func TestServiceReleaseSpaceRetention(t *testing.T) {
	policyID := "policy0000000000000000000x"

	assignedPolicy := func(t *testing.T, h *testHarness, channelID string) string {
		t.Helper()
		var got string
		err := h.db.QueryRow(`SELECT PolicyId FROM RetentionPoliciesChannels WHERE ChannelId = $1`, channelID).Scan(&got)
		if errors.Is(err, sql.ErrNoRows) {
			return ""
		}
		require.NoError(t, err)
		return got
	}

	t.Run("returns every enrolled space channel to the standard clock", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		stubRetentionEnrolment(t, mockAPI, h.db, nil)
		h.svc.SetRetentionPolicyID(policyID)

		spaceA := mustCreateSpace(t, h.store, mmmodel.NewId())
		spaceB := mustCreateSpace(t, h.store, mmmodel.NewId())
		require.NoError(t, h.svc.ReconcileSpaceRetention())

		require.NoError(t, h.svc.ReleaseSpaceRetention(policyID))

		for _, channelID := range []string{spaceA.ChannelId, spaceB.ChannelId} {
			assert.Empty(t, assignedPolicy(t, h, channelID), "clearing the setting must drop the Docs assignment")
		}
	})

	t.Run("leaves an assignment Docs did not make in place", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		stubRetentionEnrolment(t, mockAPI, h.db, nil)

		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		adminPolicyID := "policy0000000000000000000z"
		_, err := h.db.Exec(`INSERT INTO RetentionPoliciesChannels (PolicyId, ChannelId) VALUES ($1, $2)`, adminPolicyID, space.ChannelId)
		require.NoError(t, err)

		require.NoError(t, h.svc.ReleaseSpaceRetention(policyID))

		assert.Equal(t, adminPolicyID, assignedPolicy(t, h, space.ChannelId),
			"only the policy Docs enrolled into may be released")
	})

	t.Run("is idempotent and a no-op without a policy", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		h := openTestServiceWithAPI(t, mockAPI)
		stubRetentionEnrolment(t, mockAPI, h.db, nil)
		h.svc.SetRetentionPolicyID(policyID)

		space := mustCreateSpace(t, h.store, mmmodel.NewId())
		require.NoError(t, h.svc.ReconcileSpaceRetention())

		require.NoError(t, h.svc.ReleaseSpaceRetention(""))
		assert.Equal(t, policyID, assignedPolicy(t, h, space.ChannelId), "an empty policy releases nothing")

		require.NoError(t, h.svc.ReleaseSpaceRetention(policyID))
		require.NoError(t, h.svc.ReleaseSpaceRetention(policyID))
		assert.Empty(t, assignedPolicy(t, h, space.ChannelId))
	})
}
