// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// provisionImportTarget makes the job's target ready to receive pages and returns the refreshed job.
//
// The two target kinds are genuinely different problems. An existing Space needs only its ImportSource row,
// which is a database transaction. A new Space needs a backing channel created through an external API that
// shares no transaction with the database, which is where every hard case in this file comes from.
func (s *Service) provisionImportTarget(job *model.ImportJob) (*model.ImportJob, error) {
	if job.TargetSpaceExisted {
		provisioned, err := s.store.EnsureImportSourceForTarget(job.Id, job.SelectedSourceDisplayName)
		if err != nil {
			return nil, errors.Wrap(err, "ensure import source for existing target")
		}
		return provisioned, nil
	}
	return s.provisionImportSpace(job)
}

// provisionImportSpace stands up a new Space for an import, tolerating a crash at any point.
//
// The sequence is ordered so that every window a crash can land in is recoverable. A channel id is persisted
// before it is used for anything, so a channel this job created is always findable. The Space row is written
// last, because it is the point of no return: before it exists a failure can compensate the channel, and after
// it exists the import owns real user-visible content that must be preserved and reported instead.
//
// One window remains genuinely unavoidable: the process can die after core creates the channel but before the
// returned id reaches the database. A Space channel cannot be looked up by name, so that channel needs an
// operator. The durable attempt row is what makes it visible, and the random channel name is what lets the job
// retry at all — a name derived from the job id would collide with the orphan on every retry, wedging the job
// permanently instead of leaving one channel to clean up.
func (s *Service) provisionImportSpace(job *model.ImportJob) (*model.ImportJob, error) {
	channelID, err := s.resolveImportChannel(job)
	if err != nil {
		return nil, err
	}

	// Membership is added before the Space row, and rechecked around it: a Space whose creator is not a member
	// of its backing channel is unreachable to everyone, its creator included.
	if appErr := s.requireImportTargetStillAuthorized(job, job.ActorId); appErr != nil {
		return nil, appErr
	}
	if _, addErr := s.client.Channel.AddMember(channelID, job.ActorId); addErr != nil {
		return nil, errors.Wrap(addErr, "add the import actor to the new Space channel")
	}

	attempts, err := s.store.GetImportChannelAttempts(job.Id)
	if err != nil {
		return nil, errors.Wrap(err, "load import channel attempts")
	}
	attached, err := s.store.AttachImportSpace(store.ImportSpaceAttachment{
		JobID:       job.Id,
		AttemptID:   attemptIDForChannel(attempts, channelID),
		ChannelID:   channelID,
		Title:       job.ConfirmedSpaceTitle,
		Description: job.ConfirmedSpaceDescription,
		DisplayName: job.SelectedSourceDisplayName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "attach the imported Space")
	}
	s.log.Info("Import provisioned a new Space",
		"job_id", attached.Id, "actor_id", attached.ActorId, "team_id", attached.TeamId,
		"space_id", attached.TargetSpaceId, "channel_id", channelID)
	// The Space's creation is announced like any other, so it appears in clients before its pages arrive.
	s.publishToChannels(wsEventSpaceCreated, map[string]any{"space_id": attached.TargetSpaceId}, channelID)
	return attached, nil
}

