// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Space} from './docs';

// Must stay byte-for-byte aligned with server/model/space_permissions.go.
export const Permissions = {
    READ_PAGE: 'read_page',
    CREATE_PAGE: 'create_page',
    COMMENT_PAGE: 'comment_page',
    EDIT_PAGE: 'edit_page',
    DELETE_OWN_PAGE: 'delete_own_page',
    DELETE_PAGE: 'delete_page',
    ADMIN_SPACE: 'admin_space',

    // The manage tier. Team-scoped server-side, so unlike the others it can be held without any
    // standing in the space itself — a team administrator manages a space they are not a member of.
    // It appears in an effective set, never in a grant or a space default.
    MANAGE_SPACE: 'manage_space',

    // The delete tier, gating archive and restore. Team-scoped like the manage tier and
    // independent of it: a team role can carry either one without the other, so neither stands in
    // for the other when deciding what to offer. Appears in an effective set only.
    DELETE_SPACE: 'delete_space',
} as const;

export type Permission = (typeof Permissions)[keyof typeof Permissions];

// The permissions a space default may carry, in the order they are shown.
// read_page is absent: every member holds it, and the server rejects it as a
// grant. admin_space is absent too — it is a per-member grant, never a default.
export const DEFAULT_PERMISSION_ORDER: readonly Permission[] = [
    Permissions.CREATE_PAGE,
    Permissions.COMMENT_PAGE,
    Permissions.EDIT_PAGE,
    Permissions.DELETE_OWN_PAGE,
    Permissions.DELETE_PAGE,
];

// The per-member grant vocabulary: the space-default set plus admin_space.
export const MEMBER_PERMISSION_ORDER: readonly Permission[] = [
    ...DEFAULT_PERMISSION_ORDER,
    Permissions.ADMIN_SPACE,
];

// Canonical SpaceMember wire model. Without manage authority, permission arrays and auto_joined
// are redacted while identity and role flags remain meaningful.
export type SpaceMember = {
    user_id: string;
    permissions: Permission[];
    granted_permissions: Permission[];
    is_admin: boolean;
    is_guest: boolean;
    auto_joined: boolean;
};

// The space's own read/discover setting — distinct from the permission
// vocabulary above. One spelling everywhere: the create form, the settings tab
// and the server all speak these two tokens.
export type SpaceViewAccess = 'open' | 'private';

// Mirrors the flattened server SpaceWithAccess response. Access fields are required here even
// though bare team-list Space records omit them.
export type SpaceAccess = Space & {
    default_permissions: Permission[];
    permissions: Permission[];
    can_join: boolean;
};
