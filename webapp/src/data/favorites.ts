// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Docs sidebar favorites and ordering, stored as Mattermost user preferences.
//
// Core's channel sidebar keeps this in a first-class server model (ChannelCategory:
// per-user, per-team, with an ordered channel_ids list), and favoriting a channel
// is a move into the category of type `favorites`. The Docs server has no category
// model, so this uses the user-preferences API instead — the same class of
// mechanism (per-user sidebar state synced by the server) without inventing a
// Docs categories endpoint.
//
// Preference field limits are category ≤ 32 chars, name ≤ 32 chars, so both keys
// below fit a 26-char platform id with room to spare.

/** One preference per favorited item: name = item id, value = FavoriteType. */
export const FAVORITES_CATEGORY = 'docs_favorites';

/** One preference per team: name = team id, value = a serialized SidebarOrder. */
export const SIDEBAR_ORDER_CATEGORY = 'docs_sidebar_order';

/**
 * What kind of thing a favorite points at. New kinds are additive: they are just
 * a new preference `value`, needing no migration.
 */
export type FavoriteType = 'space' | 'page';

export type FavoriteRef = {
    type: FavoriteType;
    id: string;
};

const FAVORITE_TYPES: FavoriteType[] = ['space', 'page'];

const isFavoriteType = (value: string): value is FavoriteType => (FAVORITE_TYPES as string[]).includes(value);

/** Parses a stored preference pair, or null when the value isn't a known type. */
export function parseFavorite(name: string, value: string): FavoriteRef | null {
    return isFavoriteType(value) ? {type: value, id: name} : null;
}

// Favorites can mix types, so their order is keyed by "type:id"; the plain
// spaces category only ever holds space ids.
export const favoriteKey = ({type, id}: FavoriteRef): string => `${type}:${id}`;

export function parseFavoriteKey(key: string): FavoriteRef | null {
    const separator = key.indexOf(':');
    if (separator === -1) {
        return null;
    }
    const type = key.slice(0, separator);
    const id = key.slice(separator + 1);
    return id && isFavoriteType(type) ? {type, id} : null;
}

export type SidebarOrder = {
    favorites: string[];
    spaces: string[];
};

export const EMPTY_SIDEBAR_ORDER: SidebarOrder = {favorites: [], spaces: []};

const asStringArray = (value: unknown): string[] =>
    (Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : []);

/** Tolerates absent/corrupt values by falling back to an empty order. */
export function parseSidebarOrder(value?: string): SidebarOrder {
    if (!value) {
        return EMPTY_SIDEBAR_ORDER;
    }
    try {
        const parsed = JSON.parse(value) as Partial<SidebarOrder>;
        return {
            favorites: asStringArray(parsed.favorites),
            spaces: asStringArray(parsed.spaces),
        };
    } catch {
        return EMPTY_SIDEBAR_ORDER;
    }
}

// The server rejects a preference value over MaxPreferenceValueLength (20000
// chars), which a stored order would hit at roughly 550 entries. Manual order
// only matters for what the user actually dragged, so the payload is capped and
// trimmed from the tail (the least-recently-positioned end); anything dropped
// falls back to the natural order rather than failing the whole save.
export const MAX_PREFERENCE_VALUE_LENGTH = 20000;

// Leaves headroom under the server limit for JSON overhead.
const ORDER_BUDGET = MAX_PREFERENCE_VALUE_LENGTH - 1000;

export function serializeSidebarOrder(order: SidebarOrder): string {
    let trimmed = order;
    let serialized = JSON.stringify(trimmed);

    while (serialized.length > ORDER_BUDGET && (trimmed.favorites.length || trimmed.spaces.length)) {
        // Drop from whichever list is longer, so one huge list can't starve the other.
        if (trimmed.spaces.length >= trimmed.favorites.length) {
            trimmed = {...trimmed, spaces: trimmed.spaces.slice(0, -1)};
        } else {
            trimmed = {...trimmed, favorites: trimmed.favorites.slice(0, -1)};
        }
        serialized = JSON.stringify(trimmed);
    }

    return serialized;
}
