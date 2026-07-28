// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

import {getCurrentTeamId, getMyTeams} from 'mattermost-redux/selectors/entities/teams';

import type {CreateSpaceInput, Space} from 'types/docs';
import type {DocsThunkAction} from 'types/store';

import {PageTypes, SpaceTypes} from './action_types';

// Stable per-id hash so a space always lands in the same team across refetches
// (a plain random would make spaces hop teams on every fetch).
const hashString = (value: string): number => {
    let hash = 0;
    for (let i = 0; i < value.length; i++) {
        hash = ((hash * 31) + value.charCodeAt(i)) | 0;
    }
    return Math.abs(hash);
};

// The mock fixtures aren't team-aware. Spread them across the user's teams
// (deterministically by space id) so team-scoped reads visibly differ per team;
// falls back to the current team when membership isn't loaded. The real API will
// return spaces already scoped to their team, at which point this goes away.
export function fetchSpaces(): DocsThunkAction<void> {
    return (dispatch, getState) => {
        const state = getState();
        const teamIds = getMyTeams(state).map((team) => team.id);
        const fallbackTeamId = getCurrentTeamId(state);
        const spaces = docsDataSource.listSpaces().map((space) => ({
            ...space,
            team_id: teamIds.length ? teamIds[hashString(space.id) % teamIds.length] : fallbackTeamId,
        }));
        dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces});
    };
}

// Bootstraps pages for one space, or for every known space when called with no
// argument — there's no bulk "list all pages" on the data source yet.
export function fetchPages(spaceId?: string): DocsThunkAction<void> {
    return (dispatch) => {
        const spaceIds = spaceId ? [spaceId] : docsDataSource.listSpaces().map((space) => space.id);
        const pages = spaceIds.flatMap((id) => docsDataSource.listPages(id));
        dispatch({type: PageTypes.RECEIVED_PAGES, pages});
    };
}

export function createSpace(input: CreateSpaceInput): DocsThunkAction<Space> {
    return (dispatch, getState) => {
        const teamId = getCurrentTeamId(getState());
        const space = {...docsDataSource.createSpace(input), team_id: teamId};
        dispatch({type: SpaceTypes.CREATED_SPACE, space});
        return space;
    };
}

export function leaveSpace(spaceId: string): DocsThunkAction<void> {
    return (dispatch) => {
        dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
    };
}
