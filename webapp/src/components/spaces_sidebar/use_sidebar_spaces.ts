// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaces} from 'hooks/use_spaces';
import {useMemo, useState} from 'react';

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

export type SidebarSpacesModel = {
    spacesById: Map<string, Space>;
    favoriteOrder: string[];
    spacesOrder: string[];
    favoritesCollapsed: boolean;
    toggleFavoritesCollapsed: () => void;
    toggleFavorite: (id: string) => void;
};

// View-model for the spaces sidebar: ordered favorites/spaces, collapse state,
// favorite toggling, and the drag-and-drop reorder wiring. Holds local state
// today; ordering/favorites move to user preferences in a later phase.
export function useSidebarSpaces(): SidebarSpacesModel {
    const spaces = useSpaces();
    const [favoriteOrder, setFavoriteOrder] = useState<string[]>([]);
    const [spacesOrder, setSpacesOrder] = useState<string[]>(() => spaces.map((s) => s.id));
    const [favoritesCollapsed, setFavoritesCollapsed] = useState(false);

    const spacesById = useMemo(() => new Map(spaces.map((s) => [s.id, s])), [spaces]);

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

    useSpacesDnd({favoriteOrder, spacesOrder, onReorder: applyMove});

    return {
        spacesById,
        favoriteOrder,
        spacesOrder,
        favoritesCollapsed,
        toggleFavoritesCollapsed: () => setFavoritesCollapsed((v) => !v),
        toggleFavorite,
    };
}
