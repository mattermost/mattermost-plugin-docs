// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {dropTargetForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import {useEffect, useState} from 'react';

import {FAVORITES_ZONE_TYPE, SPACE_DRAG_TYPE} from './types';

// Makes the empty-favorites area a drop target so a non-favorite space can be
// dragged in to favorite it.
export function useFavoritesDropZone(element: HTMLElement | null) {
    const [over, setOver] = useState(false);

    useEffect(() => {
        if (!element) {
            return undefined;
        }

        return dropTargetForElements({
            element,
            getData: () => ({type: FAVORITES_ZONE_TYPE}),
            canDrop: ({source}) => source.data.type === SPACE_DRAG_TYPE && source.data.category !== 'favorites',
            onDragEnter: () => setOver(true),
            onDragLeave: () => setOver(false),
            onDrop: () => setOver(false),
        });
    }, [element]);

    return over;
}
