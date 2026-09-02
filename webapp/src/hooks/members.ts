// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useEffect, useMemo} from 'react';
import {shallowEqual} from 'react-redux';

import {getMissingProfilesByIds} from 'mattermost-redux/actions/users';
import {Client4} from 'mattermost-redux/client';
import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {getUser, makeGetUsersByIds} from 'mattermost-redux/selectors/entities/users';
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
    const user = useAppSelector((state) => (userId ? getUser(state, userId) : undefined));
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
        return {
            id: userId,
            displayName: displayUsername(user, nameDisplay),
            username: user?.username ?? '',
            avatarUrl: Client4.getProfilePictureUrl(userId, user?.last_picture_update ?? 0),
        };
    }, [userId, user, nameDisplay]);
}

// A space's member ids, for callers that resolve profiles themselves (core's
// Avatars fetches any it's missing).
export function useSpaceMemberIds(spaceId: string): string[] {
    return useAppSelector((state) => getSpaceMemberIds(state, spaceId));
}

// Resolves a space's member ids (from the Docs store) to display profiles,
// fetching any not yet in the host store. Member ids are loaded by
// fetchSpaceMembers (see useSpaceStats).
export function useSpaceMemberProfiles(spaceId: string): MemberProfile[] {
    const dispatch = useAppDispatch();
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, spaceId));
    const getUsersByIds = useMemo(() => makeGetUsersByIds(), []);
    const users = useAppSelector((state) => getUsersByIds(state, memberIds), shallowEqual);
    const nameDisplay = useAppSelector(getTeammateNameDisplaySetting) || '';

    useEffect(() => {
        if (memberIds.length) {
            dispatch(getMissingProfilesByIds(memberIds));
        }
    }, [dispatch, memberIds]);

    return useMemo(() => memberIds.map((id, index) => {
        const user = users[index];
        return {
            id,
            displayName: displayUsername(user, nameDisplay),
            username: user?.username ?? '',
            avatarUrl: Client4.getProfilePictureUrl(id, user?.last_picture_update ?? 0),
        };
    }), [memberIds, users, nameDisplay]);
}
