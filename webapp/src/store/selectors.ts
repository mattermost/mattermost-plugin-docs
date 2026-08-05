// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';
import {createSelector} from 'reselect';

import type {GlobalState} from '@mattermost/types/store';

import {getCurrentTeamId} from 'mattermost-redux/selectors/entities/teams';

import type {Page, Space} from 'types/docs';
import type {Draft} from 'types/drafts';

import {collectSubtreeIds} from './entities';
import type {DocsEntitiesState, DocsPluginState} from './types';

const EMPTY_PLUGIN_STATE: DocsPluginState = {
    entities: {
        spaces: {},
        spacesInTeam: {},
        pages: {},
        pagesInSpace: {},
        spaceMembers: {},
        drafts: {},
        draftsInSpace: {},
    },
};

const EMPTY_SPACES: Space[] = [];
const EMPTY_PAGES: Page[] = [];
const EMPTY_DRAFTS: Draft[] = [];

// Assert known typing, mirroring the Playbooks pluginState selector — the host
// store types plugin subtrees as `unknown`. Falls back to an empty slice so a
// transient missing subtree (e.g. dev hot-reload before registerReducer) can't
// crash consumers.
const pluginState = (state: GlobalState): DocsPluginState =>
    (state as unknown as Record<string, DocsPluginState>)['plugins-' + manifest.id] ?? EMPTY_PLUGIN_STATE;

const entities = (state: GlobalState): DocsEntitiesState => pluginState(state).entities;

const compact = <T>(items: Array<T | undefined>): T[] => items.filter((item): item is T => Boolean(item));

// Derived display order (spaces/pages carry no stored order): sort by the
// server `sort_order`, then title as a stable tiebreak.
const bySortOrder = <T extends {sort_order: number; title: string}>(a: T, b: T): number =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title);

const resolveSpaces = (ids: Set<string> | undefined, byId: Record<string, Space>): Space[] => {
    if (!ids || ids.size === 0) {
        return EMPTY_SPACES;
    }
    return compact([...ids].map((id) => byId[id])).sort(bySortOrder);
};

const resolvePages = (ids: Set<string> | undefined, byId: Record<string, Page>): Page[] => {
    if (!ids || ids.size === 0) {
        return EMPTY_PAGES;
    }
    return compact([...ids].map((id) => byId[id])).sort(bySortOrder);
};

// Drafts have no sort_order to order by, so recency stands in — matching the
// server's UpdateAt DESC collection order.
const resolveDrafts = (ids: Set<string> | undefined, byId: Record<string, Draft>): Draft[] => {
    if (!ids || ids.size === 0) {
        return EMPTY_DRAFTS;
    }
    return compact([...ids].map((id) => byId[id])).sort((a, b) => b.update_at - a.update_at);
};

export const getSpacesById = (state: GlobalState): Record<string, Space> => entities(state).spaces;

export const getSpacesInTeamIndex = (state: GlobalState): Record<string, Set<string>> => entities(state).spacesInTeam;

export const getPagesById = (state: GlobalState): Record<string, Page> => entities(state).pages;

export const getPagesInSpaceIndex = (state: GlobalState): Record<string, Set<string>> => entities(state).pagesInSpace;

const EMPTY_MEMBER_IDS: string[] = [];

export const getSpaceMemberIds = (state: GlobalState, spaceId: string): string[] =>
    entities(state).spaceMembers[spaceId] ?? EMPTY_MEMBER_IDS;

// Spaces for an explicit team, resolved through the byId map and sorted.
// Mirrors core's getChannelsInTeam-style read: index Set → entities → order.
export const getSpacesInTeam = createSelector(
    [getSpacesById, getSpacesInTeamIndex, (_state: GlobalState, teamId: string) => teamId],
    (byId, index, teamId) => resolveSpaces(index[teamId], byId),
);

export const getSpacesForCurrentTeam = createSelector(
    [getSpacesById, getSpacesInTeamIndex, getCurrentTeamId],
    (byId, index, teamId) => resolveSpaces(index[teamId], byId),
);

// Whether the current team's space list has been loaded (see fetchSpaces, which
// seeds the index entry even for a team with no spaces). Lets consumers tell an
// unknown space id from one that simply hasn't arrived yet.
export const areSpacesLoadedForCurrentTeam = (state: GlobalState): boolean =>
    getCurrentTeamId(state) in getSpacesInTeamIndex(state);

// All spaces the store holds, across every team the user has loaded. Backs the
// cross-team docs switcher (which finds docs regardless of the current team).
export const getAllSpaces = createSelector(
    [getSpacesById],
    (byId) => Object.values(byId).sort(bySortOrder),
);

const getAllPages = createSelector(
    [getPagesById],
    (byId) => Object.values(byId),
);

