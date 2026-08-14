// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {dropTargetForElements} from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import {useLatest} from 'hooks/utils';
import {useEffect, useState} from 'react';

import {PAGE_APPEND_TYPE, PAGE_DRAG_TYPE} from './types';

type Args = {

    // The group this strip appends into; '' is the root group.
    parentId: string;
    element: HTMLElement | null;

    // True when `sourcePageId` may become the last child of `parentId` — guards
    // the nesting cap and dropping a page into its own subtree. Read through a
    // ref so a changing predicate doesn't re-register the listener.
    canDrop: (sourcePageId: string) => boolean;
    enabled: boolean;
};

/**
 * A group's trailing drop strip. Registers the "append to this group" target and
 * reports whether a drag is currently over it, so the caller can draw the
 * indicator at the group's own indentation.
 */
export function useAppendDrop({parentId, element, canDrop, enabled}: Args) {
    const [active, setActive] = useState(false);
    const [blocked, setBlocked] = useState(false);
    const canDropRef = useLatest(canDrop);

    useEffect(() => {
        if (!element || !enabled) {
            return undefined;
        }

        return dropTargetForElements({
            element,
            getData: ({source}) => ({
                type: PAGE_APPEND_TYPE,
                parentId,
                blocked: !canDropRef.current(source.data.pageId as string),
            }),
            canDrop: ({source}) => source.data.type === PAGE_DRAG_TYPE,
            onDrag: ({self}) => {
                setBlocked(Boolean(self.data.blocked));
                setActive(!self.data.blocked);
            },
            onDragLeave: () => {
                setActive(false);
                setBlocked(false);
            },
            onDrop: () => {
                setActive(false);
                setBlocked(false);
            },
        });
    }, [parentId, element, enabled, canDropRef]);

    return {active, blocked};
}
