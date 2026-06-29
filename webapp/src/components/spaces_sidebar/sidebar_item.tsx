// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import './sidebar_item.scss';

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
    <div className={classNames('DocsSidebarItem', {'DocsSidebarItem--active': active})}>
        {active && <span className='DocsSidebarItem__activeBar'/>}
        <button
            type='button'
            className='DocsSidebarItem__button'
            title={title}
            onClick={onClick}
        >
            <span className='DocsSidebarItem__icon'>{leading}</span>
            <span className='DocsSidebarItem__content'>
                <span className={classNames('DocsSidebarItem__label', {'DocsSidebarItem__label--bright': active || !muted})}>
                    {label}
                </span>
            </span>
        </button>
        {trailing && (
            <span className={classNames('DocsSidebarItem__trailing', {'DocsSidebarItem__trailing--reveal': revealTrailingOnHover})}>
                {trailing}
            </span>
        )}
    </div>
);

export default SidebarItem;
