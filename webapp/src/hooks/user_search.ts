// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useEffect, useMemo, useState} from 'react';

import type {UserProfile} from '@mattermost/types/users';

import {searchProfiles} from 'mattermost-redux/actions/users';
import {Client4} from 'mattermost-redux/client';
import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {getCurrentTeamId} from 'mattermost-redux/selectors/entities/teams';
import {displayUsername} from 'mattermost-redux/utils/user_utils';

const SEARCH_DEBOUNCE_MS = 300;
const SEARCH_LIMIT = 20;

export type UserSearchResult = {
    results: MemberProfile[];
    loading: boolean;
};

// Server-side people search for the share picker, debounced and scoped to the
// current team (the Playbooks pattern: searchProfiles into the host store, read
// the returned profiles). Excludes ids already shown (existing members) so the
// suggestions only offer people you can still add.
export function useUserSearch(term: string, excludeIds: string[]): UserSearchResult {
    const dispatch = useAppDispatch();
    const teamId = useAppSelector(getCurrentTeamId);
    const nameDisplay = useAppSelector(getTeammateNameDisplaySetting) || '';

    const [profiles, setProfiles] = useState<UserProfile[]>([]);
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        const trimmed = term.trim();
        if (!trimmed) {
            setProfiles([]);
            setLoading(false);
            return undefined;
        }

        setProfiles([]);
        setLoading(true);
        let cancelled = false;

        const handle = window.setTimeout(async () => {
            try {
                const {data} = await dispatch(searchProfiles(trimmed, {team_id: teamId, limit: SEARCH_LIMIT}));
                if (!cancelled) {
                    setProfiles(data ?? []);
                }
            } catch {
                if (!cancelled) {
                    setProfiles([]);
                }
            } finally {
                if (!cancelled) {
                    setLoading(false);
                }
            }
        }, SEARCH_DEBOUNCE_MS);

        return () => {
            cancelled = true;
            window.clearTimeout(handle);
        };
    }, [dispatch, term, teamId]);

    const exclude = useMemo(() => new Set(excludeIds), [excludeIds]);

    const results = useMemo(() => profiles.
        filter((profile) => !exclude.has(profile.id)).
        map((profile) => ({
            id: profile.id,
            displayName: displayUsername(profile, nameDisplay),
            username: profile.username,
            avatarUrl: Client4.getProfilePictureUrl(profile.id, profile.last_picture_update),
        })), [profiles, exclude, nameDisplay]);

    return {results, loading};
}
