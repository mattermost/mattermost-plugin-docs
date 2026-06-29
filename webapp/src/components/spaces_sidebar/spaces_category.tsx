// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import './spaces_category.scss';

type Props = {
    title: React.ReactNode;
    collapsible?: boolean;
    collapsed?: boolean;
    onToggle?: () => void;
    children: React.ReactNode;
};

const SpacesCategory = ({title, collapsible = true, collapsed = false, onToggle, children}: Props) => (
    <div className='DocsCategory'>
        <button
            type='button'
            className={classNames('DocsCategory__header', {'DocsCategory__header--static': !collapsible})}
            onClick={collapsible ? onToggle : undefined}
        >
            <span className={classNames('DocsCategory__chevron', {'DocsCategory__chevron--hidden': !collapsible, 'DocsCategory__chevron--collapsed': collapsed})}>
                <ChevronDownIcon size={12}/>
            </span>
            <span className='DocsCategory__title'>{title}</span>
        </button>
        {!collapsed && <div className='DocsCategory__items'>{children}</div>}
    </div>
);

export default SpacesCategory;
