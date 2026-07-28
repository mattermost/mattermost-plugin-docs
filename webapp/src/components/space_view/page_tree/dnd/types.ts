// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Edge} from '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge';

export const PAGE_DRAG_TYPE = 'docs-page';

// Where a drop lands relative to the target row: 'reorder' (above/below a
// sibling, via `edge`) or 'reparent' (onto the row's center → become its child).
export type PageDropTarget =
    | {mode: 'reorder'; edge: Edge}
    | {mode: 'reparent'};

export type PageDragData = {
    type: typeof PAGE_DRAG_TYPE;
    pageId: string;
};
