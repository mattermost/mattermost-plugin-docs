// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combine} from '@atlaskit/pragmatic-drag-and-drop/combine';
import {draggable, dropTargetForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import {useLatest} from 'hooks/utils';
import {useEffect, useState} from 'react';

import {PAGE_DRAG_TYPE, type PageDropTarget} from './types';

type Args = {
    pageId: string;
    element: HTMLElement | null;

    // True when `sourcePageId` is allowed to drop on this row (guards against
    // dropping a page into its own subtree). Read at drop time via a ref, so a
    // changing predicate doesn't re-register the drag listeners.
    canDrop: (sourcePageId: string) => boolean;
    enabled: boolean;
};

// A row's vertical hitbox: the top and bottom quarters reorder above/below the
// row; the middle half reparents (drop onto center → become a child).
const REORDER_BAND = 0.25;

function computeDropTarget(element: Element, clientY: number): PageDropTarget {
    const rect = element.getBoundingClientRect();
    const ratio = (clientY - rect.top) / rect.height;
    if (ratio <= REORDER_BAND) {
        return {mode: 'reorder', edge: 'top'};
    }
    if (ratio >= 1 - REORDER_BAND) {
        return {mode: 'reorder', edge: 'bottom'};
    }
    return {mode: 'reparent'};
}

export function usePageDragDrop({pageId, element, canDrop, enabled}: Args) {
    const [dragging, setDragging] = useState(false);
    const [dropTarget, setDropTarget] = useState<PageDropTarget | null>(null);
    const canDropRef = useLatest(canDrop);

    useEffect(() => {
        if (!element || !enabled) {
            return undefined;
        }

        return combine(
            draggable({
                element,
                getInitialData: () => ({type: PAGE_DRAG_TYPE, pageId}),
                onDragStart: () => setDragging(true),
                onDrop: () => setDragging(false),
            }),
            dropTargetForElements({
                element,
                getData: ({input, element: el}) => ({type: PAGE_DRAG_TYPE, pageId, ...computeDropTarget(el, input.clientY)}),
                canDrop: ({source}) => source.data.type === PAGE_DRAG_TYPE &&
                    source.data.pageId !== pageId &&
                    canDropRef.current(source.data.pageId as string),
                onDrag: ({self}) => setDropTarget({mode: self.data.mode, edge: self.data.edge} as PageDropTarget),
                onDragLeave: () => setDropTarget(null),
                onDrop: () => setDropTarget(null),
            }),
        );
    }, [pageId, element, enabled, canDropRef]);

    return {dragging, dropTarget};
}
