// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {HostModalIds, openHostModal} from 'actions/host_modals';

import type {DocsThunkAction} from 'types/store';

export const SETTINGS_BUTTON_ID = 'docsSettingsButton';

export const openUserSettings = (): DocsThunkAction => openHostModal(HostModalIds.USER_SETTINGS, {
    isContentProductSettings: true,
    focusOriginElement: SETTINGS_BUTTON_ID,
});
