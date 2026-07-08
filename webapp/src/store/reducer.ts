// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers} from 'redux';
import type {UnknownAction} from 'redux';

import type {Page, Space} from 'types/docs';

import {PageTypes, SpaceTypes} from './action_types';

type ReceivedSpacesAction = {spaces: Space[]};
type CreatedSpaceAction = {space: Space};
type DeletedSpaceAction = {spaceId: string};

// SpaceTypes' values aren't string-literal types (manifest.id is loaded via
// JSON.parse), so `action.type` can't discriminate a union by itself — each
// case casts to its own shape instead, mirroring the Playbooks reducer.

function spacesById(state: Record<string, Space> = {}, action: UnknownAction): Record<string, Space> {
    switch (action.type) {
    case SpaceTypes.RECEIVED_SPACES: {
        const receivedAction = action as unknown as ReceivedSpacesAction;
        const next = {...state};
        for (const space of receivedAction.spaces) {
            next[space.id] = space;
        }
        return next;
    }
    case SpaceTypes.CREATED_SPACE: {
        const createdAction = action as unknown as CreatedSpaceAction;
        return {...state, [createdAction.space.id]: createdAction.space};
    }
    case SpaceTypes.DELETED_SPACE: {
        const deletedAction = action as unknown as DeletedSpaceAction;
        const next = {...state};
        delete next[deletedAction.spaceId];
        return next;
    }
    default:
        return state;
    }
}

function spacesOrder(state: string[] = [], action: UnknownAction): string[] {
    switch (action.type) {
    case SpaceTypes.RECEIVED_SPACES: {
        const receivedAction = action as unknown as ReceivedSpacesAction;
        const incoming = receivedAction.spaces.map((space) => space.id).filter((id) => !state.includes(id));
        return incoming.length === 0 ? state : [...state, ...incoming];
    }
    case SpaceTypes.CREATED_SPACE: {
        const createdAction = action as unknown as CreatedSpaceAction;
        return [createdAction.space.id, ...state.filter((id) => id !== createdAction.space.id)];
    }
    case SpaceTypes.DELETED_SPACE: {
        const deletedAction = action as unknown as DeletedSpaceAction;
        return state.filter((id) => id !== deletedAction.spaceId);
    }
    default:
        return state;
    }
}

const spaces = combineReducers({byId: spacesById, order: spacesOrder});

type ReceivedPagesAction = {pages: Page[]};

function pagesById(state: Record<string, Page> = {}, action: UnknownAction): Record<string, Page> {
    switch (action.type) {
    case PageTypes.RECEIVED_PAGES: {
        const receivedAction = action as unknown as ReceivedPagesAction;
        if (receivedAction.pages.length === 0) {
            return state;
        }
        const next = {...state};
        for (const page of receivedAction.pages) {
            next[page.id] = page;
        }
        return next;
    }
    case SpaceTypes.DELETED_SPACE: {
        const deletedAction = action as unknown as DeletedSpaceAction;
        const remaining = Object.entries(state).filter(([, page]) => page.space_id !== deletedAction.spaceId);
        return remaining.length === Object.keys(state).length ? state : Object.fromEntries(remaining);
    }
    default:
        return state;
    }
}

function pagesBySpace(state: Record<string, string[]> = {}, action: UnknownAction): Record<string, string[]> {
    switch (action.type) {
    case PageTypes.RECEIVED_PAGES: {
        const receivedAction = action as unknown as ReceivedPagesAction;
        if (receivedAction.pages.length === 0) {
            return state;
        }
        const next = {...state};
        for (const page of receivedAction.pages) {
            const ids = next[page.space_id] ?? [];
            next[page.space_id] = ids.includes(page.id) ? ids : [...ids, page.id];
        }
        return next;
    }
    case SpaceTypes.DELETED_SPACE: {
        const deletedAction = action as unknown as DeletedSpaceAction;
        if (!(deletedAction.spaceId in state)) {
            return state;
        }
        const next = {...state};
        delete next[deletedAction.spaceId];
        return next;
    }
    default:
        return state;
    }
}

const pages = combineReducers({byId: pagesById, bySpace: pagesBySpace});

const reducer = combineReducers({spaces, pages});

export default reducer;
