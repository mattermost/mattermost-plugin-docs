// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';
import FolderOutlineIcon from '@mattermost/compass-icons/components/folder-outline';

import styles from './page_drag_preview.module.scss';

type Props = {
    title: string;
    type: string;

    // Direct children only: what rides along with the drag is the whole subtree,
    // but the count communicates the move's shape without unbounded arithmetic.
    childCount: number;
};

/**
 * What follows the pointer while a page row is dragged. Deliberately not the row
 * itself: the row's controls (disclosure toggle, overflow menu, favorite star)
 * are affordances for a stationary row and mean nothing mid-drag.
 */
const PageDragPreview = ({title, type, childCount}: Props) => {
    const Glyph = type === 'page_folder' ? FolderOutlineIcon : FileTextOutlineIcon;

    return (
        <div className={styles.preview}>
            <span
                className={styles.icon}
                aria-hidden={true}
            >
                <Glyph size={16}/>
            </span>
            <span className={styles.title}>{title}</span>
            {childCount > 0 && (
                <span className={styles.count}>{childCount}</span>
            )}
        </div>
    );
};

export default PageDragPreview;
