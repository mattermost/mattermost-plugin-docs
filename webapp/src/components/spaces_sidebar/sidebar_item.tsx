// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import styles from './sidebar_item.module.scss';

type Props = {
    leading: React.ReactNode;
    label: React.ReactNode;
    active?: boolean;
    muted?: boolean;
    title?: string;
    trailing?: React.ReactNode;
    revealTrailingOnHover?: boolean;
    onClick?: () => void;
};

const SidebarItem = ({leading, label, active = false, muted = true, title, trailing, revealTrailingOnHover = false, onClick}: Props) => (
    <div className={classNames(styles.root, {[styles.active]: active})}>
        {active && <span className={styles.activeBar}/>}
        <button
            type='button'
            className={styles.button}
            title={title}
            onClick={onClick}
        >
            <span className={styles.icon}>{leading}</span>
            <span className={styles.content}>
                <span className={classNames(styles.label, {[styles.bright]: active || !muted})}>
                    {label}
                </span>
            </span>
        </button>
        {trailing && (
            <span className={classNames(styles.trailing, {[styles.reveal]: revealTrailingOnHover})}>
                {trailing}
            </span>
        )}
    </div>
);

export default SidebarItem;
