// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"strings"

	"github.com/jmoiron/sqlx"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"
)

// spaceCustomSchemeDisplayName is the DisplayName of every space-private custom scheme this store
// creates: one immutable scheme per non-preset default-capability set.
const spaceCustomSchemeDisplayName = "Space Custom Scheme"

// spaceCustomSchemeNamePrefix namespaces the Name of every space-private custom scheme this store
// creates. Core reserves only the three seeded preset names (mmmodel.IsSpaceSchemeName), so this
// namespace belongs to the plugin: it is what DeleteSpaceCustomSchemeIfUnreferenced matches on to
// prove a scheme is one this store created before deleting it.
const spaceCustomSchemeNamePrefix = "docs_space_custom_"

// The generated roles' DisplayName prefixes, mirroring the values core's own scheme store writes
// (its SchemeRoleDisplayNameChannel* constants) so a space-private scheme is indistinguishable
// from a core-created one in the admin console. They are restated here rather than imported:
// those constants live in server/v8, which this module pins as a test-harness-only dependency
// contributing no runtime symbols.
const (
	schemeRoleDisplayNameChannelUser  = "Channel User Role for Scheme"
	schemeRoleDisplayNameChannelAdmin = "Channel Admin Role for Scheme"
	schemeRoleDisplayNameChannelGuest = "Channel Guest Role for Scheme"
)

// SchemeRoles is the generated channel-scheme role names governing one backing channel's scheme.
// Space capability grants must reference these generated names, not the literal
// channel_user/channel_admin roles: on a scheme-backed channel, core rejects the literal.
type SchemeRoles struct {
	SchemeId      string `db:"scheme_id"`
	SchemeName    string `db:"scheme_name"`
	UserRoleName  string `db:"user_role_name"`
	AdminRoleName string `db:"admin_role_name"`
	GuestRoleName string `db:"guest_role_name"`
}

// GetSchemeRolesForChannel returns the generated scheme role names governing channelID's channel
// scheme. Returns ErrNotFound when the channel does not exist or carries no scheme. There is
// deliberately no DeleteAt filter on the channel: a soft-deleted space is restorable and keeps its
// SchemeId.
func (s *Store) GetSchemeRolesForChannel(channelID string) (*SchemeRoles, error) {
	if channelID == "" {
		return nil, &ErrInvalidInput{Entity: "Channel", Field: "id", Value: channelID}
	}

	query := s.getQueryBuilder().
		Select(
			"s.Id AS scheme_id",
			"s.Name AS scheme_name",
			"s.DefaultChannelUserRole AS user_role_name",
			"s.DefaultChannelAdminRole AS admin_role_name",
			"s.DefaultChannelGuestRole AS guest_role_name",
		).
		From("Channels c").
		Join("Schemes s ON s.Id = c.SchemeId").
		Where(sq.Eq{"c.Id": channelID})

	var roles SchemeRoles
	if err := s.getBuilder(s.db, &roles, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ChannelScheme", ID: channelID}
		}
		return nil, errors.Wrap(err, "unable_to_get_scheme_roles_for_channel")
	}
	return &roles, nil
}

// GetSchemeIDByName returns the id of the scheme with the given name, or ErrNotFound if none
// exists.
func (s *Store) GetSchemeIDByName(name string) (string, error) {
	if name == "" {
		return "", &ErrInvalidInput{Entity: "Scheme", Field: "name", Value: name}
	}

	query := s.getQueryBuilder().
		Select("Id").
		From("Schemes").
		Where(sq.Eq{"Name": name})

	var id string
	if err := s.getBuilder(s.db, &id, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", &ErrNotFound{EntityName: "Scheme", ID: name}
		}
		return "", errors.Wrap(err, "unable_to_get_scheme_by_name")
	}
	return id, nil
}

