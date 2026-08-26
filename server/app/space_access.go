// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// The low-level access primitives the space subsystem shares: client wiring, and team-membership
// and team-permission resolution. They belong to no single
// caller — space lifecycle (space.go), authorization (permissions.go), and membership
// (space_members.go) each build on them — so they live here rather than in whichever file happened
// to need them first.

// requireClient rejects the operation when the pluginapi client is not wired, which every
// membership-gated space operation depends on. where identifies the calling operation for the
// log line and the returned AppError; kv are its extra log context pairs.
func (s *Service) requireClient(where string, kv ...any) *mmmodel.AppError {
	if s.client != nil {
		return nil
	}
	s.log.Warn("pluginapi client not wired; denying access", append([]any{"operation", where}, kv...)...)
	return mmmodel.NewAppError(where, "app.space.client_not_wired.app_error", nil, "", http.StatusInternalServerError)
}

// activeTeamMember returns userID's membership in teamID, or nil when there is none. Core keeps
// removed team members as rows with DeleteAt set — and GetMember returns such a row without error
// — so a missing row and a soft-deleted row both read as "not a member". Space access must check
// this, not just backing-channel membership: leaving a team does not remove a user from the
// team's space channels, so channel membership alone would let a former team member keep using
// known space and page IDs.
func (s *Service) activeTeamMember(teamID, userID string) (*mmmodel.TeamMember, error) {
	member, err := s.client.Team.GetMember(teamID, userID)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if member.DeleteAt != 0 {
		return nil, nil
	}
	return member, nil
}

// isActiveTeamMember reports whether userID currently belongs to teamID, for callers that need
// only the answer and not the roles.
func (s *Service) isActiveTeamMember(teamID, userID string) (bool, error) {
	member, err := s.activeTeamMember(teamID, userID)
	return member != nil, err
}
