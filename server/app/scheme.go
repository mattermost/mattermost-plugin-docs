// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"fmt"
	"slices"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// errPooledSchemeNotConforming tags a scheme found under a pooled name that the pool cannot adopt:
// a non-channel scope, or generated roles granting something the name does not imply — an ordinary
// channel scheme occupying the name, or a pooled scheme edited outside this plugin. The pool
// refuses it rather than rewriting roles that govern
// channels beyond the caller's space, so this surfaces as a server-state condition an admin must
// resolve, not as a caller error.
var errPooledSchemeNotConforming = errors.New("scheme under a pooled space name does not carry the pool's permission sets")

// errUnsupportedSchemeAPI tags a scheme or role plugin-API call that answered with neither a value
// nor an error. The generated plugin RPC client logs a transport failure and returns the zero
// values, so a server whose plugin API does not implement the call is indistinguishable from a
// successful read of nothing. Dereferencing that nil crashes the plugin process for every request,
// not just this one, so each call site turns it into this error instead.
var errUnsupportedSchemeAPI = errors.New("the server's plugin API did not answer a scheme or role call; it does not carry the space permission support this plugin requires")

// spacePermissionSetsEqual reports whether two sets carry the same SPACE permissions, ignoring the
// channel permissions a role read carries beyond them. Every comparison against a role read has to
// go through this rather than permissionSetsEqual: see model.SpacePermissionsOnly for what core
// adds to a role on the way out.
func spacePermissionSetsEqual(a, b []string) bool {
	return permissionSetsEqual(model.SpacePermissionsOnly(a), model.SpacePermissionsOnly(b))
}

// permissionSetsEqual reports whether two permission sets hold the same ids, disregarding order.
// The sets being compared are built differently — a generated user role leads with the baseline
// read followed by the sorted permissions, the admin role follows core's declaration order, and a
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

// A non-preset default-permission set resolves to a scheme in a shared pool keyed by that set, so
// the number of schemes is bounded by the permission vocabulary rather than by the number of
// spaces: every space configured the same way points at one scheme. A pooled scheme is never
// deleted — nothing owns it — so there is no retirement, no reference counting, and no residue
// from an interrupted create.
//
// What makes that bound worth the pooling, rather than giving each space its own scheme: core
// generates three roles per channel scheme, those roles are read on every permission check, and
// they share one bounded LRU with every other role on the server (RoleCacheSize, 20000 entries).
// Creating or deleting a scheme also purges the role, role-permission, and channel caches
// cluster-wide. Confluence Data Center — whose deployments this feature is sized for — recommends a
// ceiling of 8000 spaces, so a scheme per space is up to 24000 generated roles against that 20000
// cap, with a cluster-wide purge on every space reconfiguration. Pooling holds the permission-check
// working set at three roles per distinct permission set: at most 32 sets exist, so at most 96
// roles, whatever the space count. That is the whole reason for the digest naming below; without it
// this reads as gratuitous deduplication of cheap rows.
//
// Core accepts a scheme name as proof of space scope only for the reserved preset names
// (model.SchemeNameForDefaultPermissions recognizes them); a pooled scheme proves its scope by
// having a space backing channel point at it instead.

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

