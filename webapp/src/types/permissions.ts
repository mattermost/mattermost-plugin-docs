// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The capability vocabulary is the server's permission ids verbatim
// (server/model/space_capabilities.go), so the client speaks the same tokens
// the server enforces rather than inventing level names of its own.
export const Capabilities = {
    READ_PAGE: 'read_page',
    CREATE_PAGE: 'create_page',
    COMMENT_PAGE: 'comment_page',
    EDIT_PAGE: 'edit_page',
    DELETE_OWN_PAGE: 'delete_own_page',
    DELETE_PAGE: 'delete_page',
    ADMIN_SPACE: 'admin_space',
} as const;

export type Capability = (typeof Capabilities)[keyof typeof Capabilities];

// The capabilities a space default may carry, in the order they are shown.
// read_page is absent: every member holds it, and the server rejects it as a
// grant. admin_space is absent too — it is a per-member grant, never a default.
export const DEFAULT_CAPABILITY_ORDER: Capability[] = [
    Capabilities.CREATE_PAGE,
    Capabilities.COMMENT_PAGE,
    Capabilities.EDIT_PAGE,
    Capabilities.DELETE_OWN_PAGE,
    Capabilities.DELETE_PAGE,
];

// The per-member grant vocabulary: the space-default set plus admin_space.
export const MEMBER_CAPABILITY_ORDER: Capability[] = [
    ...DEFAULT_CAPABILITY_ORDER,
    Capabilities.ADMIN_SPACE,
];

// Mirrors server/model/space.go SpaceMember. capabilities is the effective set
// (space default union granted, read_page included); granted_capabilities is
// only what this member holds beyond the default.
export type SpaceMember = {
    user_id: string;
    capabilities: Capability[];
    granted_capabilities: Capability[];
    is_admin: boolean;
    is_guest: boolean;
};

// Mirrors server/model/space.go SpaceWithAccess. The Space fields are flattened
// into the same object server-side; only the two access fields are modelled
// here, since that is all the permissions surface reads.
export type SpaceAccess = {
    id: string;
    default_capabilities: Capability[];
    capabilities: Capability[];
};

// The list-endpoint envelope (paginatedResponse in server/api.go).
export type Paginated<T> = {
    items: T[];
    page: number;
    per_page: number;
    has_more: boolean;
};
