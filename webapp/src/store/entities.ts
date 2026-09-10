// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers} from 'redux';
import type {UnknownAction} from 'redux';

import type {Page, Space} from 'types/docs';
import {isFullDraft} from 'types/drafts';
import type {Draft, StoredDraft} from 'types/drafts';

import {DraftTypes, PageTypes, SpaceTypes} from './action_types';

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
type SpaceMemberAction = {spaceId: string; userId: string};

// `spaceId` marks a full list for that space, seeding its index entry even when the
// space has no drafts — the same "loaded" signal RECEIVED_PAGES uses.
type ReceivedDraftsAction = {drafts: StoredDraft[]; spaceId?: string};
type ReceivedDraftAction = {draft: Draft};
type DeletedDraftAction = {spaceId: string; pageId: string};

// Publishing is one action so no render sees both the draft and its page.
type PublishedDraftAction = {spaceId: string; pageId: string; page: Page};

const bySortOrder = (a: Page, b: Page): number =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title);

// Moves a page under `newParentId` at `siblingIndex` and renumbers the destination
// sibling group's sort_order, mirroring the server's reindex. Returns a new byId
// map (untouched pages are shared by reference). Only pages in `spaceId` are
// considered.
//
// Two details exist to match the server exactly, because the move's response
// upserts only the moved page: whatever this function writes has to survive that
// merge sitting next to it. Diverge and the merged page ties with a sibling, where
// the title tiebreak silently drops it a slot.
//
//  - Numbering is 1-based, as the server's is: store.reindexSiblingGroup writes
//    i+1 and nextSortOrder appends MAX+1.
//  - The page's old group is left alone rather than closed up, because
//    store.MovePage reindexes the destination group only. The gap the page leaves
//    behind is what the server has, and order doesn't depend on contiguity.
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
    const next = {...byId};

    const newGroup = Object.values(byId).
        filter((page) => page.space_id === spaceId && page.parent_id === newParentId && page.id !== pageId).
        sort(bySortOrder);

    const index = Math.max(0, Math.min(siblingIndex, newGroup.length));
    newGroup.splice(index, 0, moved);
    newGroup.forEach((page, i) => {
        next[page.id] = {...page, parent_id: newParentId, sort_order: i + 1};
    });

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
    if (!(space.team_id in state)) {
        return state;
    }
    const next = new Set(state[space.team_id]);
    next.add(space.id);
    return {...state, [space.team_id]: next};
}

