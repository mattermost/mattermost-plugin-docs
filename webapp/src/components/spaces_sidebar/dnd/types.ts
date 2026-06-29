// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type DndCategory = 'favorites' | 'spaces';

export const SPACE_DRAG_TYPE = 'docs-space';
export const FAVORITES_ZONE_TYPE = 'docs-favorites-zone';

export type SpaceDragData = {
    type: typeof SPACE_DRAG_TYPE;
    spaceId: string;
    category: DndCategory;
};
