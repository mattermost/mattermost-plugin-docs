// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {monitorForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import {useLatest} from 'hooks/utils';
import {useEffect} from 'react';

import type {Page} from 'types/docs';

import {PAGE_DRAG_TYPE, type PageDropTarget} from './types';

type MoveArgs = {
    pageId: string;
    parentId: string;
    siblingIndex: number;
};

type Args = {
    pages: Page[];
    onMove: (args: MoveArgs) => void;
    enabled: boolean;
};

const bySortOrder = (a: Page, b: Page): number =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title);

// Resolves the drop of `sourceId` onto `targetId` into a (parentId, siblingIndex)
// move. Siblings are computed with the source removed, so the returned index is
// the final 0-based position the server expects.
function resolveMove(pages: Page[], sourceId: string, targetId: string, drop: PageDropTarget): MoveArgs | null {
    const target = pages.find((page) => page.id === targetId);
    if (!target) {
        return null;
    }

    if (drop.mode === 'reparent') {
        const childCount = pages.filter((page) => page.parent_id === targetId && page.id !== sourceId).length;
        return {pageId: sourceId, parentId: targetId, siblingIndex: childCount};
    }

    const parentId = target.parent_id;
    const siblings = pages.
        filter((page) => page.parent_id === parentId && page.id !== sourceId).
        sort(bySortOrder);
    const targetIndex = siblings.findIndex((page) => page.id === targetId);
    if (targetIndex === -1) {
        return null;
    }
    const siblingIndex = drop.edge === 'bottom' ? targetIndex + 1 : targetIndex;
    return {pageId: sourceId, parentId, siblingIndex};
}

// One monitor for the whole tree: on drop it resolves the source/target pair
// into a move and hands it to `onMove`. Registered once; live state is read
// through refs so the listener never re-registers mid-drag.
export function usePagesDnd({pages, onMove, enabled}: Args) {
    const pagesRef = useLatest(pages);
    const onMoveRef = useLatest(onMove);
    const enabledRef = useLatest(enabled);

    useEffect(() => monitorForElements({
        canMonitor: ({source}) => enabledRef.current && source.data.type === PAGE_DRAG_TYPE,
        onDrop: ({source, location}) => {
            const target = location.current.dropTargets[0];
            if (!target || target.data.type !== PAGE_DRAG_TYPE) {
                return;
            }

            const sourceId = source.data.pageId as string;
            const targetId = target.data.pageId as string;
            const drop = {mode: target.data.mode, edge: target.data.edge} as PageDropTarget;

            const move = resolveMove(pagesRef.current, sourceId, targetId, drop);
            if (move) {
                onMoveRef.current(move);
            }
        },
    }), [pagesRef, onMoveRef, enabledRef]);
}
