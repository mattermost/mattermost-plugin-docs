// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useEffect, useMemo} from 'react';

import {getMissingProfilesByIds} from 'mattermost-redux/actions/users';
import {Client4} from 'mattermost-redux/client';
import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {getUsers} from 'mattermost-redux/selectors/entities/users';
import {displayUsername} from 'mattermost-redux/utils/user_utils';

import {getSpaceMemberIds} from 'store/selectors';

export type MemberProfile = {
    id: string;
    displayName: string;
    username: string;
    avatarUrl: string;
};

// Resolves a single user id to a display profile, fetching it if the host store
// doesn't have it yet (e.g. a page's author who isn't a loaded space member).
export function useUserProfile(userId?: string): MemberProfile | undefined {
    const dispatch = useAppDispatch();
    const usersById = useAppSelector(getUsers);
    const nameDisplay = useAppSelector(getTeammateNameDisplaySetting) || '';

    useEffect(() => {
        if (userId) {
            dispatch(getMissingProfilesByIds([userId]));
        }
    }, [dispatch, userId]);

    return useMemo(() => {
        if (!userId) {
            return undefined;
        }
        const user = usersById[userId];
        return {
            id: userId,
            displayName: displayUsername(user, nameDisplay),
            username: user?.username ?? '',
            avatarUrl: Client4.getProfilePictureUrl(userId, user?.last_picture_update),
        };
    }, [userId, usersById, nameDisplay]);
}

// Resolves a space's member ids (from the Docs store) to display profiles,
// fetching any not yet in the host store. Member ids are loaded by
// fetchSpaceMembers (see useSpaceStats).
export function useSpaceMemberProfiles(spaceId: string): MemberProfile[] {
    const dispatch = useAppDispatch();
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, spaceId));
    const usersById = useAppSelector(getUsers);
    const nameDisplay = useAppSelector(getTeammateNameDisplaySetting) || '';

    useEffect(() => {
        if (memberIds.length) {
            dispatch(getMissingProfilesByIds(memberIds));
        }
    }, [dispatch, memberIds]);

    return useMemo(() => memberIds.map((id) => {
        const user = usersById[id];
        return {
            id,
            displayName: displayUsername(user, nameDisplay),
            username: user?.username ?? '',
            avatarUrl: Client4.getProfilePictureUrl(id, user?.last_picture_update),
        };
    }), [memberIds, usersById, nameDisplay]);
}
