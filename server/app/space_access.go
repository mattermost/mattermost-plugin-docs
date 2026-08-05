// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// The low-level access primitives the space subsystem shares: client wiring, team-membership and
// team-permission resolution, and backing-channel member iteration. They belong to no single
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
	s.log.Error("pluginapi client not wired; denying access", append([]any{"operation", where}, kv...)...)
	return mmmodel.NewAppError(where, "app.space.client_not_wired.app_error", nil, "", http.StatusInternalServerError)
}

// activeTeamMember returns userID's membership in teamID, or nil when there is none. Core keeps
// removed team members as rows with DeleteAt set — and GetMember returns such a row without error
// — so a missing row and a soft-deleted row both read as "not a member". Space access must check
// this, not just backing-channel membership: leaving a team does not remove a user from the
// team's space channels, so channel membership alone would let a former team member keep using
// known space and page IDs.
//
// The membership carries the team roles, so a caller that needs both "is a member" and "holds a
// team permission" answers both from this one row via teamPermGranted.
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

// teamPermGranted reports whether userID holds perm on teamID, answering from member's roles when
// that already settles it. member is a membership the caller has resolved, or nil.
//
// The roles on an already-resolved membership answer the granting case directly. A negative is not
// conclusive — core also honours a system-role grant the team roles cannot express — so it defers
// to core rather than denying, which keeps the outcome identical to calling HasPermissionToTeam
// directly.
func (s *Service) teamPermGranted(member *mmmodel.TeamMember, userID, teamID string, perm *mmmodel.Permission) bool {
	if member != nil && s.client.User.RolesGrantPermission(member.GetRoles(), perm.Id) {
		return true
	}
	return s.client.User.HasPermissionToTeam(userID, teamID, perm)
}

// forEachChannelMember visits every member of channelID page by page. Iteration ends early
// when visit returns stop=true or an error; the error is returned as-is.
func (s *Service) forEachChannelMember(channelID string, visit func(cm *mmmodel.ChannelMember) (stop bool, err error)) error {
	for page := 0; ; page++ {
		members, err := s.client.Channel.ListMembers(channelID, page, PerPageMaximum)
		if err != nil {
			return err
		}
		for _, cm := range members {
			stop, visitErr := visit(cm)
			if visitErr != nil {
				return visitErr
			}
			if stop {
				return nil
			}
		}
		if len(members) < PerPageMaximum {
			return nil
		}
	}
}

// otherAuthorizedMembers answers both reachability questions the membership guards ask — is there
// another member who can still reach the space, and is one of them an admin — in a single walk,
// disregarding excludeUserID (the member being demoted or removed). Former team members keep their
// channel-member rows after leaving the team, so counting raw rows would let the last reachable
// member be removed and leave the space stranded behind members who all fail the team half of the
// access gate. Iteration stops once both answers are known, and a row that cannot change either
// answer is skipped before its team lookup.
func (s *Service) otherAuthorizedMembers(space *model.Space, excludeUserID string) (anyMember, anyAdmin bool, err error) {
	err = s.forEachChannelMember(space.ChannelId, func(cm *mmmodel.ChannelMember) (bool, error) {
		if cm.UserId == excludeUserID {
			return false, nil
		}
		// Once a reachable member is known, only an admin row can still teach us anything.
		if anyMember && !cm.SchemeAdmin {
			return false, nil
		}
		active, activeErr := s.isActiveTeamMember(space.TeamId, cm.UserId)
		if activeErr != nil {
			return false, activeErr
		}
		if !active {
			return false, nil
		}
		anyMember = true
		if cm.SchemeAdmin {
			anyAdmin = true
		}
		return anyMember && anyAdmin, nil
	})
	if err != nil {
		return false, false, err
	}
	return anyMember, anyAdmin, nil
}
