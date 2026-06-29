// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Edge} from '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge';

// Destination index for a reorder WITHIN a single list, accounting for the
// dragged item being removed from the list first. Mirrors the inline math core
// uses in channel_bookmarks (no external reorder util).
export function getDropIndex(sourceIndex: number, targetIndex: number, edge: Edge | null): number {
    if (edge === 'top') {
        return sourceIndex < targetIndex ? targetIndex - 1 : targetIndex;
    }
    return sourceIndex < targetIndex ? targetIndex : targetIndex + 1;
}
