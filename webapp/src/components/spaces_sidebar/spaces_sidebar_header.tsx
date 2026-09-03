// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import React from 'react';
import {useIntl} from 'react-intl';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import AccountMultiplePlusOutlineIcon from '@mattermost/compass-icons/components/account-multiple-plus-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import LightbulbOutlineIcon from '@mattermost/compass-icons/components/lightbulb-outline';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';

import {invitePeople, leaveTeam, manageMembers, openTeamSettings} from 'actions/team_menu';

import Menu from 'components/menu/menu';

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

    const addMenuLabel = formatMessage({id: 'docs.sidebar.add.menu', defaultMessage: 'Add or browse spaces'});

    return (
        <div className={styles.root}>
            <Menu
                ariaLabel={formatMessage({id: 'docs.sidebar.team.menu', defaultMessage: 'Manage {teamName}'}, {teamName})}
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
            >
                <Menu.Item
                    leadingIcon={<AccountMultiplePlusOutlineIcon size={18}/>}
                    secondaryLabel={formatMessage({id: 'docs.sidebar.team.invite.secondary', defaultMessage: 'Add or invite people to the team'})}
                    onClick={() => dispatch(invitePeople())}
                >
                    {formatMessage({id: 'docs.sidebar.team.invite', defaultMessage: 'Invite people'})}
                </Menu.Item>
                <Menu.Item
                    leadingIcon={<SettingsOutlineIcon size={18}/>}
                    onClick={() => dispatch(openTeamSettings())}
                >
                    {formatMessage({id: 'docs.sidebar.team.settings', defaultMessage: 'Team settings'})}
                </Menu.Item>
                <Menu.Item
                    leadingIcon={<AccountMultipleOutlineIcon size={18}/>}
                    onClick={() => dispatch(manageMembers())}
                >
                    {formatMessage({id: 'docs.sidebar.team.members', defaultMessage: 'Manage members'})}
                </Menu.Item>
                <Menu.Item
                    leadingIcon={<ExitToAppIcon size={18}/>}
                    destructive={true}
                    onClick={() => dispatch(leaveTeam())}
                >
                    {formatMessage({id: 'docs.sidebar.team.leave', defaultMessage: 'Leave team'})}
                </Menu.Item>
                <Menu.Separator/>
                <Menu.LinkItem
                    href={CREATE_TEAM_URL}
                    leadingIcon={<PlusIcon size={18}/>}
                >
                    {formatMessage({id: 'docs.sidebar.team.createTeam', defaultMessage: 'Create a team'})}
                </Menu.LinkItem>
                <Menu.Separator/>
                <Menu.LinkItem
                    href={LEARN_ABOUT_TEAMS_URL}
                    external={true}
                    leadingIcon={<LightbulbOutlineIcon size={18}/>}
                >
                    {formatMessage({id: 'docs.sidebar.team.learn', defaultMessage: 'Learn about teams'})}
                </Menu.LinkItem>
            </Menu>
            <Menu
                ariaLabel={addMenuLabel}
                align='right'
                tooltip={addMenuLabel}
                trigger={(
                    <button
                        type='button'
                        className={styles.add}
                        aria-label={addMenuLabel}
                    >
                        <PlusIcon size={16}/>
                    </button>
                )}
            >
                {onCreateSpace && (
                    <Menu.Item
                        leadingIcon={<PlusIcon size={18}/>}
                        onClick={onCreateSpace}
                    >
                        {formatMessage({id: 'docs.sidebar.add.create', defaultMessage: 'Create a space'})}
                    </Menu.Item>
                )}
                <Menu.Item
                    leadingIcon={<LockOutlineIcon size={18}/>}
                    onClick={onBrowseSpaces}
                >
                    {formatMessage({id: 'docs.sidebar.add.browse', defaultMessage: 'Browse spaces'})}
                </Menu.Item>
            </Menu>
        </div>
    );
};

export default SpacesSidebarHeader;
