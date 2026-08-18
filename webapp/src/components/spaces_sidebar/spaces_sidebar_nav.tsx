// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import HomeVariantOutlineIcon from '@mattermost/compass-icons/components/home-variant-outline';

import SidebarItem from './sidebar_item';
import styles from './spaces_sidebar_nav.module.scss';

// Notifications and Drafts are intentionally omitted from the initial version;
// only Home ships for now. The key type stays broad for when they return.
export type DocsNavKey = 'home' | 'notifications' | 'drafts';

type Props = {
    active: DocsNavKey | null;
    homeHref: string;
};

const SpacesSidebarNav = ({active, homeHref}: Props) => {
    const {formatMessage} = useIntl();

    return (
        <div className={styles.root}>
            <SidebarItem
                leading={<HomeVariantOutlineIcon size={16}/>}
                label={formatMessage({id: 'docs.sidebar.nav.home', defaultMessage: 'Home'})}
                active={active === 'home'}
                to={homeHref}
            />
        </div>
    );
};

export default SpacesSidebarNav;
