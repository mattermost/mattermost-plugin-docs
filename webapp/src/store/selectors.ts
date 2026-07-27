// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';
import {createSelector} from 'reselect';

import type {GlobalState} from '@mattermost/types/store';

import {getCurrentTeamId} from 'mattermost-redux/selectors/entities/teams';

import type {Page, Space} from 'types/docs';

import type {DocsPluginState} from './types';

const EMPTY_PLUGIN_STATE: DocsPluginState = {
    spaces: {},
    spacesInTeam: {},
    pages: {},
    pagesInSpace: {},
};

const EMPTY_SPACES: Space[] = [];
const EMPTY_PAGES: Page[] = [];

// Assert known typing, mirroring the Playbooks pluginState selector — the host
// store types plugin subtrees as `unknown`. Falls back to an empty slice so a
// transient missing subtree (e.g. dev hot-reload before registerReducer) can't
// crash consumers.
const pluginState = (state: GlobalState): DocsPluginState =>
    (state as unknown as Record<string, DocsPluginState>)['plugins-' + manifest.id] ?? EMPTY_PLUGIN_STATE;

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

export const getSpacesById = (state: GlobalState): Record<string, Space> => pluginState(state).spaces;

export const getSpacesInTeamIndex = (state: GlobalState): Record<string, Set<string>> => pluginState(state).spacesInTeam;

export const getPagesById = (state: GlobalState): Record<string, Page> => pluginState(state).pages;

export const getPagesInSpaceIndex = (state: GlobalState): Record<string, Set<string>> => pluginState(state).pagesInSpace;

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
