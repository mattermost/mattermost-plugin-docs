// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import AccountMultiplePlusOutlineIcon from '@mattermost/compass-icons/components/account-multiple-plus-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import FolderPlusOutlineIcon from '@mattermost/compass-icons/components/folder-plus-outline';
import GlobeIcon from '@mattermost/compass-icons/components/globe';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import Menu from 'components/menu/menu';
import type {MenuItemSpec} from 'components/menu/menu_types';

import './spaces_sidebar_header.scss';

type Props = {
    teamName: string;
    onCreateSpace?: () => void;
};

const SpacesSidebarHeader = ({teamName, onCreateSpace}: Props) => {
    const {formatMessage} = useIntl();

    const teamItems: MenuItemSpec[] = [
        {id: 'invite', label: formatMessage({id: 'docs.sidebar.team.invite', defaultMessage: 'Invite people'}), leadingIcon: <AccountMultiplePlusOutlineIcon size={18}/>},
        {id: 'members', label: formatMessage({id: 'docs.sidebar.team.members', defaultMessage: 'Manage members'}), leadingIcon: <AccountMultipleOutlineIcon size={18}/>},
        {id: 'settings', label: formatMessage({id: 'docs.sidebar.team.settings', defaultMessage: 'Team settings'}), leadingIcon: <CogOutlineIcon size={18}/>},
        {id: 'leave', label: formatMessage({id: 'docs.sidebar.team.leave', defaultMessage: 'Leave team'}), leadingIcon: <ExitToAppIcon size={18}/>, isDestructive: true, hasDivider: true},
    ];

    const addItems: MenuItemSpec[] = [
        {id: 'create', label: formatMessage({id: 'docs.sidebar.add.create', defaultMessage: 'Create a space'}), leadingIcon: <PlusIcon size={18}/>, onClick: onCreateSpace},
        {id: 'browse', label: formatMessage({id: 'docs.sidebar.add.browse', defaultMessage: 'Browse spaces'}), leadingIcon: <GlobeIcon size={18}/>},
        {id: 'category', label: formatMessage({id: 'docs.sidebar.add.category', defaultMessage: 'Create a category'}), leadingIcon: <FolderPlusOutlineIcon size={18}/>},
    ];

    return (
        <div className='DocsSidebarHeader'>
            <Menu
                ariaLabel={formatMessage({id: 'docs.sidebar.team.menu', defaultMessage: 'Manage {teamName}'}, {teamName})}
                items={teamItems}
                trigger={(
                    <button
                        type='button'
                        className='DocsSidebarHeader__dropdown'
                        title={formatMessage({id: 'docs.sidebar.team.menu', defaultMessage: 'Manage {teamName}'}, {teamName})}
                    >
                        <span className='DocsSidebarHeader__teamName'>{teamName}</span>
                        <ChevronDownIcon size={16}/>
                    </button>
                )}
            />
            <Menu
                ariaLabel={formatMessage({id: 'docs.sidebar.add.menu', defaultMessage: 'Add or browse spaces'})}
                align='right'
                items={addItems}
                tooltip={formatMessage({id: 'docs.sidebar.add.menu', defaultMessage: 'Add or browse spaces'})}
                trigger={(
                    <button
                        type='button'
                        className='DocsSidebarHeader__add'
                        aria-label={formatMessage({id: 'docs.sidebar.add.menu', defaultMessage: 'Add or browse spaces'})}
                    >
                        <PlusIcon size={16}/>
                    </button>
                )}
            />
        </div>
    );
};

export default SpacesSidebarHeader;
