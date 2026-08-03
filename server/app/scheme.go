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

// spaceCustomSchemeDisplayName is the DisplayName of every space-private custom scheme: one
// immutable scheme per non-preset default-capability set.
const spaceCustomSchemeDisplayName = "Space Custom Scheme"

// spaceCustomSchemeNamePrefix labels the Name of every space-private custom scheme this plugin
// creates. It is an operator-facing label only: core accepts a scheme name as proof of space scope
// solely for the three reserved preset names, and a per-space custom scheme proves its scope by
// having a space backing channel point at it instead.
const spaceCustomSchemeNamePrefix = "docs_space_custom_"

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

// isPresetSchemeID reports whether schemeID is one of the three seeded preset schemes, by resolving
// each reserved name to its id. Preset membership is settled by identity rather than by projecting
// the scheme's capability set, because a custom scheme whose generated roles were never given their
// permission sets still carries core's default channel baseline, which projects to the same empty
// set as the read-only preset.
func (s *Service) isPresetSchemeID(schemeID string) (bool, error) {
	for _, name := range mmmodel.SpaceSchemeNames {
		presetID, err := s.getSchemeIDByName(name)
		if err != nil {
			return false, err
		}
		if presetID == schemeID {
			return true, nil
		}
	}
	return false, nil
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

// createSpaceCustomScheme creates one space-private channel scheme and returns its id along with
// the three roles core generated for it. Those roles start on the default channel baseline; they
// are given their exact permission sets later by configureSpaceCustomScheme, which cannot run until
// a space backing channel points at the scheme. Until the caller attaches it, the scheme is
// referenced by nothing, so a caller whose next step fails retires it directly.
func (s *Service) createSpaceCustomScheme() (string, *schemeRoles, error) {
	scheme, err := s.client.Scheme.Create(&mmmodel.Scheme{
		Name:        spaceCustomSchemeNamePrefix + mmmodel.NewId(),
		DisplayName: spaceCustomSchemeDisplayName,
		Scope:       mmmodel.SchemeScopeChannel,
	})
	if err != nil {
		return "", nil, err
	}
	return scheme.Id, &schemeRoles{
		UserRoleName:  scheme.DefaultChannelUserRole,
		AdminRoleName: scheme.DefaultChannelAdminRole,
		GuestRoleName: scheme.DefaultChannelGuestRole,
	}, nil
}

// configureSpaceCustomScheme replaces the permission sets of the three roles generated for a
// space-private custom scheme, so the space's members hold exactly capabilities plus the baseline
// read. roles are the names createSpaceCustomScheme returned, so the writes land on the scheme that
// was created rather than on whatever a channel currently resolves to.
//
// It must run only once a space backing channel already points at that scheme: core admits a role
// write carrying space permissions for a seeded preset's roles, or for a scheme a space backing
// channel already references, and it does not accept a caller-chosen scheme name as proof.
func (s *Service) configureSpaceCustomScheme(roles *schemeRoles, capabilities []string) error {
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
	if _, err = s.client.Role.Patch(role, &mmmodel.RolePatch{Permissions: &permissions}); err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return &store.ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return err
	}
	return nil
}

// retireSpaceCustomScheme deletes a space-private custom scheme once its space no longer uses it.
// Core refuses to delete a seeded preset, and refuses any scheme a space backing channel still
// references, so a caller must have repointed the channel to another scheme first; the abandon path
// uses detachAndDeleteCustomScheme instead. A scheme already gone is a no-op.
func (s *Service) retireSpaceCustomScheme(schemeID string) error {
	if _, err := s.client.Scheme.Delete(schemeID); err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// detachAndDeleteCustomScheme retires the custom scheme a failed CreateSpace left behind. The
// doomed backing channel still points at the scheme, and core counts even a soon-to-be-archived
// space channel as a live reference that blocks the delete, so the channel's scheme reference is
// cleared first through Channel.Update and only then is the now-unreferenced scheme deleted.
//
// Best-effort: any failure is logged, since the caller is already unwinding an earlier error.
func (s *Service) detachAndDeleteCustomScheme(channelID, schemeID string) {
	channel, err := s.client.Channel.GetChannelOfType(channelID, mmmodel.ChannelTypeSpace)
	switch {
	case errors.Is(err, pluginapi.ErrNotFound):
		// No channel row exists, so nothing references the scheme and the delete below succeeds
		// without a detach.
	case err != nil:
		s.log.Error("failed to load abandoned backing channel to detach its custom scheme; the scheme may leak and must be deleted manually", "channel_id", channelID, "scheme_id", schemeID, "err", err)
		return
	case channel != nil && channel.SchemeId != nil && *channel.SchemeId == schemeID:
		channel.SchemeId = nil
		if updErr := s.client.Channel.Update(channel); updErr != nil {
			s.log.Error("failed to detach custom scheme from abandoned backing channel; the scheme may leak and must be deleted manually", "channel_id", channelID, "scheme_id", schemeID, "err", updErr)
			return
		}
	}

	if err := s.retireSpaceCustomScheme(schemeID); err != nil {
		s.log.Error("failed to retire space custom scheme after a failed create; it must be deleted manually", "scheme_id", schemeID, "err", err)
	}
}
