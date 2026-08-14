// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {GlobalState} from '@mattermost/types/store';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {getSpaceMemberIds} from './selectors';

// Temporary membership gate. The selector/hook seam stays stable when the
// server-backed capability pipeline replaces this basic check.
export const getCanManageSpaceMembers = (state: GlobalState, spaceId: string): boolean =>
    getSpaceMemberIds(state, spaceId).includes(getCurrentUserId(state));
