// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Permission} from 'types/permissions';
import {Permissions} from 'types/permissions';

// The named tiers every permission surface offers first. The three content tiers are the
// seeded default schemes; admin is a per-member grant only, never a space default.
export type PermissionTier = 'view' | 'comment' | 'edit';
export type MemberPermissionTier = PermissionTier | 'admin';

export const PERMISSION_TIERS: readonly PermissionTier[] = ['view', 'comment', 'edit'];
export const MEMBER_PERMISSION_TIERS: readonly MemberPermissionTier[] = [...PERMISSION_TIERS, 'admin'];

// The permission set each tier stands for, in wire form (read_page is the implicit baseline).
export const TIER_PERMISSIONS: Record<MemberPermissionTier, readonly Permission[]> = {
    view: [],
    comment: [Permissions.COMMENT_PAGE],
    edit: [Permissions.CREATE_PAGE, Permissions.COMMENT_PAGE, Permissions.EDIT_PAGE, Permissions.DELETE_OWN_PAGE],
    admin: [Permissions.ADMIN_SPACE],
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

// Names the tier a member holds: admin when the grant carries admin_space, otherwise the content
// tier of their effective set. Effective rather than granted, since a member cannot hold less
// than the space default, so a lower tier would name something the member cannot be.
export const summarizeMemberPermissions = (granted: readonly Permission[], effective: readonly Permission[]): MemberPermissionTier | 'custom' => {
    if (granted.includes(Permissions.ADMIN_SPACE)) {
        return 'admin';
    }
    return summarizePermissions(effective);
};
