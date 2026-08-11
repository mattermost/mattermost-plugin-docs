// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {FAVORITES_CATEGORY, parseFavorite} from 'data/favorites';
import type {FavoriteRef, FavoriteType} from 'data/favorites';
import {createSelector} from 'reselect';

import type {GlobalState} from '@mattermost/types/store';

import {makeGetCategory} from 'mattermost-redux/selectors/entities/preferences';

import {getPagesById} from 'store/selectors';

// Favorites live in the host's preferences state, so these read from there rather
// than the plugin subtree. Every derivation below is a module-level memoized
// selector: the work is done once per preferences change and shared by every
// consumer, instead of once per component (the sidebar renders a menu per row,
// so per-component derivation is O(rows x favorites) on each change).
//
// makeGetCategory scans all of the user's preferences, which is why it must be
// instantiated once here and not per hook call.
const getFavoritePreferences = makeGetCategory('docsFavorites', FAVORITES_CATEGORY);

/** The user's favorites, newest-first order not guaranteed (see useSidebarSpaces). */
export const getDocsFavorites = createSelector(
    [getFavoritePreferences],
    (preferences): FavoriteRef[] => preferences.
        map(({name, value}) => parseFavorite(name, value)).
        filter((ref): ref is FavoriteRef => ref !== null),
);

/** O(1) membership tests, for the many components that only need a boolean. */
export const getDocsFavoriteIds = createSelector(
    [getDocsFavorites],
    (favorites): Set<string> => new Set(favorites.map((ref) => ref.id)),
);

/** O(1) type lookup, so a drag doesn't linear-scan the favorites list. */
export const getDocsFavoriteTypes = createSelector(
    [getDocsFavorites],
    (favorites): Map<string, FavoriteType> => new Map(favorites.map((ref) => [ref.id, ref.type])),
);

export const isDocsFavorite = (state: GlobalState, id: string): boolean => getDocsFavoriteIds(state).has(id);

/**
 * Favorited page ids grouped by the space that owns them, so the sidebar can nest
 * them under their space. A page whose space hasn't loaded yet is omitted — its
 * owner is unknown until then, and there'd be no label to render anyway.
 */
export const getDocsFavoritePagesBySpace = createSelector(
    [getDocsFavorites, getPagesById],
    (favorites, pagesById): Map<string, string[]> => {
        const bySpace = new Map<string, string[]>();
        for (const ref of favorites) {
            if (ref.type !== 'page') {
                continue;
            }
            const page = pagesById[ref.id];
            if (!page) {
                continue;
            }
            const pageIds = bySpace.get(page.space_id);
            if (pageIds) {
                pageIds.push(ref.id);
            } else {
                bySpace.set(page.space_id, [ref.id]);
            }
        }
        return bySpace;
    },
);

/**
 * A space's favorite state, in nested-checkbox terms: `on` when the space itself
 * is favorited, `partial` when it isn't but one of its pages is, else `off`.
 */
export type SpaceFavoriteState = 'on' | 'partial' | 'off';

export function getSpaceFavoriteState(state: GlobalState, spaceId: string): SpaceFavoriteState {
    if (getDocsFavoriteIds(state).has(spaceId)) {
        return 'on';
    }
    return getDocsFavoritePagesBySpace(state).has(spaceId) ? 'partial' : 'off';
}

/**
 * Spaces eligible for the favorites category: explicitly favorited, or holding a
 * favorited page.
 *
 * Team-agnostic, like the preferences it derives from — it can name spaces in
 * other teams, or ones the user has left. Callers rendering a team's sidebar must
 * intersect this with that team's loaded spaces before counting it (see
 * useSidebarSpaces), or an out-of-team favorite inflates the count with no row.
 */
export const getDocsFavoriteSpaceIds = createSelector(
    [getDocsFavorites, getDocsFavoritePagesBySpace],
    (favorites, pagesBySpace): string[] => {
        const ids = favorites.filter((ref) => ref.type === 'space').map((ref) => ref.id);
        const seen = new Set(ids);
        for (const spaceId of pagesBySpace.keys()) {
            if (!seen.has(spaceId)) {
                seen.add(spaceId);
                ids.push(spaceId);
            }
        }
        return ids;
    },
);
