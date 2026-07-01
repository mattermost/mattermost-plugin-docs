// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsDispatch} from 'hooks/redux';
import {useEffect} from 'react';
import {useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import {selectTeam} from 'mattermost-redux/actions/teams';
import {getCurrentTeam, getCurrentTeamId, getMyTeams} from 'mattermost-redux/selectors/entities/teams';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

type TeamContext = {
    id: string;
    name: string;
    displayName: string;
    description: string;
};

export function useTeamContext(): TeamContext {
    const team = useSelector((state: GlobalState) => getCurrentTeam(state));

    return {
        id: team?.id ?? '',
        name: team?.name ?? '',
        displayName: team?.display_name ?? '',
        description: team?.description ?? '',
    };
}

// Mattermost webapp (and the Playbooks product) persist the user's last-active
// team under `user_prev_team:{userId}`. Docs shares the key so the team you
// leave in Channels — or here — is restored across products and refreshes.
const previousTeamIdKey = (userId: string) => `user_prev_team:${userId}`;

function readPreviousTeamId(userId: string): string | null {
    try {
        return window.localStorage.getItem(previousTeamIdKey(userId));
    } catch {
        return null;
    }
}

function writePreviousTeamId(userId: string, teamId: string) {
    try {
        window.localStorage.setItem(previousTeamIdKey(userId), teamId);
    } catch {
        // Ignore storage failures (private mode, quota); the next refresh just
        // falls back to a default team.
    }
}

// Docs is a global product (/docs, no team segment), so on a hard refresh there
// is no team in the URL for the host to select and `currentTeamId` can be empty.
// Restore the last-active team from localStorage — validated against the user's
// teams, with a first-team fallback — and keep it persisted, mirroring Channels
// and Playbooks. Call once from the product root.
export function useEnsureCurrentTeam(): void {
    const dispatch = useDocsDispatch();
    const userId = useSelector(getCurrentUserId);
    const currentTeamId = useSelector(getCurrentTeamId);
    const myTeams = useSelector(getMyTeams);

    useEffect(() => {
        if (currentTeamId || !userId) {
            return;
        }

        const restored = readPreviousTeamId(userId);
        const teamId = restored && myTeams.some((team) => team.id === restored) ? restored : (myTeams[0]?.id ?? '');
        if (teamId) {
            dispatch(selectTeam(teamId));
        }
    }, [currentTeamId, userId, myTeams, dispatch]);

    useEffect(() => {
        if (currentTeamId && userId) {
            writePreviousTeamId(userId, currentTeamId);
        }
    }, [currentTeamId, userId]);
}
