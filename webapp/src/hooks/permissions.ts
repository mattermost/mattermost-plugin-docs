// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppSelector} from 'hooks/redux';

import {haveICurrentTeamPermission} from 'mattermost-redux/selectors/entities/roles';

import {getCanAdministerSpace, getCanCreatePage, getCanDeletePage, getCanDeleteSpace, getCanEditPage, getCanManageSpaceMembers, getCustomDefaultsAvailable} from 'store/permissions';

// Team-scoped, unlike the permissions below. Core owns this permission and includes it in the
// current user's resolved team roles; the plugin only decides whether to offer its create flow.
export const useCanCreateSpace = (): boolean =>
    useAppSelector((state) => haveICurrentTeamPermission(state, 'create_space'));

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

// License-scoped rather than space-scoped: whether the default-permission controls may offer a
// combination beyond the named tiers on this server at all.
export const useCustomDefaultsAvailable = (): boolean =>
    useAppSelector(getCustomDefaultsAvailable);
