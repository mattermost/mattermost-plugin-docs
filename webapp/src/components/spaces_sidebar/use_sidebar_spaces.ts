// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {favoriteKey} from 'data/favorites';
import type {SidebarOrder} from 'data/favorites';
import {useSidebarOrder, useToggleFavorite} from 'hooks/favorites';
import {useAppSelector} from 'hooks/redux';
import {useSpaces} from 'hooks/spaces';
import {useTeamContext} from 'hooks/team';
import {useCallback, useMemo, useState} from 'react';

import {getDocsFavoriteSpaceIds} from 'store/favorites';

import type {Space} from 'types/docs';

import type {DndCategory} from './dnd/types';
import {useSpacesDnd} from './dnd/use_spaces_dnd';

// Both stored lists hold prefixed favorite keys ("space:<id>"), never bare ids:
// moveBetween writes a prefixed key into whichever list receives the move, so a
// list read back as bare ids would silently lose every reordered entry.
const SPACE_KEY_PREFIX = favoriteKey({type: 'space', id: ''});

// Applies a move of `key` into `to` at `index`, dropping it from both lists first
// so a cross-category move can't leave a duplicate behind.
const moveBetween = (order: SidebarOrder, key: string, to: DndCategory, index: number): SidebarOrder => {
    const favorites = order.favorites.filter((entry) => entry !== key);
    const spaces = order.spaces.filter((entry) => entry !== key);
    const target = to === 'favorites' ? favorites : spaces;
    target.splice(Math.max(0, Math.min(index, target.length)), 0, key);
    return {favorites, spaces};
};

export type SidebarSpacesModel = {
    spacesById: Map<string, Space>;

    /**
     * Spaces in the favorites category, in display order: favorited outright, or
     * holding a favorited page. Favorited pages themselves are surfaced in the
     * page tree rather than here.
     */
    favoriteSpaceIds: string[];
    spacesOrder: string[];
    favoritesCollapsed: boolean;
    toggleFavoritesCollapsed: () => void;
    toggleFavorite: (id: string) => void;
};

/**
 * Composes the sidebar's display model from the space list, the user's favorites,
 * and their manual ordering (both persisted as user preferences).
 *
 * Order is derived, not trusted: the stored list is a hint, filtered to what's
 * live and then extended with anything it hasn't seen. So a stale entry can't
 * hide a space, and a newly created one still appears.
 */
export function useSidebarSpaces(dndEnabled: boolean): SidebarSpacesModel {
    const spaces = useSpaces();
    const {id: teamId} = useTeamContext();
    const toggleFavoritePreference = useToggleFavorite();
    const {order, setOrder} = useSidebarOrder(teamId);
    const [favoritesCollapsed, setFavoritesCollapsed] = useState(false);

    const spacesById = useMemo(() => new Map(spaces.map((s) => [s.id, s])), [spaces]);

    const allFavoriteSpaceIds = useAppSelector(getDocsFavoriteSpaceIds);

    // Sorts ids by their position in the stored order, keeping anything the
    // stored order hasn't seen at the end (in its natural order).
    const applyStoredOrder = useCallback((ids: string[], storedKeys: string[], prefix: string) => {
        const position = new Map<string, number>();
        storedKeys.forEach((key, index) => {
            if (key.startsWith(prefix)) {
                position.set(key.slice(prefix.length), index);
            }
        });
        return [...ids].sort((a, b) => (position.get(a) ?? Number.MAX_SAFE_INTEGER) - (position.get(b) ?? Number.MAX_SAFE_INTEGER));
    }, []);

    // Favorites are a user preference, so the stored list spans every team (and
    // can name a space that's been left or deleted). Scoping it to the team's
    // loaded spaces here is what makes the counts downstream honest — an
    // out-of-team favorite has no row to render, so counting it would leave the
    // favorites category looking populated while showing nothing.
    const favoriteSpaceIds = useMemo(
        () => applyStoredOrder(allFavoriteSpaceIds.filter((id) => spacesById.has(id)), order.favorites, SPACE_KEY_PREFIX),
        [allFavoriteSpaceIds, spacesById, order.favorites, applyStoredOrder],
    );

    // Spaces that aren't favorited, in stored order, then the rest.
    const spacesOrder = useMemo(() => {
        const shownInFavorites = new Set(favoriteSpaceIds);
        const eligible = spaces.map((s) => s.id).filter((id) => !shownInFavorites.has(id));
        return applyStoredOrder(eligible, order.spaces, SPACE_KEY_PREFIX);
    }, [spaces, favoriteSpaceIds, order.spaces, applyStoredOrder]);

    // The drag layer works in plain ids (only spaces are draggable), so ids are
    // mapped back to their stored keys here. A cross-category move also flips
    // favorite membership — in core, favoriting *is* a move between categories.
    const applyMove = useCallback((id: string, from: DndCategory, to: DndCategory, index: number) => {
        const toKeys = (ids: string[]) => ids.map((spaceId) => favoriteKey({type: 'space', id: spaceId}));
        const stored: SidebarOrder = {favorites: toKeys(favoriteSpaceIds), spaces: toKeys(spacesOrder)};
        setOrder(moveBetween(stored, favoriteKey({type: 'space', id}), to, index));

        if (from !== to) {
            toggleFavoritePreference('space', id);
        }
    }, [setOrder, favoriteSpaceIds, spacesOrder, toggleFavoritePreference]);

    const toggleFavorite = useCallback((id: string) => toggleFavoritePreference('space', id), [toggleFavoritePreference]);

    // Projected to plain ids so the monitor's indexOf matches the drag payload;
    // indices still line up with the key list, which is the same list projected.
    useSpacesDnd({
        favoriteOrder: favoriteSpaceIds,
        spacesOrder,
        onReorder: applyMove,
        enabled: dndEnabled,
    });

    return {
        spacesById,
        favoriteSpaceIds,
        spacesOrder,
        favoritesCollapsed,
        toggleFavoritesCollapsed: () => setFavoritesCollapsed((v) => !v),
        toggleFavorite,
    };
}
