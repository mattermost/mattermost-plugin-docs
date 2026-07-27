// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import React from 'react';
import {useIntl} from 'react-intl';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import AccountMultiplePlusOutlineIcon from '@mattermost/compass-icons/components/account-multiple-plus-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LightbulbOutlineIcon from '@mattermost/compass-icons/components/lightbulb-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';

import {invitePeople, leaveTeam, manageMembers, openTeamSettings} from 'actions/team_menu';

import Menu from 'components/menu/menu';
import type {MenuItemSpec} from 'components/menu/menu_types';

import styles from './spaces_sidebar_header.module.scss';

const CREATE_TEAM_URL = '/create_team';
const LEARN_ABOUT_TEAMS_URL = 'https://mattermost.com/pl/mattermost-academy-team-training';

type Props = {
    teamName: string;
    teamDescription?: string;
    onCreateSpace?: () => void;
    onBrowseSpaces?: () => void;
};

const SpacesSidebarHeader = ({teamName, teamDescription, onCreateSpace, onBrowseSpaces}: Props) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();

    const teamItems: MenuItemSpec[] = [
        {
            id: 'invite',
            label: formatMessage({id: 'docs.sidebar.team.invite', defaultMessage: 'Invite people'}),
            secondaryLabel: formatMessage({id: 'docs.sidebar.team.invite.secondary', defaultMessage: 'Add or invite people to the team'}),
            leadingIcon: <AccountMultiplePlusOutlineIcon size={18}/>,
            onClick: () => dispatch(invitePeople()),
        },
        {id: 'settings', label: formatMessage({id: 'docs.sidebar.team.settings', defaultMessage: 'Team settings'}), leadingIcon: <SettingsOutlineIcon size={18}/>, onClick: () => dispatch(openTeamSettings())},
        {id: 'members', label: formatMessage({id: 'docs.sidebar.team.members', defaultMessage: 'Manage members'}), leadingIcon: <AccountMultipleOutlineIcon size={18}/>, onClick: () => dispatch(manageMembers())},
        {id: 'leave', label: formatMessage({id: 'docs.sidebar.team.leave', defaultMessage: 'Leave team'}), leadingIcon: <ExitToAppIcon size={18}/>, isDestructive: true, onClick: () => dispatch(leaveTeam())},
        {id: 'createTeam', label: formatMessage({id: 'docs.sidebar.team.createTeam', defaultMessage: 'Create a team'}), leadingIcon: <PlusIcon size={18}/>, hasDivider: true, href: CREATE_TEAM_URL},
        {id: 'learn', label: formatMessage({id: 'docs.sidebar.team.learn', defaultMessage: 'Learn about teams'}), leadingIcon: <LightbulbOutlineIcon size={18}/>, isLink: true, hasDivider: true, href: LEARN_ABOUT_TEAMS_URL, external: true},
    ];

    const addItems: MenuItemSpec[] = [
        {id: 'create', label: formatMessage({id: 'docs.sidebar.add.create', defaultMessage: 'Create a space'}), leadingIcon: <PlusIcon size={18}/>, onClick: onCreateSpace},
        {id: 'browse', label: formatMessage({id: 'docs.sidebar.add.browse', defaultMessage: 'Browse spaces'}), leadingIcon: <GlobeIcon size={18}/>, onClick: onBrowseSpaces},
    ];

    return (
        <div className={styles.root}>
            <Menu
                ariaLabel={formatMessage({id: 'docs.sidebar.team.menu', defaultMessage: 'Manage {teamName}'}, {teamName})}
                items={teamItems}
                tooltip={teamDescription || teamName}
                trigger={(
                    <button
                        type='button'
                        className={styles.dropdown}
                    >
                        <span className={styles.teamName}>{teamName}</span>
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
                        className={styles.add}
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
