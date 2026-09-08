// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// errUnsupportedSchemeAPI tags a scheme plugin-API call that the server does not implement. Newer
// server/public clients report pluginapi.ErrNotSupported explicitly; the nil checks below also
// protect older clients and incomplete responses. Keeping one local error gives callers the same
// behavior across supported server versions.
var errUnsupportedSchemeAPI = errors.New("the server's plugin API did not answer a scheme call; it does not carry the space permission support this plugin requires")

func normalizeUnsupportedSchemeAPI(err error) error {
	if errors.Is(err, pluginapi.ErrNotSupported) {
		return errUnsupportedSchemeAPI
	}
	return err
}

// The three functions below define the space's three roles: what a plain member, an admin, and a
// guest of a space may do. Core creates the scheme with these permission sets but does not define
// them — it validates only that they are channel-scoped.

// spaceUserRolePermissions is what a space's plain members hold: the baseline read every member
// has, plus the space's configured default set. permissions must already be normalized, which its
// only caller does before the set is persisted anywhere.
func spaceUserRolePermissions(permissions []string) []string {
	return append([]string{mmmodel.PermissionReadPage.Id}, permissions...)
}

// spaceAdminRolePermissions is what a space admin holds, single-sourced from core's canonical
// admin slice so the role cannot drift from the permission the admin toggle grants.
func spaceAdminRolePermissions() []string {
	return mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions)
}

// spaceGuestRolePermissions is what a guest of a space holds: read and nothing more, whatever the
// space's defaults are — a guest never gains a page write from the space being configured
// permissively.
func spaceGuestRolePermissions() []string {
	return []string{mmmodel.PermissionReadPage.Id}
}

// schemeRoles carries the generated role names used for grants and the user role's permissions used
// to project defaults. On a scheme-backed channel, core rejects literal channel_user/channel_admin
// grants.
//
// UserPermissions is populated only by getSchemeRolesForChannel, which reads it from an already
// fetched ChannelScheme's roles. rolesFromScheme builds a schemeRoles from a bare *mmmodel.Scheme,
// which carries only role-name strings, so it leaves UserPermissions nil; a caller must not read
// the field on a value that traces back to rolesFromScheme.
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
		return nil, normalizeUnsupportedSchemeAPI(err)
	}
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
		return nil, normalizeUnsupportedSchemeAPI(err)
	}
	if scheme == nil {
		return nil, errUnsupportedSchemeAPI
	}
	return scheme, nil
}

// rolesFromScheme names the three roles core generated for scheme. UserPermissions is left nil:
// scheme carries only role-name strings, not the user role's permissions.
func rolesFromScheme(scheme *mmmodel.Scheme) *schemeRoles {
	return &schemeRoles{
		UserRoleName:  scheme.DefaultChannelUserRole,
		AdminRoleName: scheme.DefaultChannelAdminRole,
		GuestRoleName: scheme.DefaultChannelGuestRole,
	}
}
