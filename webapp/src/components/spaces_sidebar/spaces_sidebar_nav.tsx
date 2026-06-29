// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import BellOutlineIcon from '@mattermost/compass-icons/components/bell-outline';
import HomeVariantOutlineIcon from '@mattermost/compass-icons/components/home-variant-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';

import SidebarItem from './sidebar_item';
import './spaces_sidebar_nav.scss';

export type DocsNavKey = 'home' | 'notifications' | 'drafts';

type Props = {
    active: DocsNavKey | null;
    onSelect: (key: DocsNavKey) => void;
};

const SpacesSidebarNav = ({active, onSelect}: Props) => {
    const {formatMessage} = useIntl();

    return (
        <div className='DocumentationSidebarNav'>
            <SidebarItem
                leading={<HomeVariantOutlineIcon size={16}/>}
                label={formatMessage({id: 'docs.sidebar.nav.home', defaultMessage: 'Home'})}
                active={active === 'home'}
                onClick={() => onSelect('home')}
            />
            <SidebarItem
                leading={<BellOutlineIcon size={16}/>}
                label={formatMessage({id: 'docs.sidebar.nav.notifications', defaultMessage: 'Notifications'})}
                active={active === 'notifications'}
                onClick={() => onSelect('notifications')}
            />
            <SidebarItem
                leading={<PencilOutlineIcon size={16}/>}
                label={formatMessage({id: 'docs.sidebar.nav.drafts', defaultMessage: 'Drafts'})}
                active={active === 'drafts'}
                onClick={() => onSelect('drafts')}
            />
        </div>
    );
};

export default SpacesSidebarNav;
