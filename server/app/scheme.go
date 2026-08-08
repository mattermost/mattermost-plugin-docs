// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"slices"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// permissionSetsEqual reports whether two permission sets hold the same ids, disregarding order.
// The sets being compared are built differently — a generated user role leads with the baseline
// read followed by the sorted capabilities, the admin role follows core's declaration order, and a
// stored role comes back in whatever order it was last written.
func permissionSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA, sortedB := slices.Clone(a), slices.Clone(b)
	slices.Sort(sortedA)
	slices.Sort(sortedB)
	return slices.Equal(sortedA, sortedB)
}

// A non-preset default-capability set resolves to a scheme in a shared pool keyed by that set, so
// the number of schemes is bounded by the capability vocabulary rather than by the number of
// spaces: every space configured the same way points at one scheme. A pooled scheme is never
// deleted — nothing owns it — so there is no retirement, no reference counting, and no residue
// from an interrupted create.
//
// Core accepts a scheme name as proof of space scope only for the reserved preset names
// (model.SchemeNameForDefaultCapabilities recognizes them); a pooled scheme proves its scope by
// having a space backing channel point at it instead.

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

// getSchemeByName returns the scheme with the given name.
func (s *Service) getSchemeByName(name string) (*mmmodel.Scheme, error) {
	scheme, err := s.client.Scheme.GetByName(name)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return nil, &store.ErrNotFound{EntityName: "Scheme", ID: name}
		}
		return nil, err
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
		if scopeErr := s.adoptableSharedScheme(scheme, capabilities); scopeErr != nil {
			return "", nil, scopeErr
		}
		// Logged because the configure that follows rewrites roles every space already on this
		// scheme resolves against, not just this caller's space. The write is idempotent while the
		// capability-to-permission mapping is correct, so a mapping regression is otherwise
		// indistinguishable from ordinary traffic until its effects show up across those spaces.
		s.log.Debug("resolved an existing pooled space scheme; its role write applies to every space sharing it",
			"scheme_id", scheme.Id, "scheme_name", name)
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
			if scopeErr := s.adoptableSharedScheme(existing, capabilities); scopeErr != nil {
				return "", nil, scopeErr
			}
			return existing.Id, rolesFromScheme(existing), nil
		}
		return "", nil, err
	}
	return scheme.Id, rolesFromScheme(scheme), nil
}

// adoptableSharedScheme validates a scheme found under a pooled name before the pool adopts it.
// configureSharedScheme rewrites an adopted scheme's roles wholesale, so a scheme that merely
// occupies the name — creatable only by an actor with scheme-management privilege outside this
// plugin — must not silently have its roles overwritten and be pointed at by a space.
//
// The scope check is the hard gate: a channel cannot reference a non-channel scheme, so adopting
// one could never work. A display-name mismatch only warns: the display name is operator-facing
// and renameable in the System Console, so refusing on it would permanently brick every space
// using that capability set on an ordinary rename — while as an identity signal it is forgeable by
// exactly the actors it would exclude, who can already edit schemes and roles at will.
func (s *Service) adoptableSharedScheme(scheme *mmmodel.Scheme, capabilities []string) error {
	if scheme.Scope != mmmodel.SchemeScopeChannel {
		return errors.New("scheme " + scheme.Name + " under the pooled name has scope " + scheme.Scope + "; the pool only adopts channel-scoped schemes")
	}
	if expected := model.SharedSchemeDisplayNameForCapabilities(capabilities); scheme.DisplayName != expected {
		s.log.Warn("adopting a pooled space scheme whose display name is not the pool's; its roles will be rewritten to the pool's permission sets",
			"scheme_id", scheme.Id, "scheme_name", scheme.Name, "display_name", scheme.DisplayName, "expected_display_name", expected)
	}
	return nil
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
// It must run only once a space backing channel already points at that scheme: core allows a role
// write carrying space permissions for the roles of a reserved preset scheme (see the pool comment
// above), or for a scheme a space backing channel already references, and it does not accept a
// caller-chosen scheme name as proof.
// Idempotent, so re-running it against an already-configured pooled scheme is a no-op in effect.
// changed reports whether any role's stored permission set was actually rewritten, so a caller that
// only runs this as a no-op recovery check (the space's channel already points at the target scheme)
// can tell a real permission change from an already-correct set and broadcast accordingly.
//
// The User role is written LAST, not first: SetSpaceDefaultCapabilities' no-op shortcut projects a
// space's default capabilities from the User role alone (spaceDefaultCapabilitiesFromChannel) and
// treats a projection matching the request as proof the scheme is fully configured, skipping the
// recovery call that would otherwise re-run this. Writing Admin and Guest before User makes that
// projection true only once both have already landed, so a mid-loop failure can never leave the
// admin/guest roles stranded at core's broader channel defaults behind a User role that reads as
// already correct.
func (s *Service) configureSharedScheme(roles *schemeRoles, capabilities []string) (changed bool, err error) {
	capabilities = model.NormalizeCapabilitySet(capabilities)
	roleSets := []struct {
		name  string
		perms []string
	}{
		{roles.AdminRoleName, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions)},
		{roles.GuestRoleName, []string{model.CapabilityReadPage}},
		{roles.UserRoleName, append([]string{model.CapabilityReadPage}, capabilities...)},
	}
	for _, rs := range roleSets {
		rsChanged, err := s.setRolePermissions(rs.name, rs.perms)
		if err != nil {
			return changed, err
		}
		changed = changed || rsChanged
	}
	return changed, nil
}

// setRolePermissions replaces the named role's permission set with permissions. Returns
// store.ErrNotFound for a missing role, matching getRolePermissionsByName's translation of the same
// lookup so both surface the same status through storeAppError. changed reports whether the stored
// set differed and was actually patched.
func (s *Service) setRolePermissions(roleName string, permissions []string) (changed bool, err error) {
	role, err := s.client.Role.GetByName(roleName)
	if err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return false, &store.ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return false, err
	}
	// configureSharedScheme runs on every resolution, not only when the scheme is created, so that a
	// scheme a racing caller left mid-configuration still converges. Once the stored set matches,
	// rewriting it would invalidate the role in core's cache on every node, for every space sharing
	// this pooled scheme, so a matching set is left as it stands.
	if permissionSetsEqual(role.Permissions, permissions) {
		return false, nil
	}
	// Patched by id rather than by handing back the role just read: core re-reads the stored role
	// so its scope guard judges a SchemeId the caller cannot influence.
	if _, err = s.client.Role.Patch(role.Id, &mmmodel.RolePatch{Permissions: &permissions}); err != nil {
		if errors.Is(err, pluginapi.ErrNotFound) {
			return false, &store.ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return false, err
	}
	return true, nil
}