export const getSpace = (state: GlobalState, id: string): Space | undefined => getSpacesById(state)[id];

export const getPagesForSpace = createSelector(
    [getPagesById, getPagesInSpaceIndex, (_state: GlobalState, spaceId: string) => spaceId],
    (byId, index, spaceId) => resolvePages(index[spaceId], byId),
);

export const getPage = (state: GlobalState, id: string): Page | undefined => getPagesById(state)[id];

// A page only if it belongs to `spaceId`. Page ids are globally unique, so a
// URL can name a real page that lives in another space (or one that has since
// moved); resolving by id alone would render it inside the wrong space.
export const getPageInSpace = (state: GlobalState, spaceId: string, id: string): Page | undefined => {
    const page = getPagesById(state)[id];
    return page?.space_id === spaceId ? page : undefined;
};

// Whether `id` is `rootId` or sits beneath it. Deleting a page deletes its
// subpages too, so a viewer on any page in the subtree — not just the one being
// deleted — is about to lose what they're looking at.
export const isPageInSubtree = (state: GlobalState, rootId: string, id: string): boolean =>
    collectSubtreeIds(getPagesById(state), rootId).has(id);

// Whether a space's member list has been loaded. A space always has at least its
// creator, so a count of 0 means "not loaded (or the request failed)" rather than
// "no members" — consumers need to tell those apart before rendering a number.
export const areMembersLoadedForSpace = (state: GlobalState, spaceId: string): boolean =>
    spaceId in entities(state).spaceMembers;

// Whether a space's page list has been loaded (fetchPages seeds the index entry
// even for a space with no pages). Lets consumers tell a page id that is simply
// still in flight from one that doesn't belong here.
export const arePagesLoadedForSpace = (state: GlobalState, spaceId: string): boolean =>
    spaceId in getPagesInSpaceIndex(state);

export const getDraftsById = (state: GlobalState): Record<string, Draft> => entities(state).drafts;

const getDraftsInSpaceIndex = (state: GlobalState): Record<string, Set<string>> => entities(state).draftsInSpace;

// The caller's unpublished work on a page, if any. Keyed by page id, so this is
// also how an orphan draft is reached — its page id is reserved, not published.
export const getDraftForPage = (state: GlobalState, pageId: string): Draft | undefined =>
    getDraftsById(state)[pageId];

// Whether the caller has unpublished edits to an existing page. Distinct from
// having a draft at all: an orphan draft is an unpublished *page*, not an edit.
export const hasUnpublishedEdits = (state: GlobalState, pageId: string): boolean =>
    pageId in getDraftsById(state) && pageId in getPagesById(state);

// A space's drafts, newest first — the order the server's drafts collection uses,
// and the only order available since drafts carry no sort_order.
export const getDraftsForSpace = createSelector(
    [getDraftsById, getDraftsInSpaceIndex, (_state: GlobalState, spaceId: string) => spaceId],
    (byId, index, spaceId) => resolveDrafts(index[spaceId], byId),
);

/**
 * A space's drafts that have no published page — unpublished new pages, which the
 * tree renders as rows of their own.
 *
 * The complement (a draft whose page exists) must NOT get a row: its page is
 * already in the tree, and adding one would render the same page twice. Encoded
 * here once rather than at each call site, because that duplicate is the easy
 * mistake to make.
 */
export const getOrphanDraftsForSpace = createSelector(
    [getDraftsForSpace, getPagesById],
    (spaceDrafts, pagesById) => {
        const orphans = spaceDrafts.filter((draft) => !(draft.page_id in pagesById));
        return orphans.length === 0 ? EMPTY_DRAFTS : orphans;
    },
);

// Whether a space's drafts have been fetched (fetchDrafts seeds the index entry
// even for a space with none), so "no drafts" is distinguishable from "not asked".
export const areDraftsLoadedForSpace = (state: GlobalState, spaceId: string): boolean =>
    spaceId in getDraftsInSpaceIndex(state);

export type DocsSearchResults = {
    spaces: Space[];
    pages: Page[];
};

const EMPTY_RESULTS: DocsSearchResults = {spaces: EMPTY_SPACES, pages: EMPTY_PAGES};

// Cross-team: the docs switcher finds spaces/pages regardless of the current
// team. Memoized so an unchanged (spaces, pages, query) triple reuses the
// previous result.
export const searchDocs = createSelector(
    [getAllSpaces, getAllPages, (_state: GlobalState, query: string) => query],
    (spaces, pages, query): DocsSearchResults => {
        const q = query.trim().toLowerCase();
        if (!q) {
            return EMPTY_RESULTS;
        }
        return {
            spaces: spaces.filter((space) => space.title.toLowerCase().includes(q)),
            pages: pages.filter((page) => page.title.toLowerCase().includes(q)),
        };
    },
);