// getOrCreateSharedScheme resolves the pooled channel scheme expressing a permission set, creating it
// on first use. The name is a pure function of the permission set (see
// model.SharedSchemeNameForPermissions), so every space configured that way resolves to the same
// scheme: two schemes carrying one permission set would be indistinguishable in behaviour, since
// nothing reads a scheme id or a generated role name for meaning.
//
// The returned roles start on core's default channel baseline when the scheme is new, and the
// caller gives them their permission sets through configureSharedScheme once a backing channel
// points at the scheme. That configure runs on every resolution, not only on creation: a scheme
// created by a racing caller may still be mid-configuration when this one finds it, and rewriting
// an already-correct permission set is idempotent.
func (s *Service) getOrCreateSharedScheme(permissions []string) (string, *schemeRoles, error) {
	name := model.SharedSchemeNameForPermissions(permissions)
	if scheme, err := s.getSchemeByName(name); err == nil {
		if scopeErr := s.adoptableSharedScheme(scheme, permissions); scopeErr != nil {
			return "", nil, scopeErr
		}
		// adoptableSharedScheme has established that every role either already holds the set this
		// name implies or is still empty, so the configure that follows either writes nothing or
		// finishes an unconfigured scheme. It cannot change what this scheme means for the spaces
		// already sharing it.
		s.log.Debug("resolved an existing pooled space scheme", "scheme_id", scheme.Id, "scheme_name", name)
		return scheme.Id, rolesFromScheme(scheme), nil
	} else if !store.IsErrNotFound(err) {
		return "", nil, err
	}

	scheme, err := s.client.Scheme.Create(&mmmodel.Scheme{
		Name:        name,
		DisplayName: model.SharedSchemeDisplayNameForPermissions(permissions),
		Scope:       mmmodel.SchemeScopeChannel,
	})
	if err != nil {
		// The name is unique, so a concurrent first use of the same permission set loses this
		// create and adopts the winner's scheme rather than failing the caller.
		if existing, getErr := s.getSchemeByName(name); getErr == nil {
			if scopeErr := s.adoptableSharedScheme(existing, permissions); scopeErr != nil {
				return "", nil, scopeErr
			}
			return existing.Id, rolesFromScheme(existing), nil
		}
		return "", nil, err
	}
	if scheme == nil {
		return "", nil, errUnsupportedSchemeAPI
	}
	return scheme.Id, rolesFromScheme(scheme), nil
}

