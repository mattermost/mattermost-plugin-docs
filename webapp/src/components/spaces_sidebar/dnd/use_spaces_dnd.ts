// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {monitorForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import {extractClosestEdge} from '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge';
import {useLatest} from 'hooks/utils';
import {useEffect} from 'react';

import {getDropIndex} from './get_drop_index';
import {FAVORITES_ZONE_TYPE, SPACE_DRAG_TYPE, type DndCategory} from './types';

type Args = {
    favoriteOrder: string[];
    spacesOrder: string[];
    onReorder: (spaceId: string, from: DndCategory, to: DndCategory, index: number) => void;
    enabled: boolean;
};

export function useSpacesDnd({favoriteOrder, spacesOrder, onReorder, enabled}: Args) {
    const favRef = useLatest(favoriteOrder);
    const spacesRef = useLatest(spacesOrder);
    const onReorderRef = useLatest(onReorder);
    const enabledRef = useLatest(enabled);

    useEffect(() => monitorForElements({
        canMonitor: ({source}) => enabledRef.current && source.data.type === SPACE_DRAG_TYPE,
        onDrop: ({source, location}) => {
            const target = location.current.dropTargets[0];
            if (!target) {
                return;
            }

            const spaceId = source.data.spaceId as string;
            const from = source.data.category as DndCategory;

            if (target.data.type === FAVORITES_ZONE_TYPE) {
                if (from === 'favorites') {
                    return;
                }
                onReorderRef.current(spaceId, from, 'favorites', favRef.current.length);
                return;
            }

            if (target.data.type !== SPACE_DRAG_TYPE) {
                return;
            }

            const to = target.data.category as DndCategory;
            const targetId = target.data.spaceId as string;
            const edge = extractClosestEdge(target.data);
            const targetList = to === 'favorites' ? favRef.current : spacesRef.current;
            const targetIndex = targetList.indexOf(targetId);
            if (targetIndex === -1) {
                return;
            }

            let index: number;
            if (from === to) {
                const sourceIndex = targetList.indexOf(spaceId);
                index = getDropIndex(sourceIndex, targetIndex, edge);
            } else {
                index = edge === 'bottom' ? targetIndex + 1 : targetIndex;
            }

            onReorderRef.current(spaceId, from, to, index);
        },
    }), []);
}
