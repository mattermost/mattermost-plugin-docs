// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getBrowserHistory} from 'webapp_globals';

import {HostModalIds, openHostModal} from 'actions/host_modals';

import type {DocsThunkAction} from 'types/store';

const LEARN_ABOUT_TEAMS_URL = 'https://mattermost.com/pl/mattermost-academy-team-training';

export const invitePeople = (): DocsThunkAction => openHostModal(HostModalIds.INVITATION);
export const openTeamSettings = (): DocsThunkAction => openHostModal(HostModalIds.TEAM_SETTINGS, {isOpen: true});
export const manageMembers = (): DocsThunkAction => openHostModal(HostModalIds.TEAM_MEMBERS);
export const leaveTeam = (): DocsThunkAction => openHostModal(HostModalIds.LEAVE_TEAM);

export const createTeam = (): DocsThunkAction => () => {
    getBrowserHistory()?.push('/create_team');
};

export const learnAboutTeams = (): DocsThunkAction => () => {
    window.open(LEARN_ABOUT_TEAMS_URL, '_blank', 'noopener,noreferrer');
};
