// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {hostCanOpenModal, hostOpenModalAction} from 'webapp_globals';

import type {DocsThunkAction} from 'types/store';

export const HostModalIds = {
    USER_SETTINGS: 'user_settings',
    INVITATION: 'invitation',
    TEAM_SETTINGS: 'team_settings',
    TEAM_MEMBERS: 'team_members',
    LEAVE_TEAM: 'leave_team',
} as const;

type HostModalId = typeof HostModalIds[keyof typeof HostModalIds];

export const openHostModal = (modalId: HostModalId, dialogProps?: Record<string, unknown>): DocsThunkAction => (dispatch) => {
    if (!hostCanOpenModal(modalId)) {
        return;
    }

    const action = hostOpenModalAction(modalId, dialogProps);
    if (action) {
        dispatch(action);
    }
};
