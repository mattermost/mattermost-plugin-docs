// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppSelector} from 'hooks/redux';

import {getCanAdministerSpace, getCanCreatePage, getCanDeletePage, getCanDeleteSpace, getCanEditPage, getCanManageSpaceMembers} from 'store/permissions';

export const useCanManageSpaceMembers = (spaceId: string): boolean =>
    useAppSelector((state) => getCanManageSpaceMembers(state, spaceId));

export const useCanAdministerSpace = (spaceId: string): boolean =>
    useAppSelector((state) => getCanAdministerSpace(state, spaceId));

export const useCanDeleteSpace = (spaceId: string): boolean =>
    useAppSelector((state) => getCanDeleteSpace(state, spaceId));

export const useCanCreatePage = (spaceId: string): boolean =>
    useAppSelector((state) => getCanCreatePage(state, spaceId));

export const useCanEditPage = (spaceId: string): boolean =>
    useAppSelector((state) => getCanEditPage(state, spaceId));

// pageAuthorId decides the delete_own_page half, so this is per-page rather than per-space.
export const useCanDeletePage = (spaceId: string, pageAuthorId: string): boolean =>
    useAppSelector((state) => getCanDeletePage(state, spaceId, pageAuthorId));