// GetRolePermissionsByName returns the permission ids granted by the named role.
// Roles.Permissions is stored as a space-joined string column, not an array.
func (s *Store) GetRolePermissionsByName(roleName string) ([]string, error) {
	if roleName == "" {
		return nil, &ErrInvalidInput{Entity: "Role", Field: "name", Value: roleName}
	}

	query := s.getQueryBuilder().
		Select("Permissions").
		From("Roles").
		Where(sq.Eq{"Name": roleName})

	var permissions string
	if err := s.getBuilder(s.db, &permissions, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "Role", ID: roleName}
		}
		return nil, errors.Wrap(err, "unable_to_get_role_permissions")
	}
	return strings.Fields(permissions), nil
}

// CreateSpaceCustomScheme creates one immutable space-private channel scheme with three generated
// roles carrying exactly the given permission sets (user/admin/guest), and returns the new
// scheme's id. The scheme is unreferenced by any channel until the caller repoints the space's
// backing channel at it.
func (s *Store) CreateSpaceCustomScheme(userPermissions, adminPermissions, guestPermissions []string) (_ string, err error) {
	tx, cancel, err := s.beginBoundedTx()
	if err != nil {
		return "", errors.Wrap(err, "begin_transaction")
	}
	defer cancel()
	defer s.finalizeTransaction(tx, &err)

	schemeID := mmmodel.NewId()
	schemeName := spaceCustomSchemeNamePrefix + mmmodel.NewId()
	now := mmmodel.GetMillis()

	userRoleName, roleErr := s.createSchemeRole(tx, schemeID, schemeName, schemeRoleDisplayNameChannelUser, userPermissions, now)
	if roleErr != nil {
		return "", roleErr
	}
	adminRoleName, roleErr := s.createSchemeRole(tx, schemeID, schemeName, schemeRoleDisplayNameChannelAdmin, adminPermissions, now)
	if roleErr != nil {
		return "", roleErr
	}
	guestRoleName, roleErr := s.createSchemeRole(tx, schemeID, schemeName, schemeRoleDisplayNameChannelGuest, guestPermissions, now)
	if roleErr != nil {
		return "", roleErr
	}

	schemeBuilder := s.getQueryBuilder().
		Insert("Schemes").
		Columns(
			"Id", "Name", "DisplayName", "Description", "Scope",
			"DefaultTeamAdminRole", "DefaultTeamUserRole", "DefaultTeamGuestRole",
			"DefaultChannelAdminRole", "DefaultChannelUserRole", "DefaultChannelGuestRole",
			"CreateAt", "UpdateAt", "DeleteAt",
			"DefaultPlaybookAdminRole", "DefaultPlaybookMemberRole", "DefaultRunAdminRole", "DefaultRunMemberRole",
		).
		Values(
			schemeID, schemeName, spaceCustomSchemeDisplayName, "", mmmodel.SchemeScopeChannel,
			"", "", "",
			adminRoleName, userRoleName, guestRoleName,
			now, now, 0,
			"", "", "", "",
		)
	if _, execErr := s.execBuilder(tx, schemeBuilder); execErr != nil {
		return "", errors.Wrap(execErr, "unable_to_save_space_custom_scheme")
	}

	if err = tx.Commit(); err != nil {
		return "", errors.Wrap(err, "commit_transaction")
	}
	return schemeID, nil
}

// createSchemeRole inserts one generated, exact-permission, SchemeManaged role belonging to a
// space-private custom scheme, mirroring core's createScheme role shape: SchemeManaged true (the
// discriminator UpdateChannelMemberRoles requires to accept the generated name on the wire),
// BuiltIn false, SchemeId set. Must run inside tx.
func (s *Store) createSchemeRole(tx *sqlx.Tx, schemeID, schemeName, displayNamePrefix string, permissions []string, now int64) (string, error) {
	roleName := mmmodel.NewId()
	builder := s.getQueryBuilder().
		Insert("Roles").
		Columns("Id", "Name", "DisplayName", "Description", "Permissions", "CreateAt", "UpdateAt", "DeleteAt", "SchemeManaged", "BuiltIn", "SchemeId").
		Values(mmmodel.NewId(), roleName, displayNamePrefix+" "+schemeName, "", joinRolePermissions(permissions), now, now, 0, true, false, schemeID)
	if _, err := s.execBuilder(tx, builder); err != nil {
		return "", errors.Wrap(err, "unable_to_save_space_custom_scheme_role")
	}
	return roleName, nil
}

