// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// retentionEnrolChunkSize bounds one detection read and one enrolment call; the sweep loops
// until every space's backing channel is enrolled.
const retentionEnrolChunkSize = 100

// enrolSpaceChannelInRetention assigns a space's backing channel to the configured Docs
// data-retention policy. A no-op when no policy is configured — an admin who has configured
// nothing has not asked for a Docs exception, so Docs content follows the chat clock.
func (s *Service) enrolSpaceChannelInRetention(channelID string) *mmmodel.AppError {
	policyID := s.RetentionPolicyID()
	if policyID == "" {
		return nil
	}
	if err := s.client.System.AddChannelsToRetentionPolicy(policyID, []string{channelID}); err != nil {
		return mmmodel.NewAppError("CreateSpace", "app.space.create.retention_enrol_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return nil
}

// ReconcileSpaceRetention moves every space backing channel into the configured Docs
// data-retention policy — spaces created before the setting existed, or while the plugin was
// inactive, channels a failed create-time enrolment left behind, and channels still assigned
// to a previously configured policy. The last case is why this is a re-home rather than an
// add: core keys the assignment on the channel alone, so a channel carrying another policy
// must be removed from it first or the add leaves the old assignment silently in place.
// Idempotent and re-runnable; a no-op when no policy is configured. Deleted spaces are swept
// too: a soft-deleted space is restorable, so its content needs the policy's protection while
// it waits.
func (s *Service) ReconcileSpaceRetention() error {
	policyID := s.RetentionPolicyID()
	if policyID == "" || s.client == nil {
		return nil
	}
	lastFirst := ""
	for {
		rows, err := s.store.GetSpaceChannelsNotInRetentionPolicy(policyID, retentionEnrolChunkSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		// The detection is ordered, so the same leading id after a successful enrolment means
		// the enrolment is not landing rows — stop rather than loop forever.
		if rows[0].ChannelId == lastFirst {
			return errors.New("space retention enrolment is not converging; channel " + rows[0].ChannelId + " is still unenrolled after an enrolment")
		}
		lastFirst = rows[0].ChannelId

		byCurrentPolicy := make(map[string][]string)
		channelIDs := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.PolicyId != "" {
				byCurrentPolicy[row.PolicyId] = append(byCurrentPolicy[row.PolicyId], row.ChannelId)
			}
			channelIDs = append(channelIDs, row.ChannelId)
		}
		for currentPolicyID, ids := range byCurrentPolicy {
			if err := s.client.System.RemoveChannelsFromRetentionPolicy(currentPolicyID, ids); err != nil {
				return err
			}
		}
		if err := s.client.System.AddChannelsToRetentionPolicy(policyID, channelIDs); err != nil {
			return err
		}
	}
}
