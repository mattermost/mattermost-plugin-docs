// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combine} from '@atlaskit/pragmatic-drag-and-drop/combine';
import {draggable, dropTargetForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import {pointerOutsideOfPreview} from '@atlaskit/pragmatic-drag-and-drop/element/pointer-outside-of-preview';
import {setCustomNativeDragPreview} from '@atlaskit/pragmatic-drag-and-drop/element/set-custom-native-drag-preview';
import {useLatest} from 'hooks/utils';
import {useEffect, useState} from 'react';

import {PAGE_DRAG_TYPE, type PageDropTarget} from './types';

type Args = {
    pageId: string;
    element: HTMLElement | null;

    // True when `sourcePageId` is allowed to drop on this row in `mode` (guards
    // against dropping a page into its own subtree, and against exceeding the
    // nesting cap). Read at drop time via a ref, so a changing predicate doesn't
    // re-register the drag listeners.
    canDrop: (sourcePageId: string, mode: PageDropTarget['mode']) => boolean;
    enabled: boolean;

    // True when this row's children are showing. Such a row has no bottom edge to
    // offer: the pixels below it hold its children, so an indicator drawn there
    // would sit above the very subtree the drop lands after. "Last in this group"
    // is expressed by the group's trailing strip instead, and "first child" by the
    // first child's own top edge.
    expanded: boolean;
};

// A row's vertical hitbox: the top and bottom quarters reorder above/below the
// row; the middle half reparents (drop onto center → become a child).
const REORDER_BAND = 0.25;

function computeDropTarget(element: Element, clientY: number, expanded: boolean): PageDropTarget {
    const rect = element.getBoundingClientRect();
    const ratio = (clientY - rect.top) / rect.height;
    if (ratio <= REORDER_BAND) {
        return {mode: 'reorder', edge: 'top'};
    }
    if (!expanded && ratio >= 1 - REORDER_BAND) {
        return {mode: 'reorder', edge: 'bottom'};
    }

    // An expanded row's bottom band becomes more reparent surface, so dragging low
    // over it nests rather than doing nothing.
    return {mode: 'reparent'};
}

// Keeps the preview clear of the cursor so it never covers the row underneath,
// which is the row the drop indicator is describing.
const PREVIEW_OFFSET = {x: '12px', y: '8px'};

export function usePageDragDrop({pageId, element, canDrop, enabled, expanded}: Args) {
    const [dragging, setDragging] = useState(false);
    const [dropTarget, setDropTarget] = useState<PageDropTarget | null>(null);

    // Set while the browser is capturing the drag image: the caller portals its
    // preview into this container instead of letting the row itself be snapshotted.
    const [previewContainer, setPreviewContainer] = useState<HTMLElement | null>(null);

    // True while a drag hovers this row in a position it cannot accept (dropping
    // onto own descendant, or a move that would exceed the nesting cap). Drives
    // the "not allowed" indicator; the move itself is still suppressed on drop.
    const [blocked, setBlocked] = useState(false);
    const canDropRef = useLatest(canDrop);

    useEffect(() => {
        if (!element || !enabled) {
            return undefined;
        }

        return combine(
            draggable({
                element,
                getInitialData: () => ({type: PAGE_DRAG_TYPE, pageId}),
                onGenerateDragPreview: ({nativeSetDragImage}) => {
                    setCustomNativeDragPreview({
                        nativeSetDragImage,
                        getOffset: pointerOutsideOfPreview(PREVIEW_OFFSET),
                        render: ({container}) => {
                            setPreviewContainer(container);
                            return () => setPreviewContainer(null);
                        },
                    });
                },
                onDragStart: () => setDragging(true),
                onDrop: () => setDragging(false),
            }),
            dropTargetForElements({
                element,

                // `blocked` covers the mode the pointer currently sits in: the row
                // may accept a reorder while rejecting a reparent, so the verdict
                // travels with the data and suppresses both the indicator and the
                // move itself (see `usePagesDnd`).
                getData: ({input, element: el, source}) => {
                    const target = computeDropTarget(el, input.clientY, expanded);
                    return {
                        type: PAGE_DRAG_TYPE,
                        pageId,
                        blocked: !canDropRef.current(source.data.pageId as string, target.mode),
                        ...target,
                    };
                },

                // Truthful: this row is a drop target only if the source can
                // actually land here in at least one mode. `getData.blocked`
                // then narrows it to the mode under the pointer, so a row that
                // accepts a reorder but not a (cap-exceeding) reparent still
                // shows the "not allowed" cue in its middle band.
                canDrop: ({source}) => source.data.type === PAGE_DRAG_TYPE &&
                    source.data.pageId !== pageId &&
                    (canDropRef.current(source.data.pageId as string, 'reorder') ||
                        canDropRef.current(source.data.pageId as string, 'reparent')),
                onDrag: ({self}) => {
                    if (self.data.blocked) {
                        setBlocked(true);
                        setDropTarget(null);
                    } else {
                        setBlocked(false);
                        setDropTarget({mode: self.data.mode, edge: self.data.edge} as PageDropTarget);
                    }
                },
                onDragLeave: () => {
                    setDropTarget(null);
                    setBlocked(false);
                },
                onDrop: () => {
                    setDropTarget(null);
                    setBlocked(false);
                },
            }),
        );
    }, [pageId, element, enabled, expanded, canDropRef]);

    return {dragging, dropTarget, blocked, previewContainer};
}
