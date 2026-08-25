// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// errUnsupportedSchemeAPI tags a scheme or role plugin-API call that answered with neither a value
// nor an error. The generated plugin RPC client logs a transport failure and returns the zero
// values, so a server whose plugin API does not implement the call is indistinguishable from a
// successful read of nothing. Dereferencing that nil crashes the plugin process for every request,
// not just this one, so each call site turns it into this error instead.
var errUnsupportedSchemeAPI = errors.New("the server's plugin API did not answer a scheme or role call; it does not carry the space permission support this plugin requires")

// The three functions below are the space tier model: what a plain member, an admin and a guest
// of a space may do. Core creates the scheme with these permission sets but does not define them —
// it validates only that they are channel-scoped — so this is where the space policy lives.

// spaceUserRolePermissions is what a space's plain members hold: the baseline read every member
// has, plus the space's configured default set. permissions must already be normalized, which its
// only caller does before the set is persisted anywhere.
func spaceUserRolePermissions(permissions []string) []string {
	return append([]string{mmmodel.PermissionReadPage.Id}, permissions...)
}

// spaceAdminRolePermissions is what a space admin holds, single-sourced from core's canonical
// admin slice so the tier cannot drift from the permission the admin toggle grants.
func spaceAdminRolePermissions() []string {
	return mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions)
}

// spaceGuestRolePermissions is what a guest of a space holds. The guest tier reads and nothing
// more, whatever the space's defaults are: a guest never gains a page write from the space being
// configured permissively.
func spaceGuestRolePermissions() []string {
	return []string{mmmodel.PermissionReadPage.Id}
}

// schemeRoles is the generated channel-scheme role names governing one backing channel's scheme.
// Space permission grants reference these generated names, not the literal
// channel_user/channel_admin roles: on a scheme-backed channel, core rejects the literal.
type schemeRoles struct {
	UserRoleName  string
	AdminRoleName string
	GuestRoleName string
}

// getSchemeRolesForChannel resolves the generated role names of the scheme governing channelID's
// backing channel. Returns store.ErrNotFound when the channel does not exist or carries no scheme,
// so callers translate it with storeAppError/IsErrNotFound.
func (s *Service) getSchemeRolesForChannel(channelID string) (*schemeRoles, error) {
	if channelID == "" {
		return nil, &store.ErrInvalidInput{Entity: "Channel", Field: "id", Value: channelID}
	}

	channel, err := s.client.Channel.GetChannelOfType(channelID, mmmodel.ChannelTypeSpace)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, &store.ErrNotFound{EntityName: "ChannelScheme", ID: channelID}
		}
		return nil, err
	}
	return s.schemeRolesFromChannel(channelID, channel)
}

// schemeRolesFromChannel is getSchemeRolesForChannel for a caller that already holds the backing
// channel. channelID identifies the channel in the returned not-found errors independently of what
// the channel object carries.
func (s *Service) schemeRolesFromChannel(channelID string, channel *mmmodel.Channel) (*schemeRoles, error) {
	// The scheme reference is checked here rather than inferred from the resolved role names: core's
	// RolesForChannel falls back to the team scheme's channel roles for a channel carrying no scheme
	// of its own, and a space that lost its scheme must report not-found instead of silently
	// resolving to team roles that grant no page permissions.
	if channel == nil || channel.SchemeId == nil || *channel.SchemeId == "" {
		return nil, &store.ErrNotFound{EntityName: "ChannelScheme", ID: channelID}
	}

	guestRole, userRole, adminRole, err := s.client.Scheme.GetRolesForChannel(channelID)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, &store.ErrNotFound{EntityName: "ChannelScheme", ID: channelID}
		}
		return nil, err
	}

	return &schemeRoles{
		UserRoleName:  userRole,
		AdminRoleName: adminRole,
		GuestRoleName: guestRole,
	}, nil
}

// getSchemeByName returns the scheme with the given name.
func (s *Service) getSchemeByName(name string) (*mmmodel.Scheme, error) {
	scheme, err := s.client.Scheme.GetByName(name)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, &store.ErrNotFound{EntityName: "Scheme", ID: name}
		}
		return nil, err
	}
	if scheme == nil {
		return nil, errUnsupportedSchemeAPI
	}
	return scheme, nil
}

// getRolePermissionsByName returns the permission ids granted by the named role.
func (s *Service) getRolePermissionsByName(roleName string) ([]string, error) {
	role, err := s.client.Role.GetByName(roleName)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, &store.ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return nil, err
	}
	if role == nil {
		return nil, errUnsupportedSchemeAPI
	}
	return role.Permissions, nil
}

// rolesFromScheme names the three roles core generated for scheme.
func rolesFromScheme(scheme *mmmodel.Scheme) *schemeRoles {
	return &schemeRoles{
		UserRoleName:  scheme.DefaultChannelUserRole,
		AdminRoleName: scheme.DefaultChannelAdminRole,
		GuestRoleName: scheme.DefaultChannelGuestRole,
	}
}