function spacesInTeam(state: Record<string, Set<string>> = {}, action: UnknownAction): Record<string, Set<string>> {
    switch (action.type) {
    case SpaceTypes.RECEIVED_SPACES: {
        const {spaces: received, teamId} = action as unknown as ReceivedSpacesAction;
        if (teamId !== undefined) {
            return {
                ...state,
                [teamId]: new Set(received.filter((space) => space.team_id === teamId).map((space) => space.id)),
            };
        }
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
        const {pages: received, spaceId} = action as unknown as ReceivedPagesAction;
        if (received.length === 0 && spaceId === undefined) {
            return state;
        }
        const next = spaceId === undefined ? {...state} : Object.fromEntries(
            Object.entries(state).filter(([, page]) => page.space_id !== spaceId),
        );
        for (const page of received) {
            next[page.id] = page;
        }
        return next;
    }

    // The other half of PUBLISHED_DRAFT: the page the draft became.
    case DraftTypes.PUBLISHED_DRAFT: {
        const {page} = action as unknown as PublishedDraftAction;
        return {...state, [page.id]: page};
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
        if (spaceId !== undefined) {
            return {
                ...state,
                [spaceId]: new Set(received.filter((page) => page.space_id === spaceId).map((page) => page.id)),
            };
        }
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
    case DraftTypes.PUBLISHED_DRAFT: {
        const {page} = action as unknown as PublishedDraftAction;
        const current = state[page.space_id];
        if (!current || current.has(page.id)) {
            return state;
        }
        const set = new Set(current);
        set.add(page.id);
        return {...state, [page.space_id]: set};
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
    case SpaceTypes.ADDED_SPACE_MEMBER: {
        const {spaceId, userId} = action as unknown as SpaceMemberAction;
        const current = state[spaceId];

        // An absent entry means the roster was never loaded. Seeding it from a single
        // id would make areMembersLoadedForSpace claim a full list; fetchSpaceMembers
        // is what populates it.
        if (!current || current.includes(userId)) {
            return state;
        }
        return {...state, [spaceId]: [...current, userId]};
    }
    case SpaceTypes.REMOVED_SPACE_MEMBER: {
        const {spaceId, userId} = action as unknown as SpaceMemberAction;
        const current = state[spaceId];
        if (!current?.includes(userId)) {
            return state;
        }
        return {...state, [spaceId]: current.filter((id) => id !== userId)};
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

/**
 * The caller's drafts, keyed by **page id**.
 *
 * Deliberately not merged into `pages`. The server keys a draft by
 * (user_id, page_id), and every draft a client can read is its own user's — so page
 * id alone is a sufficient key here, but the maps must stay separate or one user's
 * unpublished title would render in another's tree.
 */
function drafts(state: Record<string, StoredDraft> = {}, action: UnknownAction): Record<string, StoredDraft> {
    switch (action.type) {
    case DraftTypes.RECEIVED_DRAFTS: {
        const {drafts: received} = action as unknown as ReceivedDraftsAction;
        if (received.length === 0) {
            return state;
        }
        const next = {...state};
        for (const draft of received) {
            const current = next[draft.page_id];
            next[draft.page_id] = current && isFullDraft(current) && !isFullDraft(draft) ? {...current, ...draft} : draft;
        }
        return next;
    }
    case DraftTypes.RECEIVED_DRAFT: {
        const {draft} = action as unknown as ReceivedDraftAction;
        return {...state, [draft.page_id]: draft};
    }
    case DraftTypes.DELETED_DRAFT:
    case DraftTypes.PUBLISHED_DRAFT: {
        const {pageId} = action as unknown as DeletedDraftAction;
        if (!(pageId in state)) {
            return state;
        }
        const next = {...state};
        delete next[pageId];
        return next;
    }

    // A deleted page takes its subtree's drafts with it: the pages are gone, so
    // unpublished edits to them can never be published.
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
        const remaining = Object.entries(state).filter(([, draft]) => draft.space_id !== spaceId);
        return remaining.length === Object.keys(state).length ? state : Object.fromEntries(remaining);
    }
    default:
        return state;
    }
}

// Page ids of the caller's drafts, keyed by space id. Mirrors pagesInSpace so
// "this space has no drafts" can be told from "not fetched yet".
function draftsInSpace(state: Record<string, Set<string>> = {}, action: UnknownAction): Record<string, Set<string>> {
    const withDraft = (current: Record<string, Set<string>>, draft: StoredDraft) => {
        const set = new Set(current[draft.space_id]);
        set.add(draft.page_id);
        return {...current, [draft.space_id]: set};
    };

    switch (action.type) {
    case DraftTypes.RECEIVED_DRAFTS: {
        const {drafts: received, spaceId} = action as unknown as ReceivedDraftsAction;
        if (spaceId !== undefined) {
            return {
                ...state,
                [spaceId]: new Set(received.filter((draft) => draft.space_id === spaceId).map((draft) => draft.page_id)),
            };
        }
        if (received.length === 0) {
            return state;
        }
        let next = {...state};
        for (const draft of received) {
            next = withDraft(next, draft);
        }
        return next;
    }
    case DraftTypes.RECEIVED_DRAFT: {
        const {draft} = action as unknown as ReceivedDraftAction;
        return state[draft.space_id]?.has(draft.page_id) ? state : withDraft(state, draft);
    }
    case DraftTypes.DELETED_DRAFT:
    case DraftTypes.PUBLISHED_DRAFT: {
        const {spaceId, pageId} = action as unknown as DeletedDraftAction;
        const set = state[spaceId];
        if (!set?.has(pageId)) {
            return state;
        }
        const nextSet = new Set(set);
        nextSet.delete(pageId);
        return {...state, [spaceId]: nextSet};
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

// Normalized server entities, kept separate from view/UI state so future
// top-level reducers (e.g. `views`) can sit beside this one.
const entities = combineReducers({spaces, spacesInTeam, pages, pagesInSpace, spaceMembers, drafts, draftsInSpace});

export default entities;
