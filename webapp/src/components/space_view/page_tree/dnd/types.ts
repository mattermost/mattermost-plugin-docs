// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Edge} from '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge';

export const PAGE_DRAG_TYPE = 'docs-page';

// Where a drop lands relative to the target row: 'reorder' (above/below a
// sibling, via `edge`) or 'reparent' (onto the row's center → become its child).
export type PageDropTarget =
    | {mode: 'reorder'; edge: Edge}
    | {mode: 'reparent'};

// A group's trailing strip, which appends as the last item of that group. It has
// no target row — the group's own parent is the target, and '' means the root
// group — so it can express "last at this level", which a row edge cannot: an
// expanded row's bottom edge sits above its children, not after them.
export const PAGE_APPEND_TYPE = 'docs-page-append';

export type PageAppendData = {
    type: typeof PAGE_APPEND_TYPE;
    parentId: string;
    blocked: boolean;
};

export type PageDragData = {
    type: typeof PAGE_DRAG_TYPE;
    pageId: string;
};
