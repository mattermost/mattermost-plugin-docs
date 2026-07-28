// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

import {getCurrentTeamId, getMyTeams} from 'mattermost-redux/selectors/entities/teams';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import type {CreatePageInput, CreateSpaceInput, Page, Space, UpdateSpacePatch} from 'types/docs';
import type {DocsThunkAction} from 'types/store';

import {PageTypes, SpaceTypes} from './action_types';
import {getPage, getSpace} from './selectors';

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

// Loads a space's pages into the store (backs the page count today, the page
// tree later). Best-effort: a failure leaves the count at its current value.
export function fetchPages(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        try {
            const pages = await docsDataSource.listPages(spaceId);
            dispatch({type: PageTypes.RECEIVED_PAGES, pages});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load pages', error);
        }
    };
}

// Reparents/reorders a page. Optimistically reindexes the store, then reconciles
// with the server-returned page. On failure it re-fetches the space's pages to
// restore server truth. siblingIndex is 0-based within the new parent;
// parentId '' is the space root.
export function movePage(spaceId: string, pageId: string, parentId: string, siblingIndex: number): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const page = getPage(getState(), pageId);
        if (!page) {
            return;
        }
        const expectedUpdateAt = page.update_at;

        dispatch({type: PageTypes.MOVED_PAGE, pageId, spaceId, parentId, siblingIndex});

        try {
            const moved = await docsDataSource.movePage(spaceId, pageId, parentId, siblingIndex, expectedUpdateAt);
            dispatch({type: PageTypes.RECEIVED_PAGES, pages: [moved]});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to move page', error);
            dispatch(fetchPages(spaceId));
        }
    };
}

// Creates a page in a space (optionally under a parent) and returns the
// server-assigned entity (rejects on failure so the caller can surface it).
export function createPage(spaceId: string, input: CreatePageInput): DocsThunkAction<Promise<Page>> {
    return async (dispatch) => {
        const page = await docsDataSource.createPage(spaceId, input);
        dispatch({type: PageTypes.RECEIVED_PAGES, pages: [page]});
        return page;
    };
}

// Loads a space's members (user ids) into the store, backing the member count.
export function fetchSpaceMembers(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        try {
            const members = await docsDataSource.listSpaceMembers(spaceId);
            dispatch({type: SpaceTypes.RECEIVED_SPACE_MEMBERS, spaceId, userIds: members.map((m) => m.user_id)});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load space members', error);
        }
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

// Patches a space's editable fields and reconciles the store with the
// server-returned space. Reads the current update_at for optimistic concurrency
// (the server rejects a stale write). Rejects on failure so the settings form
// can surface it and stay open.
export function updateSpace(spaceId: string, patch: UpdateSpacePatch): DocsThunkAction<Promise<Space>> {
    return async (dispatch, getState) => {
        const expectedUpdateAt = getSpace(getState(), spaceId)?.update_at ?? 0;
        const updated = await docsDataSource.updateSpace(spaceId, patch, expectedUpdateAt);
        dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: [updated]});
        return updated;
    };
}

// Archives (soft-deletes) a space and prunes it from the store. Rejects on
// failure so the caller can surface it.
export function deleteSpace(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.deleteSpace(spaceId);
        dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
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
