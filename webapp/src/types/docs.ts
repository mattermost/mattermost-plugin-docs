// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Whether a space is visible to the whole team or only invited members. Only
// 'public' is selectable in the initial MVF — space-level permissions (and thus
// private spaces) are not built yet.
export type SpaceVisibility = 'public' | 'private';

export type Space = {
    id: string;
    name: string;
    emoji: string;

    visibility?: SpaceVisibility;
    description?: string;
};

// The fields the create-space form collects. The data source turns this into a
// Space (assigning the opaque id, etc.).
export type CreateSpaceInput = {
    name: string;

    // Vanity URL segment, derived from the name but independently editable.
    slug: string;
    visibility: SpaceVisibility;
    description?: string;
    emoji?: string;
};

export type Page = {
    id: string;
    title: string;
    spaceId: string;
    spaceName: string;
};

export type SpaceSummary = {
    space: Space;
    pageCount: number;

    // Human-readable "Viewed 12m ago"-style label (server-provided later).
    viewedLabel?: string;
};

export type SpaceCategoryType = 'favorites' | 'spaces';

export type SpaceCategory = {
    id: string;
    type: SpaceCategoryType;
    displayName: string;
    spaceIds: string[];
};
