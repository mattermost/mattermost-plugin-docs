// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaces} from 'hooks/spaces';
import {useEffect, useMemo, useState} from 'react';

import type {Space} from 'types/docs';

import type {DndCategory} from './dnd/types';
import {useSpacesDnd} from './dnd/use_spaces_dnd';

const moveBetween = (
    favoriteOrder: string[],
    spacesOrder: string[],
    spaceId: string,
    to: DndCategory,
    index: number,
) => {
    const fav = favoriteOrder.filter((id) => id !== spaceId);
    const spaces = spacesOrder.filter((id) => id !== spaceId);
    const target = to === 'favorites' ? fav : spaces;
    target.splice(Math.max(0, Math.min(index, target.length)), 0, spaceId);
    return {favoriteOrder: fav, spacesOrder: spaces};
};

// Prunes ids no longer live and returns the same array reference when nothing
// changed, so callers can bail out of a state update without a re-render.
const pruneOrder = (order: string[], liveIds: Set<string>): string[] => {
    const pruned = order.filter((id) => liveIds.has(id));
    return pruned.length === order.length ? order : pruned;
};

export type SidebarSpacesModel = {
    spacesById: Map<string, Space>;
    favoriteOrder: string[];
    spacesOrder: string[];
    favoritesCollapsed: boolean;
    toggleFavoritesCollapsed: () => void;
    toggleFavorite: (id: string) => void;
};

// Holds ordering/favorites in local state today; these move to user
// preferences in a later phase. `dndEnabled` gates the drag-to-
// reorder monitor until that persistence exists (see SpacesSidebar).
export function useSidebarSpaces(dndEnabled: boolean): SidebarSpacesModel {
    const spaces = useSpaces();
    const [favoriteOrder, setFavoriteOrder] = useState<string[]>([]);
    const [spacesOrder, setSpacesOrder] = useState<string[]>(() => spaces.map((s) => s.id));
    const [favoritesCollapsed, setFavoritesCollapsed] = useState(false);

    const spacesById = useMemo(() => new Map(spaces.map((s) => [s.id, s])), [spaces]);

    // The space set is now reactive (creates/deletes flow through the store),
    // so reconcile the locally-owned order lists against it: prune ids that no
    // longer exist, and append ids we haven't seen yet. Guarded so a no-op
    // update returns the same reference and doesn't retrigger the effect.
    useEffect(() => {
        const liveIds = new Set(spaces.map((s) => s.id));

        setFavoriteOrder((prev) => pruneOrder(prev, liveIds));

        setSpacesOrder((prev) => {
            const pruned = pruneOrder(prev, liveIds);
            const tracked = new Set([...pruned, ...favoriteOrder]);
            const additions = spaces.map((s) => s.id).filter((id) => !tracked.has(id));
            return additions.length === 0 ? pruned : [...pruned, ...additions];
        });
    }, [spaces, favoriteOrder]);

    const applyMove = (spaceId: string, from: DndCategory, to: DndCategory, index: number) => {
        const next = moveBetween(favoriteOrder, spacesOrder, spaceId, to, index);
        setFavoriteOrder(next.favoriteOrder);
        setSpacesOrder(next.spacesOrder);
    };

    const toggleFavorite = (id: string) => {
        if (favoriteOrder.includes(id)) {
            applyMove(id, 'favorites', 'spaces', spacesOrder.length);
        } else {
            applyMove(id, 'spaces', 'favorites', favoriteOrder.length);
        }
    };

    useSpacesDnd({favoriteOrder, spacesOrder, onReorder: applyMove, enabled: dndEnabled});

    return {
        spacesById,
        favoriteOrder,
        spacesOrder,
        favoritesCollapsed,
        toggleFavoritesCollapsed: () => setFavoritesCollapsed((v) => !v),
        toggleFavorite,
    };
}
