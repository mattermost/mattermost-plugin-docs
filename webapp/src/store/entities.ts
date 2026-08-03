// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers} from 'redux';
import type {UnknownAction} from 'redux';

import type {Page, Space} from 'types/docs';

import {PageTypes, SpaceTypes} from './action_types';

// `teamId` is set when the action is a full list for that team, which seeds the
// team's index entry even when it has no spaces (see the spacesInTeam reducer).
type ReceivedSpacesAction = {spaces: Space[]; teamId?: string};
type CreatedSpaceAction = {space: Space};
type DeletedSpaceAction = {spaceId: string};
type ReceivedPagesAction = {pages: Page[]; spaceId?: string};
type MovedPageAction = {pageId: string; spaceId: string; parentId: string; siblingIndex: number};

// `pageIds` is the deleted page plus its descendants: the byId map and the
// per-space index are separate slices, so the ids are resolved once by the thunk
// (via collectSubtreeIds) rather than twice from a map only one slice holds.
type DeletedPageAction = {pageId: string; spaceId: string; pageIds: string[]};
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

// The ids of `rootId` and every page beneath it (BFS over parent_id). Deleting a
// page deletes its subpages on the server, so the store prunes the same set.
export function collectSubtreeIds(byId: Record<string, Page>, rootId: string): Set<string> {
    const childIds = new Map<string, string[]>();
    for (const page of Object.values(byId)) {
        childIds.set(page.parent_id, [...childIds.get(page.parent_id) ?? [], page.id]);
    }

    const ids = new Set([rootId]);
    const queue = [rootId];
    while (queue.length > 0) {
        for (const id of childIds.get(queue.shift()!) ?? []) {
            if (!ids.has(id)) {
                ids.add(id);
                queue.push(id);
            }
        }
    }

    return ids;
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
        const {spaces: received, teamId} = action as unknown as ReceivedSpacesAction;
        if (received.length === 0 && (teamId === undefined || teamId in state)) {
            return state;
        }
        const next = {...state};

        // A listed team always gets an entry, so its presence means "this team's
        // spaces are loaded" even when the team has none.
        if (teamId !== undefined && !(teamId in next)) {
            next[teamId] = new Set();
        }
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
    case PageTypes.DELETED_PAGE: {
        const {pageIds} = action as unknown as DeletedPageAction;
        const removed = pageIds.filter((id) => id in state);
        if (removed.length === 0) {
            return state;
        }
        const next = {...state};
        removed.forEach((id) => delete next[id]);
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
        const {pages: received, spaceId} = action as unknown as ReceivedPagesAction;
        if (received.length === 0 && (spaceId === undefined || spaceId in state)) {
            return state;
        }
        const next = {...state};

        // A listed space always gets an entry, so its presence means "this
        // space's pages are loaded" even when the space has none.
        if (spaceId !== undefined && !(spaceId in next)) {
            next[spaceId] = new Set();
        }
        for (const page of received) {
            const set = new Set(next[page.space_id]);
            set.add(page.id);
            next[page.space_id] = set;
        }
        return next;
    }
    case PageTypes.DELETED_PAGE: {
        const {spaceId, pageIds} = action as unknown as DeletedPageAction;
        const set = state[spaceId];
        if (!set || !pageIds.some((id) => set.has(id))) {
            return state;
        }
        const nextSet = new Set(set);
        pageIds.forEach((id) => nextSet.delete(id));
        return {...state, [spaceId]: nextSet};
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

// Normalized server entities, kept separate from view/UI state so future
// top-level reducers (e.g. `views`) can sit beside this one.
const entities = combineReducers({spaces, spacesInTeam, pages, pagesInSpace, spaceMembers});

export default entities;
