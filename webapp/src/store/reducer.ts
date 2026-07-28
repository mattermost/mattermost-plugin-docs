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
type MovedPageAction = {pageId: string; spaceId: string; parentId: string; siblingIndex: number};
type ReceivedSpaceMembersAction = {spaceId: string; userIds: string[]};

const bySortOrder = (a: Page, b: Page): number =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title);

// Moves a page under `newParentId` at `siblingIndex` and renumbers the affected
// sibling groups' sort_order 0-based, mirroring the server's reindex. Returns a
// new byId map (untouched pages are shared by reference). Only pages in
// `spaceId` are considered; a pure reorder (same parent) skips the old group.
export function reindexAfterMove(
    byId: Record<string, Page>,
    pageId: string,
    spaceId: string,
    newParentId: string,
    siblingIndex: number,
): Record<string, Page> {
    const moved = byId[pageId];
    if (!moved) {
        return byId;
    }
    const oldParentId = moved.parent_id;
    const next = {...byId};

    const groupOf = (parentId: string): Page[] => Object.values(byId).
        filter((page) => page.space_id === spaceId && page.parent_id === parentId && page.id !== pageId).
        sort(bySortOrder);

    const newGroup = groupOf(newParentId);
    const index = Math.max(0, Math.min(siblingIndex, newGroup.length));
    newGroup.splice(index, 0, moved);
    newGroup.forEach((page, i) => {
        next[page.id] = {...page, parent_id: newParentId, sort_order: i};
    });

    if (oldParentId !== newParentId) {
        groupOf(oldParentId).forEach((page, i) => {
            next[page.id] = {...page, sort_order: i};
        });
    }

    return next;
}

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
    case PageTypes.MOVED_PAGE: {
        const {pageId, spaceId, parentId, siblingIndex} = action as unknown as MovedPageAction;
        if (!(pageId in state)) {
            return state;
        }
        return reindexAfterMove(state, pageId, spaceId, parentId, siblingIndex);
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

// Space member user ids, keyed by space id. Roles/capabilities are hidden by
// the server, so this is just membership (count today, avatars later).
function spaceMembers(state: Record<string, string[]> = {}, action: UnknownAction): Record<string, string[]> {
    switch (action.type) {
    case SpaceTypes.RECEIVED_SPACE_MEMBERS: {
        const {spaceId, userIds} = action as unknown as ReceivedSpaceMembersAction;
        return {...state, [spaceId]: userIds};
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

const reducer = combineReducers({spaces, spacesInTeam, pages, pagesInSpace, spaceMembers});

export default reducer;
