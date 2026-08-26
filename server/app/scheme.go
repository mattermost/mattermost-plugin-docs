// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// errUnsupportedSchemeAPI tags a scheme plugin-API call that answered with neither a value nor an
// error. The generated plugin RPC client logs a transport failure and returns the zero
// values, so a server whose plugin API does not implement the call is indistinguishable from a
// successful read of nothing. Dereferencing that nil crashes the plugin process for every request,
// not just this one, so each call site turns it into this error instead.
var errUnsupportedSchemeAPI = errors.New("the server's plugin API did not answer a scheme call; it does not carry the space permission support this plugin requires")

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

// schemeRoles carries the generated role names used for grants and the user role's permissions used
// to project defaults. On a scheme-backed channel, core rejects literal channel_user/channel_admin
// grants.
type schemeRoles struct {
	UserRoleName    string
	AdminRoleName   string
	GuestRoleName   string
	UserPermissions []string
}

// getSchemeRolesForChannel resolves the generated role names of the scheme governing channelID's
// backing channel. Returns store.ErrNotFound when the channel does not exist or carries no scheme,
// so callers translate it with storeAppError/IsErrNotFound.
func (s *Service) getSchemeRolesForChannel(channelID string) (*schemeRoles, error) {
	if channelID == "" {
		return nil, &store.ErrInvalidInput{Entity: "Channel", Field: "id", Value: channelID}
	}

	channelScheme, err := s.client.Scheme.GetForChannel(channelID)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, &store.ErrNotFound{EntityName: "ChannelScheme", ID: channelID}
		}
		return nil, err
	}
	// The generated RPC client returns nil with no error when this plugin API is absent.
	if channelScheme == nil || channelScheme.Scheme == nil || channelScheme.GuestRole == nil ||
		channelScheme.UserRole == nil || channelScheme.AdminRole == nil {
		return nil, errUnsupportedSchemeAPI
	}

	return &schemeRoles{
		UserRoleName:    channelScheme.UserRole.Name,
		AdminRoleName:   channelScheme.AdminRole.Name,
		GuestRoleName:   channelScheme.GuestRole.Name,
		UserPermissions: channelScheme.UserRole.Permissions,
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

// rolesFromScheme names the three roles core generated for scheme.
func rolesFromScheme(scheme *mmmodel.Scheme) *schemeRoles {
	return &schemeRoles{
		UserRoleName:  scheme.DefaultChannelUserRole,
		AdminRoleName: scheme.DefaultChannelAdminRole,
		GuestRoleName: scheme.DefaultChannelGuestRole,
	}
}
