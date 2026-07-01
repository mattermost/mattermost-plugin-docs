// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DropIndicator} from '@atlaskit/pragmatic-drag-and-drop-react-drop-indicator/box';
import classNames from 'classnames';
import React, {useState} from 'react';

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
    favorite: boolean;
    onSelect: (id: string) => void;
    onToggleFavorite: (id: string) => void;
};

const SpaceItem = ({space, category, active, favorite, onSelect, onToggleFavorite}: Props) => {
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const {dragging, closestEdge} = useSpaceDragDrop({spaceId: space.id, category, element});

    const emoji = (
        <span
            className={styles.emoji}
            aria-hidden={true}
        >
            {space.emoji}
        </span>
    );

    return (
        <div
            ref={setElement}
            className={classNames(styles.root, {[styles.dragging]: dragging})}
        >
            <SidebarItem
                leading={emoji}
                label={space.name}
                active={active}
                title={space.name}
                trailing={(
                    <SpaceItemMenu
                        space={space}
                        favorite={favorite}
                        onToggleFavorite={onToggleFavorite}
                    />
                )}
                revealTrailingOnHover={true}
                onClick={() => onSelect(space.id)}
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

export default SpaceItem;
