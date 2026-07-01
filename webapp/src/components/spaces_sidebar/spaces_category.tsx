// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import styles from './spaces_category.module.scss';

type Props = {
    title: React.ReactNode;
    collapsible?: boolean;
    collapsed?: boolean;
    onToggle?: () => void;
    children: React.ReactNode;
};

const SpacesCategory = ({title, collapsible = true, collapsed = false, onToggle, children}: Props) => (
    <div className={styles.root}>
        <button
            type='button'
            className={classNames(styles.header, {[styles.static]: !collapsible})}
            onClick={collapsible ? onToggle : undefined}
        >
            <span className={classNames(styles.chevron, {[styles.hidden]: !collapsible, [styles.collapsed]: collapsed})}>
                <ChevronDownIcon size={12}/>
            </span>
            <span className={styles.title}>{title}</span>
        </button>
        {!collapsed && <div className={styles.items}>{children}</div>}
    </div>
);

export default SpacesCategory;
