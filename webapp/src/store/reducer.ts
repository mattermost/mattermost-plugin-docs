// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers} from 'redux';
import type {UnknownAction} from 'redux';

import type {Page, Space} from 'types/docs';

import {PageTypes, SpaceTypes} from './action_types';

type ReceivedSpacesAction = {spaces: Space[]};
type CreatedSpaceAction = {space: Space};
type DeletedSpaceAction = {spaceId: string};
type ReceivedPagesAction = {pages: Page[]};

// SpaceTypes'/PageTypes' values aren't string-literal types (manifest.id is
// loaded via JSON.parse), so `action.type` can't discriminate a union by
// itself — each case casts to its own shape, mirroring the core channels
// reducer.

// Normalized entity maps (byId) and per-parent Set indices, modeled on core's
// `channels` + `channelsInTeam`. Sets give O(1) membership/add/remove so
// high-throughput WebSocket events touch only the affected entity and index.

function spaces(state: Record<string, Space> = {}, action: UnknownAction): Record<string, Space> {
    switch (action.type) {
    case SpaceTypes.RECEIVED_SPACES: {
        const {spaces: received} = action as unknown as ReceivedSpacesAction;
        if (received.length === 0) {
            return state;
        }
        const next = {...state};
        for (const space of received) {
            next[space.id] = space;
        }
        return next;
    }
    case SpaceTypes.CREATED_SPACE: {
        const {space} = action as unknown as CreatedSpaceAction;
        return {...state, [space.id]: space};
    }
    case SpaceTypes.DELETED_SPACE: {
        const {spaceId} = action as unknown as DeletedSpaceAction;
        if (!(spaceId in state)) {
            return state;
        }
        const next = {...state};
        delete next[spaceId];
        return next;
    }
    default:
        return state;
    }
}

function addSpaceToTeam(state: Record<string, Set<string>>, space: Space): Record<string, Set<string>> {
    const next = new Set(state[space.team_id]);
    next.add(space.id);
    return {...state, [space.team_id]: next};
}

function spacesInTeam(state: Record<string, Set<string>> = {}, action: UnknownAction): Record<string, Set<string>> {
    switch (action.type) {
    case SpaceTypes.RECEIVED_SPACES: {
        const {spaces: received} = action as unknown as ReceivedSpacesAction;
        if (received.length === 0) {
            return state;
        }
        const next = {...state};
        for (const space of received) {
            const set = new Set(next[space.team_id]);
            set.add(space.id);
            next[space.team_id] = set;
        }
        return next;
    }
    case SpaceTypes.CREATED_SPACE: {
        const {space} = action as unknown as CreatedSpaceAction;
        return addSpaceToTeam(state, space);
    }
    case SpaceTypes.DELETED_SPACE: {
        const {spaceId} = action as unknown as DeletedSpaceAction;
        const teamId = Object.keys(state).find((id) => state[id].has(spaceId));
        if (teamId === undefined) {
            return state;
        }
        const set = new Set(state[teamId]);
        set.delete(spaceId);
        return {...state, [teamId]: set};
    }
    default:
        return state;
    }
}

function pages(state: Record<string, Page> = {}, action: UnknownAction): Record<string, Page> {
    switch (action.type) {
    case PageTypes.RECEIVED_PAGES: {
        const {pages: received} = action as unknown as ReceivedPagesAction;
        if (received.length === 0) {
            return state;
        }
        const next = {...state};
        for (const page of received) {
            next[page.id] = page;
        }
        return next;
    }
    case SpaceTypes.DELETED_SPACE: {
        const {spaceId} = action as unknown as DeletedSpaceAction;
        const remaining = Object.entries(state).filter(([, page]) => page.space_id !== spaceId);
        return remaining.length === Object.keys(state).length ? state : Object.fromEntries(remaining);
    }
    default:
        return state;
    }
}

function pagesInSpace(state: Record<string, Set<string>> = {}, action: UnknownAction): Record<string, Set<string>> {
    switch (action.type) {
    case PageTypes.RECEIVED_PAGES: {
        const {pages: received} = action as unknown as ReceivedPagesAction;
        if (received.length === 0) {
            return state;
        }
        const next = {...state};
        for (const page of received) {
            const set = new Set(next[page.space_id]);
            set.add(page.id);
            next[page.space_id] = set;
        }
        return next;
    }
    case SpaceTypes.DELETED_SPACE: {
        const {spaceId} = action as unknown as DeletedSpaceAction;
        if (!(spaceId in state)) {
            return state;
        }
        const next = {...state};
        delete next[spaceId];
        return next;
    }
    default:
        return state;
    }
}

const reducer = combineReducers({spaces, spacesInTeam, pages, pagesInSpace});

export default reducer;
