// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Permission} from 'types/permissions';
import {Permissions} from 'types/permissions';

// The named tiers name the three seeded default schemes, and nothing else. They belong to the
// space default set alone: a set equal to a tier selects that seeded scheme, where any other set
// resolves a pooled one. A member's grant has no such distinction to express — it is a subset of
// the permission ids with no named bundle behind it — so no member surface offers these.
export type PermissionTier = 'view' | 'comment' | 'edit';

export const PERMISSION_TIERS: readonly PermissionTier[] = ['view', 'comment', 'edit'];

// The permission set each tier stands for, in wire form (read_page is the implicit baseline).
export const TIER_PERMISSIONS: Record<PermissionTier, readonly Permission[]> = {
    view: [],
    comment: [Permissions.COMMENT_PAGE],
    edit: [Permissions.CREATE_PAGE, Permissions.COMMENT_PAGE, Permissions.EDIT_PAGE, Permissions.DELETE_OWN_PAGE],
};

export const samePermissionSet = (left: readonly Permission[], right: readonly Permission[]) => {
    const leftSet = new Set(left);
    const rightSet = new Set(right);
    return leftSet.size === rightSet.size && [...leftSet].every((permission) => rightSet.has(permission));
};

export type PermissionSummary = PermissionTier | 'custom';

// Names the content tier a permission set amounts to, ignoring the read baseline and the
// administrative permissions, which say nothing about what the holder may do to pages.
export const summarizePermissions = (permissions: readonly Permission[]): PermissionSummary => {
    const contentPermissions = permissions.filter((permission) =>
        permission !== Permissions.READ_PAGE &&
        permission !== Permissions.ADMIN_SPACE &&
        permission !== Permissions.MANAGE_SPACE &&
        permission !== Permissions.DELETE_SPACE,
    );

    return PERMISSION_TIERS.find((tier) => samePermissionSet(contentPermissions, TIER_PERMISSIONS[tier])) ?? 'custom';
};
