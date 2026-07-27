// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

import {getCurrentTeamId, getMyTeams} from 'mattermost-redux/selectors/entities/teams';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import type {CreateSpaceInput, Space} from 'types/docs';
import type {DocsThunkAction} from 'types/store';

import {PageTypes, SpaceTypes} from './action_types';

// Spaces the caller belongs to in the current team (the server scopes the list
// by backing-channel membership). A failed load leaves the store empty rather
// than crashing the product on mount.
export function fetchSpaces(): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const teamId = getCurrentTeamId(getState());
        if (!teamId) {
            return;
        }
        try {
            const spaces = await docsDataSource.listSpaces(teamId);
            dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load spaces', error);
        }
    };
}

// Cross-team load for the switcher: fan out over the user's teams. The server
// has no all-teams endpoint, so this is N team-scoped calls run in parallel.
export function fetchAllSpaces(): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const teams = getMyTeams(getState());
        try {
            const perTeam = await Promise.all(teams.map((team) => docsDataSource.listSpaces(team.id)));
            dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: perTeam.flat()});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load spaces across teams', error);
        }
    };
}

// Loads a space's pages. Wired for the page tree that lands later; no UI reads
// store pages yet, so this isn't called on bootstrap.
export function fetchPages(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        const pages = await docsDataSource.listPages(spaceId);
        dispatch({type: PageTypes.RECEIVED_PAGES, pages});
    };
}

// Creates a space in the current team and returns the server-assigned entity
// (rejects on failure so the form can surface it).
export function createSpace(input: CreateSpaceInput): DocsThunkAction<Promise<Space>> {
    return async (dispatch, getState) => {
        const teamId = getCurrentTeamId(getState());
        const space = await docsDataSource.createSpace(teamId, input);
        dispatch({type: SpaceTypes.CREATED_SPACE, space});
        return space;
    };
}

// Leaving a space is removing yourself from its membership. The server rejects
// removing the last authorized member (409); the caller surfaces that.
export function leaveSpace(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const userId = getCurrentUserId(getState());
        await docsDataSource.removeSpaceMember(spaceId, userId);
        dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
    };
}
