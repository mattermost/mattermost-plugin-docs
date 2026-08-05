// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DropIndicator} from '@atlaskit/pragmatic-drag-and-drop-react-drop-indicator/box';
import classNames from 'classnames';
import React, {useState} from 'react';
import {SpaceIcon} from 'utils/space_icon';

import type {Space} from 'types/docs';

import type {DndCategory} from './dnd/types';
import {useSpaceDragDrop} from './dnd/use_space_drag_drop';
import SidebarItem from './sidebar_item';
import styles from './space_item.module.scss';
import SpaceItemMenu from './space_item_menu';

type Props = {
    space: Space;
    category: DndCategory;
    active: boolean;

    // Gates drag-to-reorder until sidebar ordering persists (see SpacesSidebar).
    dndEnabled: boolean;
    href: string;
};

const SpaceItem = ({space, category, active, dndEnabled, href}: Props) => {
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const {dragging, closestEdge} = useSpaceDragDrop({spaceId: space.id, category, element, enabled: dndEnabled});

    const emoji = (
        <span
            className={styles.emoji}
            aria-hidden={true}
        >
            <SpaceIcon
                space={space}
                size={16}
            />
        </span>
    );

    return (
        <div
            ref={setElement}
            className={classNames(styles.root, {[styles.dragging]: dragging})}
        >
            <SidebarItem
                leading={emoji}
                label={space.title}
                active={active}
                title={space.title}
                trailing={(
                    <SpaceItemMenu space={space}/>
                )}
                revealTrailingOnHover={true}
                to={href}

                // The row is a drag source; a link's native drag would pre-empt it.
                draggable={false}
            />
            {closestEdge && (
                <DropIndicator
                    edge={closestEdge}
                    gap='0px'
                />
            )}
        </div>
    );
};

// Memoized because the sidebar re-renders on every navigation and nav-tab change,
// which would otherwise re-run every row (each of which registers a drag listener
// and reads its own favorite state) for a change affecting one or two of them.
export default React.memo(SpaceItem);