// joinRolePermissions renders permissions as core's Roles.Permissions column shape: a leading-
// space-joined string (NewRoleFromModel's convention), read back with strings.Fields.
func joinRolePermissions(permissions []string) string {
	return " " + strings.Join(permissions, " ")
}

// DeleteSpaceCustomSchemeIfUnreferenced deletes a space-private custom scheme and its three
// generated roles once no channel — regardless of DeleteAt, since a soft-deleted space is
// restorable and keeps its SchemeId — still references it. excludeChannelID, when non-empty, is
// omitted from that reference count: space creation archives its backing channel when a later
// step fails, and an archived channel keeps its SchemeId, so without the exclusion the abandoned
// channel would count as a live reference and the scheme could never be retired. A no-op (not an
// error) when the scheme is still referenced. Returns ErrInvalidInput for a scheme this store did
// not create.
func (s *Store) DeleteSpaceCustomSchemeIfUnreferenced(schemeID, excludeChannelID string) (err error) {
	if schemeID == "" {
		return &ErrInvalidInput{Entity: "Scheme", Field: "id", Value: schemeID}
	}

	tx, cancel, err := s.beginBoundedTx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer cancel()
	defer s.finalizeTransaction(tx, &err)

	var scheme struct {
		Name                    string
		DefaultChannelUserRole  string
		DefaultChannelAdminRole string
		DefaultChannelGuestRole string
	}
	schemeQuery := s.getQueryBuilder().
		Select("Name", "DefaultChannelUserRole", "DefaultChannelAdminRole", "DefaultChannelGuestRole").
		From("Schemes").
		Where(sq.Eq{"Id": schemeID})
	if txErr := s.getBuilder(tx, &scheme, schemeQuery); txErr != nil {
		if errors.Is(txErr, sql.ErrNoRows) {
			return &ErrNotFound{EntityName: "Scheme", ID: schemeID}
		}
		return errors.Wrap(txErr, "failed to read scheme for retirement")
	}
	// Two independent conditions must hold before anything is deleted: the name must carry this
	// store's own namespace with a non-empty suffix (proving this store created it), and it must
	// not be one of core's seeded preset names.
	if len(scheme.Name) <= len(spaceCustomSchemeNamePrefix) ||
		!strings.HasPrefix(scheme.Name, spaceCustomSchemeNamePrefix) ||
		mmmodel.IsSpaceSchemeName(scheme.Name) {
		return &ErrInvalidInput{Entity: "Scheme", Field: "name", Value: scheme.Name}
	}

	var referenced int
	refQuery := s.getQueryBuilder().Select("COUNT(*)").From("Channels").Where(sq.Eq{"SchemeId": schemeID})
	if excludeChannelID != "" {
		refQuery = refQuery.Where(sq.NotEq{"Id": excludeChannelID})
	}
	if txErr := s.getBuilder(tx, &referenced, refQuery); txErr != nil {
		return errors.Wrap(txErr, "failed to count channels referencing scheme")
	}
	if referenced > 0 {
		return nil
	}

	roleNames := []string{scheme.DefaultChannelUserRole, scheme.DefaultChannelAdminRole, scheme.DefaultChannelGuestRole}
	deleteRoles := s.getQueryBuilder().Delete("Roles").Where(sq.Eq{"Name": roleNames})
	if _, txErr := s.execBuilder(tx, deleteRoles); txErr != nil {
		return errors.Wrap(txErr, "failed to delete space custom scheme roles")
	}

	deleteScheme := s.getQueryBuilder().Delete("Schemes").Where(sq.Eq{"Id": schemeID})
	if _, txErr := s.execBuilder(tx, deleteScheme); txErr != nil {
		return errors.Wrap(txErr, "failed to delete space custom scheme")
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit_transaction")
	}
	return nil
}
