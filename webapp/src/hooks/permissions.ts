// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppSelector} from 'hooks/redux';

import {getCanManageSpaceMembers} from 'store/permissions';

export const useCanManageSpaceMembers = (spaceId: string): boolean =>
    useAppSelector((state) => getCanManageSpaceMembers(state, spaceId));
