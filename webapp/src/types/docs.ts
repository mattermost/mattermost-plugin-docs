// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineMessage} from 'react-intl';

import type {Permission} from './permissions';

// Whether a space is visible to the whole team or only invited members. Only
// 'private' is selectable for now — public (open) spaces wait on view_access.
export type SpaceVisibility = 'public' | 'private';

// Field names and shapes mirror the server model (server/model/space.go) —
// snake_case JSON per @mattermost/types convention. The server model has no
// `visibility` yet; it stays a client-only field and maps to the server's
// `view_access` (open/private) once that lands (PR #10 / MM-69269).
export type Space = {
    id: string;
    team_id: string;
    creator_id: string;
    title: string;
    description?: string;
    icon?: string;
    props: Record<string, string>;
    create_at: number;
    update_at: number;
    delete_at: number;
    sort_order: number;
    visibility?: SpaceVisibility;

    // The caller's own effective permissions in this space, as the server resolved them.
    // Optional because only the single-space read answers with them: the team listing returns
    // bare spaces, so a space the client has only seen in a list carries none. Undefined
    // therefore means "not resolved yet", never "holds nothing" — see getSpacePermissions.
    permissions?: Permission[];
};

// A space member. The server exposes only the user id (roles/permissions are
// hidden); the user profile is resolved from the host store when needed.
export type SpaceMember = {
    user_id: string;
};

// The fields the create-space form collects. The data source turns this into a
// Space (assigning the opaque id, etc.). visibility is client-only for now (maps
// to the server's view_access later).
export type CreateSpaceInput = {
    title: string;
    visibility: SpaceVisibility;
    description?: string;
    icon?: string;
};

// The editable fields of a space, as sent to the update-space API. Every field
// is optional so a patch carries only what changed; the server model's
// concurrency token (expected_update_at) is passed separately.
export type UpdateSpacePatch = {
    title?: string;
    description?: string;
    icon?: string;

    // Replaces the whole props map server-side, so callers must send the merged
    // result rather than just the keys they changed.
    props?: Record<string, string>;
};

// Space prop holding the page the space opens to; absent/empty means the space
// front door (hero).
export const SPACE_PROP_DEFAULT_PAGE_ID = 'default_page_id';

// The title a page carries while it has no name of its own. The server rejects an
// empty title (validateTitle in server/app/service.go, and Page.IsValid), so
// "unnamed" has to be a real stored string rather than "". One descriptor shared by
// everything that writes it and everything that recognizes it: the title editor
// shows it as placeholder text instead of a value, so naming a new page doesn't
// start with clearing the old name, and a comparison against a different id's
// translation of the same word would silently stop matching.
// defineMessage, not a bare object: the extractor only sees a descriptor it can
// read statically, and a shared one is never written inline at its call sites.
export const UNTITLED_PAGE_TITLE = defineMessage({id: 'docs.page.untitled', defaultMessage: 'Untitled'});

// Mirrors the server model (server/model/page.go). search_text, original_id, and
// props exist on the server model too; add them as the editor lands.
export type Page = {
    id: string;
    space_id: string;
    parent_id: string;
    type: string;
    title: string;
    body: string;

    // Author and last editor, returned on both the full page and list summaries.
    user_id: string;
    last_modified_by: string;
    sort_order: number;
    create_at: number;
    update_at: number;
    edit_at: number;
    delete_at: number;
};

// The editable fields of a page, as sent to the update-page API. Every field is
// optional so a patch carries only what changed; the optimistic-lock baseline
// (base_edit_at) is passed separately.
export type UpdatePagePatch = {
    title?: string;
    body?: string;
};

// The fields needed to create a page. parentId is omitted for a root page (the
// data source maps it to the server's parent_id).
export type CreatePageInput = {
    title: string;
    parentId?: string;
};

export type SpaceSummary = {
    space: Space;

    // Omitted until the server provides a count (listing pages per space just to
    // count them isn't worth it for MVP).
    pageCount?: number;

    // Epoch ms of the viewer's last visit; the client formats it into a
    // "Viewed 12m ago"-style label at render. Client-tracked today (see
    // data/recent_spaces), server-provided later.
    lastViewedAt?: number;
};

export type SpaceCategoryType = 'favorites' | 'spaces';

export type SpaceCategory = {
    id: string;
    type: SpaceCategoryType;
    displayName: string;
    spaceIds: string[];
};
