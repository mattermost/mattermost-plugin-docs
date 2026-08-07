// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useTextOverflow} from 'hooks/text_overflow';
import React from 'react';
import {Link} from 'react-router-dom';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import styles from './sidebar_item.module.scss';

type Props = {
    leading: React.ReactNode;
    label: React.ReactNode;
    active?: boolean;
    muted?: boolean;

    // The label as plain text, for the tooltip a clipped row falls back to. Rows
    // whose label cannot be clipped (a fixed one that always fits) can omit it.
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
    const [labelClipped, labelRef] = useTextOverflow();

    const labelText = (
        <span
            ref={labelRef}
            className={classNames(styles.label, {[styles.bright]: active || !muted})}
        >
            {label}
        </span>
    );

    const content = (
        <>
            <span className={styles.icon}>{leading}</span>
            <span className={styles.content}>
                {title ? (

                    // Only a clipped label gets a tooltip; a fully visible one would
                    // just repeat the text under the pointer.
                    <WithTooltip
                        title={title}
                        disabled={!labelClipped}
                    >
                        {labelText}
                    </WithTooltip>
                ) : labelText}
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
                    draggable={draggable}
                    onClick={onClick}
                >
                    {content}
                </Link>
            ) : (
                <button
                    type='button'
                    className={styles.button}
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