// adoptableSharedScheme validates a scheme found under a pooled name before the pool adopts it.
// Adoption is what makes pooling safe or unsafe: the name is derived from the permission set, so
// unlike a scheme looked up by an id this caller created, whatever sits at that name is unverified
// input. configureSharedScheme then rewrites an adopted scheme's roles, and those roles govern every
// channel referencing that scheme — so adopting the wrong scheme does not merely misconfigure this
// space, it rewrites authority elsewhere.
//
// A pooled scheme's SPACE permissions are therefore required to be a pure function of its name:
// adopt only what already conforms, never repair a stranger into conformance. Each role must carry
// either
//
//   - exactly the space permissions pooledRoleSets requires, or
//   - none, meaning no configure has replaced core's own channel-scope default yet — either this
//     caller's own create a moment ago, or a racing caller's still in flight, or a run that failed
//     partway. configureSharedScheme finishes all three.
//
// Only the space permissions are judged, because they are the only ones this plugin owns. Core seeds
// a new channel scheme's User and Guest roles with the moderated subset of the built-in role and
// merges the non-moderated ones back in on every read, so a role read always carries channel
// permissions the plugin never wrote and cannot control (see model.SpacePermissionsOnly). Judging a
// role by those made every pooled set unadoptable — the second space to request one got a 500.
//
// Any other space-permission set means the scheme is not the pool's — the realistic case being a
// pooled scheme edited in the System Console to widen what its spaces grant — and it is refused
// rather than overwritten.
//
// What that refusal does and does not buy is worth stating exactly, because the difference decides
// where to look when a space grants something unexpected. It stops THIS resolution from adopting and
// rewriting a tampered scheme. It does not undo the tampering or contain it: a role patch is already
// in force for every channel on that scheme the moment it is saved, composed by core on the next
// permission check, and nothing here runs on that path. The refusal is logged for that reason — it
// is the only signal the tampering happened, and it surfaces on an unrelated caller's request.
//
// A scheme carrying no space permissions is indistinguishable from this plugin's own unconfigured
// one, so an ordinary channel scheme occupying a pooled name is adopted rather than refused.
// Occupying one takes the sha256 digest of the exact permission set plus authority to create a
// scheme at all, which is authority to edit any role directly; the alternative — refusing both —
// is what broke the pool.
//
// The scope check stays a hard gate for a different reason: a channel cannot reference a
// non-channel scheme, so adopting one could never work. A display-name mismatch only warns — the
// display name is operator-facing and renameable, so refusing on it would brick every space using
// that permission set on an ordinary rename, and as an identity signal it is forgeable by exactly
// the actors it would exclude.
func (s *Service) adoptableSharedScheme(scheme *mmmodel.Scheme, permissions []string) error {
	if scheme.Scope != mmmodel.SchemeScopeChannel {
		return fmt.Errorf("%w: scheme %s under the pooled name has scope %s; the pool only adopts channel-scoped schemes", errPooledSchemeNotConforming, scheme.Name, scheme.Scope)
	}
	if expected := model.SharedSchemeDisplayNameForPermissions(permissions); scheme.DisplayName != expected {
		s.log.Warn("a scheme under a pooled space name does not carry the pool's display name",
			"scheme_id", scheme.Id, "scheme_name", scheme.Name, "display_name", scheme.DisplayName, "expected_display_name", expected)
	}
	for _, rs := range pooledRoleSets(rolesFromScheme(scheme), permissions) {
		stored, err := s.getRolePermissionsByName(rs.name)
		if err != nil {
			return err
		}
		// Compared on the space permissions alone. A role read carries core's own channel
		// permissions too, so a raw comparison never matches and no scheme is ever adoptable;
		// filtering to the space permissions is also what makes "empty" mean what the arms above
		// say it means, since an unconfigured role's seeded and merged-in permissions are all
		// channel ones.
		storedSpace := model.SpacePermissionsOnly(stored)
		if len(storedSpace) == 0 || permissionSetsEqual(storedSpace, model.SpacePermissionsOnly(rs.perms)) {
			continue
		}
		// Logged, not just returned: this error fails whichever caller happened to resolve the
		// pool next, whose own request is unrelated to the tampering, so the returned error reaches
		// the wrong person. The log is what puts the scheme and role in front of an operator.
		s.log.Warn("refusing to adopt a scheme under a pooled space name: its role grants space permissions the name does not imply",
			"scheme_id", scheme.Id, "scheme_name", scheme.Name, "role_name", rs.name,
			"stored_space_permissions", storedSpace, "expected_space_permissions", model.SpacePermissionsOnly(rs.perms))
		return fmt.Errorf("%w: role %s of scheme %s grants a set the pooled name does not imply",
			errPooledSchemeNotConforming, rs.name, scheme.Name)
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

// roleSet pairs a generated role name with the permission set a pooled scheme requires it to carry.
type roleSet struct {
	name  string
	perms []string
}

// pooledRoleSets states what each of a pooled scheme's three generated roles must grant for
// permissions. It is the single definition of that mapping: configureSharedScheme writes these sets
// and adoptableSharedScheme verifies against them, and a second spelling of the mapping would let
// the guard start checking something other than what the writer lands.
//
// The order is load-bearing — User comes LAST. SetSpaceDefaultPermissions' no-op shortcut reads the
// User role to decide whether the scheme is fully configured, so Admin and Guest must already have
// landed by the time the User role reads as correct; otherwise a mid-loop failure could leave them
// stranded at core's broader channel defaults behind a User role that looks done.
func pooledRoleSets(roles *schemeRoles, permissions []string) []roleSet {
	return []roleSet{
		{roles.AdminRoleName, mmmodel.PermissionIDs(mmmodel.SpaceAdminRolePermissions)},
		{roles.GuestRoleName, []string{mmmodel.PermissionReadPage.Id}},
		{roles.UserRoleName, pooledUserRolePermissions(permissions)},
	}
}

// pooledUserRolePermissions is the set a pooled scheme's generated User role must grant for
// permissions: the implicit baseline read plus the space's default set. Split out of pooledRoleSets
// because SetSpaceDefaultPermissions' no-op shortcut compares a stored User role against it
// directly, and that comparison has to be against the same set the writer lands — comparing against
// a projection of it is what let an over-privileged role read as unchanged.
func pooledUserRolePermissions(permissions []string) []string {
	return append([]string{mmmodel.PermissionReadPage.Id}, model.NormalizePermissions(permissions)...)
}

// configureSharedScheme writes the permission sets of the three roles generated for a pooled
// scheme, so members of a space pointing at it hold exactly permissions plus the baseline read.
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
// The sets and their write order come from pooledRoleSets, which explains why User is written last.
func (s *Service) configureSharedScheme(roles *schemeRoles, permissions []string) (changed bool, err error) {
	for _, rs := range pooledRoleSets(roles, permissions) {
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
	// scheme a racing caller left mid-configuration still converges. A matching set is left in
	// place, avoiding a role-cache invalidation on every node for every space sharing this pooled
	// scheme.
	if spacePermissionSetsEqual(role.Permissions, permissions) {
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
