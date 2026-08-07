// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    EMPTY_SIDEBAR_ORDER,
    FAVORITES_CATEGORY,
    SIDEBAR_ORDER_CATEGORY,
    parseSidebarOrder,
    serializeSidebarOrder,
} from 'data/favorites';
import type {FavoriteRef, FavoriteType, SidebarOrder} from 'data/favorites';
import {useAppDispatch, useAppSelector, useAppStore} from 'hooks/redux';
import {useCallback, useMemo} from 'react';

import {deletePreferences, savePreferences} from 'mattermost-redux/actions/preferences';
import {get as getPreference} from 'mattermost-redux/selectors/entities/preferences';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {getDocsFavoriteIds, getDocsFavorites, getSpaceFavoriteState} from 'store/favorites';
import type {SpaceFavoriteState} from 'store/favorites';

/**
 * Whether one item is favorited. Selects a boolean, so a component using this
 * re-renders only when its own item is toggled — not when any favorite changes.
 * Prefer this over `useFavorites` anywhere that renders per row.
 */
export function useIsFavorite(type: FavoriteType, id: string): boolean {
    return useAppSelector((state) => getDocsFavoriteIds(state).has(id));
}

/**
 * A space's tri-state favorite status (see getSpaceFavoriteState). Selects a
 * string, so the caller re-renders only when that space's status changes.
 */
export function useSpaceFavoriteState(spaceId: string): SpaceFavoriteState {
    return useAppSelector((state) => getSpaceFavoriteState(state, spaceId));
}

/**
 * A stable toggle callback. Reads current membership from the store at call time
 * rather than subscribing, so holding this never re-renders the caller.
 */
export function useToggleFavorite(): (type: FavoriteType, id: string) => void {
    const dispatch = useAppDispatch();
    const store = useAppStore();
    const userId = useAppSelector(getCurrentUserId);

    return useCallback((type: FavoriteType, id: string) => {
        const preference = {user_id: userId, category: FAVORITES_CATEGORY, name: id, value: type};
        if (getDocsFavoriteIds(store.getState()).has(id)) {
            dispatch(deletePreferences(userId, [preference]));
        } else {
            dispatch(savePreferences(userId, [preference]));
        }
    }, [dispatch, store, userId]);
}

type FavoritesApi = {
    favorites: FavoriteRef[];
    isFavorite: (type: FavoriteType, id: string) => boolean;
    toggleFavorite: (type: FavoriteType, id: string) => void;
};

/**
 * The full favorites list. Only for consumers that need to enumerate favorites
 * (the sidebar model); per-item callers should use `useIsFavorite` +
 * `useToggleFavorite` to avoid subscribing to the whole list.
 */
export function useFavorites(): FavoritesApi {
    const favorites = useAppSelector(getDocsFavorites);
    const favoriteIds = useAppSelector(getDocsFavoriteIds);
    const toggleFavorite = useToggleFavorite();

    const isFavorite = useCallback((type: FavoriteType, id: string) => favoriteIds.has(id), [favoriteIds]);

    return {favorites, isFavorite, toggleFavorite};
}

type SidebarOrderApi = {
    order: SidebarOrder;
    setOrder: (order: SidebarOrder) => void;
};

/**
 * The user's manual sidebar ordering for a team, persisted as one preference per
 * team. Mirrors the per-category ordered id lists core keeps on its sidebar
 * categories.
 */
export function useSidebarOrder(teamId: string): SidebarOrderApi {
    const dispatch = useAppDispatch();
    const userId = useAppSelector(getCurrentUserId);
    const stored = useAppSelector((state) => getPreference(state, SIDEBAR_ORDER_CATEGORY, teamId, ''));

    const order = useMemo(() => (teamId ? parseSidebarOrder(stored) : EMPTY_SIDEBAR_ORDER), [teamId, stored]);

    const setOrder = useCallback((next: SidebarOrder) => {
        if (!teamId) {
            return;
        }
        dispatch(savePreferences(userId, [{
            user_id: userId,
            category: SIDEBAR_ORDER_CATEGORY,
            name: teamId,
            value: serializeSidebarOrder(next),
        }]));
    }, [dispatch, userId, teamId]);

    return {order, setOrder};
}
