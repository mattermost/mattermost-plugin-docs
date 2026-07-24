// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Whether a space is visible to the whole team or only invited members. Only
// 'public' is selectable in the initial MVF — space-level permissions (and thus
// private spaces) are not built yet.
export type SpaceVisibility = 'public' | 'private';

// Field names and shapes mirror the server model (server/model/space.go) —
// snake_case JSON per @mattermost/types convention. The server model has no
// `visibility`; it stays a client-only field until channel privacy / props
// represent it.
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
};

// The fields the create-space form collects. The data source turns this into a
// Space (assigning the opaque id, etc.).
export type CreateSpaceInput = {
    title: string;

    // Vanity URL segment, derived from the title but independently editable.
    slug: string;
    visibility: SpaceVisibility;
    description?: string;
    icon?: string;
};

// Mirrors the server model (server/model/page.go). search_text, user_id,
// last_modified_by, original_id, and props exist on the server model too; add
// them as the API/editor lands.
export type Page = {
    id: string;
    space_id: string;
    parent_id: string;
    type: string;
    title: string;
    body: string;
    sort_order: number;
    create_at: number;
    update_at: number;
    edit_at: number;
    delete_at: number;
};

export type SpaceSummary = {
    space: Space;
    pageCount: number;

    // Epoch ms of the viewer's last visit; the client formats it into a
    // "Viewed 12m ago"-style label at render (server-provided later).
    lastViewedAt?: number;
};

export type SpaceCategoryType = 'favorites' | 'spaces';

export type SpaceCategory = {
    id: string;
    type: SpaceCategoryType;
    displayName: string;
    spaceIds: string[];
};
