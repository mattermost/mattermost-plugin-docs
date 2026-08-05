// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';
import {Link} from 'react-router-dom';

import styles from './sidebar_item.module.scss';

type Props = {
    leading: React.ReactNode;
    label: React.ReactNode;
    active?: boolean;
    muted?: boolean;
    title?: string;
    trailing?: React.ReactNode;
    revealTrailingOnHover?: boolean;

    // Where the row goes. A row that navigates is an anchor, so it can be opened in
    // a new tab, copied or middle-clicked like any other address. `onClick` is for
    // rows that act rather than navigate.
    to?: string;
    onClick?: () => void;

    // Rows that are drag sources have to opt out of the browser's own link drag,
    // which would otherwise pre-empt the drag-and-drop.
    draggable?: boolean;
};

const SidebarItem = ({leading, label, active = false, muted = true, title, trailing, revealTrailingOnHover = false, to, onClick, draggable}: Props) => {
    const content = (
        <>
            <span className={styles.icon}>{leading}</span>
            <span className={styles.content}>
                <span className={classNames(styles.label, {[styles.bright]: active || !muted})}>
                    {label}
                </span>
            </span>
        </>
    );

    return (
        <div className={classNames(styles.root, {[styles.active]: active})}>
            {active && <span className={styles.activeBar}/>}
            {to ? (
                <Link
                    className={styles.button}
                    to={to}
                    title={title}
                    draggable={draggable}
                    onClick={onClick}
                >
                    {content}
                </Link>
            ) : (
                <button
                    type='button'
                    className={styles.button}
                    title={title}
                    onClick={onClick}
                >
                    {content}
                </button>
            )}
            {trailing && (
                <span className={classNames(styles.trailing, {[styles.reveal]: revealTrailingOnHover})}>
                    {trailing}
                </span>
            )}
        </div>
    );
};

export default SidebarItem;
