// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// A non-preset default-capability set resolves to a scheme in a shared pool keyed by that set, so
// the number of schemes is bounded by the capability vocabulary rather than by the number of
// spaces: every space configured the same way points at one scheme. A pooled scheme is never
// deleted — nothing owns it — so there is no retirement, no reference counting, and no residue
// from an interrupted create.
//
// Core accepts a scheme name as proof of space scope only for the three reserved preset names; a
// pooled scheme proves its scope by having a space backing channel point at it instead.

// schemeRoles is the generated channel-scheme role names governing one backing channel's scheme.
// Space capability grants reference these generated names, not the literal
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
	// resolving to team roles that grant no page capabilities.
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

// getSchemeIDByName returns the id of the scheme with the given name.
func (s *Service) getSchemeIDByName(name string) (string, error) {
	scheme, err := s.client.Scheme.GetByName(name)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return "", &store.ErrNotFound{EntityName: "Scheme", ID: name}
		}
		return "", err
	}
	return scheme.Id, nil
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
	return role.Permissions, nil
}

// getOrCreateSharedScheme resolves the pooled channel scheme expressing capabilities, creating it
// on first use. The name is a pure function of the capability set (see
// model.SharedSchemeNameForCapabilities), so every space configured that way resolves to the same
// scheme: two schemes carrying one capability set would be indistinguishable in behaviour, since
// nothing reads a scheme id or a generated role name for meaning.
//
// The returned roles start on core's default channel baseline when the scheme is new, and the
// caller gives them their permission sets through configureSharedScheme once a backing channel
// points at the scheme. That configure runs on every resolution, not only on creation: a scheme
// created by a racing caller may still be mid-configuration when this one finds it, and rewriting
// an already-correct permission set is idempotent.
func (s *Service) getOrCreateSharedScheme(capabilities []string) (string, *schemeRoles, error) {
	name := model.SharedSchemeNameForCapabilities(capabilities)
	if scheme, err := s.client.Scheme.GetByName(name); err == nil {
		return scheme.Id, rolesFromScheme(scheme), nil
	} else if !errors.Is(err, pluginapi.ErrNotFound) {
		return "", nil, err
	}

	scheme, err := s.client.Scheme.Create(&mmmodel.Scheme{
		Name:        name,
		DisplayName: model.SharedSchemeDisplayNameForCapabilities(capabilities),
		Scope:       mmmodel.SchemeScopeChannel,
	})
	if err != nil {
		// The name is unique, so a concurrent first use of the same capability set loses this
		// create and adopts the winner's scheme rather than failing the caller.
		if existing, getErr := s.client.Scheme.GetByName(name); getErr == nil {
			return existing.Id, rolesFromScheme(existing), nil
		}
		return "", nil, err
	}
	return scheme.Id, rolesFromScheme(scheme), nil
}

// rolesFromScheme names the three roles core generated for scheme.
func rolesFromScheme(scheme *mmmodel.Scheme) *schemeRoles {
	return &schemeRoles{
		UserRoleName:  scheme.DefaultChannelUserRole,
		AdminRoleName: scheme.DefaultChannelAdminRole,
		GuestRoleName: scheme.DefaultChannelGuestRole,
	}
}

// configureSharedScheme writes the permission sets of the three roles generated for a pooled
// scheme, so members of a space pointing at it hold exactly capabilities plus the baseline read.
// roles are the names getOrCreateSharedScheme returned, so the writes land on the resolved scheme
// rather than on whatever a channel currently points at.
//
// It must run only once a space backing channel already points at that scheme: core admits a role
// write carrying space permissions for a seeded preset's roles, or for a scheme a space backing
// channel already references, and it does not accept a caller-chosen scheme name as proof.
// Idempotent, so re-running it against an already-configured pooled scheme is a no-op in effect.
func (s *Service) configureSharedScheme(roles *schemeRoles, capabilities []string) error {
	capabilities = model.NormalizeCapabilitySet(capabilities)
	roleSets := []struct {
		name  string
		perms []string
	}{
		{roles.UserRoleName, append([]string{model.CapabilityReadPage}, capabilities...)},
		{roles.AdminRoleName, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions)},
		{roles.GuestRoleName, []string{model.CapabilityReadPage}},
	}
	for _, rs := range roleSets {
		if err := s.setRolePermissions(rs.name, rs.perms); err != nil {
			return err
		}
	}
	return nil
}

// setRolePermissions replaces the named role's permission set with permissions. Returns
// store.ErrNotFound for a missing role, matching getRolePermissionsByName's translation of the same
// lookup so both surface the same status through storeAppError.
func (s *Service) setRolePermissions(roleName string, permissions []string) error {
	role, err := s.client.Role.GetByName(roleName)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return &store.ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return err
	}
	// Patched by id rather than by handing back the role just read: core re-reads the stored role
	// so its scope guard judges a SchemeId the caller cannot influence.
	if _, err = s.client.Role.Patch(role.Id, &mmmodel.RolePatch{Permissions: &permissions}); err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return &store.ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return err
	}
	return nil
}
