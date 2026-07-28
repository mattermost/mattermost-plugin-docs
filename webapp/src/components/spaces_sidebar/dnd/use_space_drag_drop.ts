// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combine} from '@atlaskit/pragmatic-drag-and-drop/combine';
import {draggable, dropTargetForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import type {Edge} from '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge';
import {attachClosestEdge, extractClosestEdge} from '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge';
import {useEffect, useState} from 'react';

import {SPACE_DRAG_TYPE, type DndCategory} from './types';

type Args = {
    spaceId: string;
    category: DndCategory;
    element: HTMLElement | null;
    enabled: boolean;
};

export function useSpaceDragDrop({spaceId, category, element, enabled}: Args) {
    const [dragging, setDragging] = useState(false);
    const [closestEdge, setClosestEdge] = useState<Edge | null>(null);

    useEffect(() => {
        if (!element || !enabled) {
            return undefined;
        }

        return combine(
            draggable({
                element,
                getInitialData: () => ({type: SPACE_DRAG_TYPE, spaceId, category}),
                onDragStart: () => setDragging(true),
                onDrop: () => setDragging(false),
            }),
            dropTargetForElements({
                element,
                getData: ({input, element: el}) => attachClosestEdge(
                    {type: SPACE_DRAG_TYPE, spaceId, category},
                    {input, element: el, allowedEdges: ['top', 'bottom']},
                ),
                canDrop: ({source}) => source.data.type === SPACE_DRAG_TYPE && source.data.spaceId !== spaceId,
                onDrag: ({self}) => setClosestEdge(extractClosestEdge(self.data)),
                onDragLeave: () => setClosestEdge(null),
                onDrop: () => setClosestEdge(null),
            }),
        );
    }, [spaceId, category, element, enabled]);

    return {dragging, closestEdge};
}