// resolveImportChannel returns the backing channel for the job's new Space, creating one only if this job has
// not already recorded one.
//
// The stored id is trusted only after it resolves to a Space channel in the right team. Reusing a recorded id
// blindly would let a job whose channel had since been deleted write its Space against a channel that no
// longer exists.
func (s *Service) resolveImportChannel(job *model.ImportJob) (string, error) {
	if appErr := s.requireClient("resolveImportChannel", "job_id", job.Id); appErr != nil {
		return "", appErr
	}
	if job.ProvisionedChannelId != "" {
		channel, err := s.client.Channel.GetChannelOfType(job.ProvisionedChannelId, mmmodel.ChannelTypeSpace)
		if err == nil && channel != nil && channel.TeamId == job.TeamId && channel.DeleteAt == 0 {
			return job.ProvisionedChannelId, nil
		}
		// A recorded id that no longer resolves is a hard failure rather than grounds for a fresh attempt:
		// creating a second channel while the first may still exist is exactly the orphan this design avoids,
		// and the attempt row already names the first for an operator.
		return "", errors.Errorf("import job %s recorded channel %s, which is no longer a usable Space channel in team %s",
			job.Id, job.ProvisionedChannelId, job.TeamId)
	}

	attemptID := mmmodel.NewId()
	channelName := importChannelName()
	if err := s.store.BeginImportChannelAttempt(job.Id, attemptID, channelName); err != nil {
		return "", errors.Wrap(err, "begin import channel attempt")
	}

	channel := &mmmodel.Channel{
		TeamId:      job.TeamId,
		Type:        mmmodel.ChannelTypeSpace,
		Name:        channelName,
		DisplayName: job.ConfirmedSpaceTitle,
		Header:      job.ConfirmedSpaceDescription,
		CreatorId:   job.ActorId,
	}
	createErr := s.client.Channel.Create(channel)
	if channel.Id == "" {
		// Nothing was created, so nothing needs compensating. The attempt row is marked failed so a later pass
		// can tell a call that never landed from one whose result was lost.
		if stateErr := s.store.SetImportChannelAttemptState(job.Id, attemptID, model.ImportChannelFailed, "create_failed"); stateErr != nil {
			s.log.Warn("Could not record a failed import channel attempt", "job_id", job.Id, "err", stateErr)
		}
		return "", errors.Wrap(createErr, "create the backing channel for the imported Space")
	}

	// The wrapper copies the created channel — including its id — into the argument before its own
	// bookkeeping, so an error alongside a populated id means the channel row exists and must be recorded
	// rather than leaked.
	selected, err := s.store.RecordImportChannelAttemptID(job.Id, attemptID, channel.Id)
	if err != nil {
		// The id is known but unrecorded, which is the one state this design cannot tolerate silently: nothing
		// else will ever find this channel. It is logged at error and left to the operator, and no further
		// creation attempt is made while an unrecorded live id exists.
		s.log.Error("Created a channel for an imported Space but could not record its id; it must be archived manually",
			"job_id", job.Id, "attempt_id", attemptID, "channel_id", channel.Id, "err", err)
		return "", errors.Wrap(err, "record the import channel id")
	}
	if !selected {
		// An earlier attempt's channel is already the selected one, so this is a duplicate. It was recorded as
		// pending compensation, and archived here so it does not linger until terminalization.
		s.log.Warn("Discarding a duplicate channel created for an imported Space",
			"job_id", job.Id, "attempt_id", attemptID, "channel_id", channel.Id)
		if delErr := s.client.Channel.Delete(channel.Id); delErr != nil {
			s.log.Error("Could not archive a duplicate import channel; it must be archived manually",
				"job_id", job.Id, "channel_id", channel.Id, "err", delErr)
		} else if stateErr := s.store.SetImportChannelAttemptState(job.Id, attemptID, model.ImportChannelCompensated, ""); stateErr != nil {
			s.log.Warn("Could not record duplicate import channel compensation", "job_id", job.Id, "err", stateErr)
		}
		refreshed, getErr := s.store.GetImportJob(job.Id)
		if getErr != nil {
			return "", errors.Wrap(getErr, "reload the import job after a duplicate channel")
		}
		return refreshed.ProvisionedChannelId, nil
	}
	if createErr != nil {
		// Creation reported an error but produced a channel. The id is recorded, so the job can proceed with it
		// rather than abandoning a channel that exists.
		s.log.Warn("Channel creation for an imported Space reported an error but produced a channel; continuing with it",
			"job_id", job.Id, "channel_id", channel.Id, "err", createErr)
	}
	return channel.Id, nil
}

// importChannelName generates the opaque channel name one creation attempt uses.
//
// It is random rather than derived from the job: a Space channel cannot be found by name, so a deterministic
// name that collided with an orphan from a lost result would make every retry fail on (TeamId, Name) forever.
func importChannelName() string {
	return "space-" + mmmodel.NewId()[:20]
}

// attemptIDForChannel finds which attempt produced a channel, so attaching the Space can mark that exact row.
// An empty result simply skips the attempt bookkeeping: the Space row is what matters, and a channel with no
// attempt row is one recorded by an earlier release or already reconciled.
func attemptIDForChannel(attempts []*model.ImportChannelAttempt, channelID string) string {
	for _, attempt := range attempts {
		if attempt.ChannelId == channelID {
			return attempt.AttemptId
		}
	}
	return ""
}
