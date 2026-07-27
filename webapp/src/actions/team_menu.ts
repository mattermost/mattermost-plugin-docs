// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {HostModalIds, openHostModal} from 'actions/host_modals';

import type {DocsThunkAction} from 'types/store';

export const invitePeople = (): DocsThunkAction => openHostModal(HostModalIds.INVITATION);
export const openTeamSettings = (): DocsThunkAction => openHostModal(HostModalIds.TEAM_SETTINGS, {isOpen: true});
export const manageMembers = (): DocsThunkAction => openHostModal(HostModalIds.TEAM_MEMBERS);
export const leaveTeam = (): DocsThunkAction => openHostModal(HostModalIds.LEAVE_TEAM);
