// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DropIndicator} from '@atlaskit/pragmatic-drag-and-drop-react-drop-indicator/box';
import classNames from 'classnames';
import React, {useState} from 'react';

import {useAppendDrop} from './dnd/use_append_drop';
import styles from './page_tree_append_zone.module.scss';

type Props = {

    // The group this strip appends into; '' is the root group.
    parentId: string;

    // Indentation of the group's own rows, so the indicator lines up with the
    // items the drop would sit after rather than spanning the whole panel.
    indent: number;
    canDrop: (sourcePageId: string) => boolean;
    enabled: boolean;
};

/**
 * The strip after a group's last row. It exists because a row edge cannot express
 * "last at this level": an expanded row's bottom edge is drawn above its own
 * children, so it can only mean "after the subtree" while looking like "before the
 * first child". This strip is the position that reads the way it behaves.
 */
const PageTreeAppendZone = ({parentId, indent, canDrop, enabled}: Props) => {
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const {active, blocked} = useAppendDrop({parentId, element, canDrop, enabled});

    return (
        <div
            ref={setElement}
            className={classNames(styles.zone, {[styles.blocked]: blocked})}
            style={{marginInlineStart: indent}}
        >
            {active && <DropIndicator edge='top'/>}
        </div>
    );
};

export default PageTreeAppendZone;
