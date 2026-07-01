// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {hostOpenModalAction} from 'webapp_globals';

import type {DocsThunkAction} from 'types/store';

export const HostModalIds = {
    USER_SETTINGS: 'user_settings',
    INVITATION: 'invitation',
    TEAM_SETTINGS: 'team_settings',
    TEAM_MEMBERS: 'team_members',
    LEAVE_TEAM: 'leave_team',
} as const;

export const openHostModal = (modalId: string, dialogProps?: Record<string, unknown>): DocsThunkAction => (dispatch) => {
    const action = hostOpenModalAction(modalId, dialogProps);
    if (action) {
        dispatch(action);
    }
};
