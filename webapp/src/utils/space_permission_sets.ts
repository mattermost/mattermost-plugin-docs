// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Permission} from 'types/permissions';
import {Permissions} from 'types/permissions';

export type DefaultPermissionPreset = {
    id: 'contribute' | 'comment' | 'read_only';
    permissions: readonly Permission[];
};

/** The three included defaults backed by core's seeded schemes. */
export const DEFAULT_PERMISSION_PRESETS: readonly DefaultPermissionPreset[] = [
    {
        id: 'contribute',
        permissions: [Permissions.CREATE_PAGE, Permissions.COMMENT_PAGE, Permissions.EDIT_PAGE, Permissions.DELETE_OWN_PAGE],
    },
    {
        id: 'comment',
        permissions: [Permissions.COMMENT_PAGE],
    },
    {
        id: 'read_only',
        permissions: [],
    },
];

export const samePermissionSet = (left: readonly Permission[], right: readonly Permission[]) => {
    const leftSet = new Set(left);
    const rightSet = new Set(right);
    return leftSet.size === rightSet.size && [...leftSet].every((permission) => rightSet.has(permission));
};

export type PermissionSummary = 'view' | 'comment' | 'edit' | 'custom';

/** Compact label used by the Share UI while the menu exposes every capability. */
export const summarizePermissions = (permissions: readonly Permission[]): PermissionSummary => {
    const contentPermissions = permissions.filter((permission) =>
        permission !== Permissions.READ_PAGE &&
        permission !== Permissions.ADMIN_SPACE &&
        permission !== Permissions.MANAGE_SPACE &&
        permission !== Permissions.DELETE_SPACE,
    );

    if (samePermissionSet(contentPermissions, DEFAULT_PERMISSION_PRESETS[2].permissions)) {
        return 'view';
    }
    if (samePermissionSet(contentPermissions, DEFAULT_PERMISSION_PRESETS[1].permissions)) {
        return 'comment';
    }
    if (samePermissionSet(contentPermissions, DEFAULT_PERMISSION_PRESETS[0].permissions)) {
        return 'edit';
    }
    return 'custom';
};
