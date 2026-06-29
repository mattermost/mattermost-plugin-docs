// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type Space = {
    id: string;
    name: string;

    // An emoji glyph used as the space's leading icon in the sidebar.
    emoji: string;
};

export type Page = {
    id: string;
    title: string;
    spaceId: string;
    spaceName: string;
};

export type SpaceCategoryType = 'favorites' | 'spaces';

export type SpaceCategory = {
    id: string;
    type: SpaceCategoryType;
    displayName: string;
    spaceIds: string[];
};
