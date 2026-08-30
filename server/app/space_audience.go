// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// spaceAudience is a space backing channel's membership split by whether each member can still
// reach the space: an active (non-deactivated) account holding the team half of the read gate.
// Core makes the permission split: FilterUsersWithTeamPermission resolves team read_space per
// member from the active team membership, its team scheme and the user's system roles, exactly as
// the per-request gates resolve it, so the plugin never derives it from TeamMembers itself.
// Account deactivation is the axis that split cannot see — it revokes sessions but leaves the
// TeamMembers and ChannelMembers rows in place — so it is read alongside the membership and
// resolved here: a deactivated member cannot authenticate, so they are never admitted.
type spaceAudience struct {
	// admitted holds the members who pass, with their SchemeAdmin flag.
	admitted []store.ChannelMemberRef
	// omitted holds the user ids of the members who fail.
	omitted []string
}

// resolveSpaceAudience lists channelID's members and asks core which of them hold team read_space;
// deactivated accounts are omitted without asking. Resolved on every call: a team departure, a
// team-scheme change, or a deactivation has no plugin-visible hook, so a cached answer could not
// be kept current. Requires a wired client.
func (s *Service) resolveSpaceAudience(channelID string) (*spaceAudience, error) {
	membership, err := s.store.GetChannelMembership(channelID)
	if err != nil {
		return nil, err
	}
	audience := &spaceAudience{}
	if len(membership.Members) == 0 {
		return audience, nil
	}
	ids := make([]string, 0, len(membership.Members))
	for _, member := range membership.Members {
		if !member.Deactivated {
			ids = append(ids, member.UserID)
		}
	}
	var granted []string
	if len(ids) > 0 {
		granted, err = s.client.User.FilterWithTeamPermission(membership.TeamID, ids, mmmodel.PermissionReadSpace)
		if err != nil {
			return nil, err
		}
	}
	admitted := make(map[string]bool, len(granted))
	for _, id := range granted {
		admitted[id] = true
	}
	for _, member := range membership.Members {
		if !member.Deactivated && admitted[member.UserID] {
			audience.admitted = append(audience.admitted, member)
		} else {
			audience.omitted = append(audience.omitted, member.UserID)
		}
	}
	return audience, nil
}

// others reports whether the audience holds an admitted member other than excludeUserID
// (anyMember), and whether any such member is a channel scheme admin (anyAdmin).
func (a *spaceAudience) others(excludeUserID string) (anyMember, anyAdmin bool) {
	for _, member := range a.admitted {
		if member.UserID == excludeUserID {
			continue
		}
		anyMember = true
		if member.SchemeAdmin {
			return true, true
		}
	}
	return anyMember, false
}

// admittedIDs returns the user ids of the admitted members.
func (a *spaceAudience) admittedIDs() []string {
	ids := make([]string, len(a.admitted))
	for i, member := range a.admitted {
		ids[i] = member.UserID
	}
	return ids
}
