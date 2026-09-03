// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {GlobalState} from '@mattermost/types/store';

import {getLicense} from 'mattermost-redux/selectors/entities/general';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {Permissions} from 'types/permissions';
import type {Permission} from 'types/permissions';

import {getSpace} from './selectors';

// Server-resolved because manage authority is independent of roster membership. Unresolved
// administrative authority fails closed.
export const getCanManageSpaceMembers = (state: GlobalState, spaceId: string): boolean =>
    getSpacePermissions(state, spaceId)?.some((permission) =>
        permission === Permissions.MANAGE_SPACE || permission === Permissions.ADMIN_SPACE) === true;

// Delete and manage are independent team tiers. Unresolved destructive authority fails closed.
export const getCanDeleteSpace = (state: GlobalState, spaceId: string): boolean =>
    getSpacePermissions(state, spaceId)?.includes(Permissions.DELETE_SPACE) === true;

// Exposure controls require space administration, not the broader team manage tier.
export const getCanAdministerSpace = (state: GlobalState, spaceId: string): boolean =>
    getSpacePermissions(state, spaceId)?.includes(Permissions.ADMIN_SPACE) === true;

// The caller's own effective permissions in a space, as the server resolved them, or
// undefined when the space has only been seen in a team listing (which carries none).
export const getSpacePermissions = (state: GlobalState, spaceId: string): Permission[] | undefined =>
    getSpace(state, spaceId)?.permissions;

// Authoring is available to a current holder or to a server-approved self-joiner whose defaults
// include it. Unresolved access withholds the affordance, as every other selector here does: a
// space reaches the store from the team listing without permissions, and offering authoring on
// that record showed create and edit to readers until useResolveSpacePermissions answered.
const offersAuthoring = (state: GlobalState, spaceId: string, permission: Permission): boolean => {
    const space = getSpace(state, spaceId);
    const permissions = space?.permissions;

    if (permissions === undefined) {
        return false;
    }
    if (permissions.includes(permission)) {
        return true;
    }
    return space?.can_join === true && (space.default_permissions ?? []).includes(permission);
};

// Whether to offer page creation in a space.
export const getCanCreatePage = (state: GlobalState, spaceId: string): boolean =>
    offersAuthoring(state, spaceId, Permissions.CREATE_PAGE);

// Creation and editing are independent permissions.
export const getCanEditPage = (state: GlobalState, spaceId: string): boolean =>
    offersAuthoring(state, spaceId, Permissions.EDIT_PAGE);

// Deletion depends on both the any-page and own-page grants. It has no self-join path. As with
// authoring affordances, unresolved access withholds the control.
export const getCanDeletePage = (state: GlobalState, spaceId: string, pageAuthorId: string): boolean => {
    const permissions = getSpacePermissions(state, spaceId);

    if (permissions === undefined) {
        return false;
    }
    if (permissions.includes(Permissions.DELETE_PAGE)) {
        return true;
    }
    return permissions.includes(Permissions.DELETE_OWN_PAGE) && pageAuthorId === getCurrentUserId(state);
};

// The server-resolved precondition for authoring in an open space.
export const getMustJoinSpace = (state: GlobalState, spaceId: string): boolean =>
    getSpace(state, spaceId)?.can_join === true;

// Whether a space default may be any combination rather than one of the named tiers. Mirrors the
// two entitlements the server checks before it creates a custom scheme: custom permission schemes
// (which the Professional SKU includes without the feature flag), and guest account permissions,
// because every custom scheme also defines what a guest may do. Offering the combination controls
// without both would offer a write the server refuses.
export const getCustomDefaultsAvailable = (state: GlobalState): boolean => {
    const license = getLicense(state);
    const customSchemes = license.CustomPermissionsSchemes === 'true' || license.SkuShortName === 'professional';
    return customSchemes && license.GuestAccountsPermissions === 'true';
};
