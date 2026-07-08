// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {recentPageIds, recentSpaceIds, recentSpaceSummaries} from 'data/fixtures';
import manifest from 'manifest';

import type {GlobalState} from '@mattermost/types/store';

import {createSelector} from 'mattermost-redux/selectors/create_selector';

import type {Page, Space, SpaceSummary} from 'types/docs';

import type {DocsPluginState} from './types';

const EMPTY_PLUGIN_STATE: DocsPluginState = {
    spaces: {byId: {}, order: []},
    pages: {byId: {}, bySpace: {}},
};

// Assert known typing, mirroring the Playbooks pluginState selector — the host
// store types plugin subtrees as `unknown`. Falls back to an empty slice so a
// transient missing subtree (e.g. dev hot-reload before registerReducer) can't
// crash consumers.
const pluginState = (state: GlobalState): DocsPluginState =>
    (state as unknown as Record<string, DocsPluginState>)['plugins-' + manifest.id] ?? EMPTY_PLUGIN_STATE;

const compact = <T>(items: Array<T | undefined>): T[] => items.filter((item): item is T => Boolean(item));

export const getSpacesById = (state: GlobalState): Record<string, Space> => pluginState(state).spaces.byId;

export const getSpacesOrder = (state: GlobalState): string[] => pluginState(state).spaces.order;

export const getSpaces = createSelector(
    'getSpaces',
    getSpacesById,
    getSpacesOrder,
    (byId, order) => compact(order.map((id) => byId[id])),
);

export const getSpace = (state: GlobalState, id: string): Space | undefined => getSpacesById(state)[id];

// A slug doubles as a space id in the mock (see data/mock_data_source.ts), so
// checking the store's id map is equivalent to checking slug uniqueness.
export const isSlugAvailable = (state: GlobalState, slug: string): boolean => !getSpacesById(state)[slug];

// "Recent" bookkeeping is design-time fixture data until a real recently-viewed
// concept (and API) exists; the ids resolve against the reactive store so the
// result still reflects any local creates/deletes.
export const getRecentSpaces = createSelector(
    'getRecentSpaces',
    getSpacesById,
    (byId) => compact(recentSpaceIds.map((id) => byId[id])),
);

export const getRecentSpaceSummaries = createSelector(
    'getRecentSpaceSummaries',
    getSpacesById,
    (byId): SpaceSummary[] => recentSpaceSummaries.flatMap(({spaceId, pageCount, viewedLabel}) => {
        const space = byId[spaceId];
        return space ? [{space, pageCount, viewedLabel}] : [];
    }),
);

export const getPagesById = (state: GlobalState): Record<string, Page> => pluginState(state).pages.byId;

const getPageIdsForSpace = (state: GlobalState, spaceId: string): string[] => pluginState(state).pages.bySpace[spaceId] ?? [];

export const getPagesForSpace = createSelector(
    'getPagesForSpace',
    getPagesById,
    getPageIdsForSpace,
    (byId, ids) => compact(ids.map((id) => byId[id])),
);

export const getPage = (state: GlobalState, id: string): Page | undefined => getPagesById(state)[id];

export const getRecentPages = createSelector(
    'getRecentPages',
    getPagesById,
    (byId) => compact(recentPageIds.map((id) => byId[id])),
);

export type DocsSearchResults = {
    spaces: Space[];
    pages: Page[];
};

// Replaces the mock's search: filters the reactive store instead of a static
// fixture array.
export function searchDocs(state: GlobalState, query: string): DocsSearchResults {
    const q = query.trim().toLowerCase();
    if (!q) {
        return {spaces: [], pages: []};
    }
    return {
        spaces: getSpaces(state).filter((space) => space.title.toLowerCase().includes(q)),
        pages: Object.values(getPagesById(state)).filter((page) => page.title.toLowerCase().includes(q)),
    };
}
