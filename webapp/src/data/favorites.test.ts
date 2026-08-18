// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    EMPTY_SIDEBAR_ORDER,
    FAVORITES_CATEGORY,
    MAX_PREFERENCE_VALUE_LENGTH,
    SIDEBAR_ORDER_CATEGORY,
    favoriteKey,
    parseFavorite,
    parseFavoriteKey,
    parseSidebarOrder,
    serializeSidebarOrder,
} from './favorites';

describe('favorites preference keys', () => {
    // The server rejects a preference whose category or name exceeds 32 chars,
    // and ids are 26-char platform ids.
    it('keeps category names within the preference limit', () => {
        expect(FAVORITES_CATEGORY.length).toBeLessThanOrEqual(32);
        expect(SIDEBAR_ORDER_CATEGORY.length).toBeLessThanOrEqual(32);
    });

    it('parses a stored favorite, rejecting unknown types', () => {
        expect(parseFavorite('abc', 'space')).toEqual({type: 'space', id: 'abc'});
        expect(parseFavorite('abc', 'page')).toEqual({type: 'page', id: 'abc'});
        expect(parseFavorite('abc', 'channel')).toBeNull();
    });

    it('round-trips a favorite key', () => {
        const ref = {type: 'page' as const, id: 'p1'};
        expect(parseFavoriteKey(favoriteKey(ref))).toEqual(ref);
    });

    it('rejects malformed favorite keys', () => {
        expect(parseFavoriteKey('p1')).toBeNull();
        expect(parseFavoriteKey('bogus:p1')).toBeNull();
        expect(parseFavoriteKey('page:')).toBeNull();
    });
});

describe('sidebar order', () => {
    it('round-trips an order', () => {
        const order = {favorites: ['space:a', 'page:b'], spaces: ['c']};
        expect(parseSidebarOrder(serializeSidebarOrder(order))).toEqual(order);
    });

    it('falls back to empty for missing or corrupt values', () => {
        expect(parseSidebarOrder(undefined)).toEqual(EMPTY_SIDEBAR_ORDER);
        expect(parseSidebarOrder('not json')).toEqual(EMPTY_SIDEBAR_ORDER);
        expect(parseSidebarOrder('null')).toEqual(EMPTY_SIDEBAR_ORDER);
        expect(parseSidebarOrder('[]')).toEqual(EMPTY_SIDEBAR_ORDER);
        expect(parseSidebarOrder('5')).toEqual(EMPTY_SIDEBAR_ORDER);
    });

    // A rejected preference would lose the whole order, so an oversized list is
    // trimmed to fit instead.
    it('caps the payload under the server value limit', () => {
        const many = Array.from({length: 5000}, (_, i) => `space:${String(i).padStart(26, '0')}`);
        const serialized = serializeSidebarOrder({favorites: many, spaces: many});

        expect(serialized.length).toBeLessThanOrEqual(MAX_PREFERENCE_VALUE_LENGTH);

        // What survives is still a valid, parseable prefix of the order.
        const parsed = parseSidebarOrder(serialized);
        expect(parsed.favorites.length).toBeGreaterThan(0);
        expect(parsed.favorites[0]).toBe(many[0]);
    });

    // Trimming takes from the longer list so one huge list can't starve the other
    // out of the budget entirely.
    it('trims the longer list when the two are lopsided', () => {
        const key = (i: number) => `space:${String(i).padStart(26, '0')}`;
        const many = Array.from({length: 5000}, (_, i) => key(i));
        const few = [key(9001), key(9002)];

        const parsed = parseSidebarOrder(serializeSidebarOrder({favorites: few, spaces: many}));

        expect(parsed.favorites).toEqual(few);
        expect(parsed.spaces.length).toBeGreaterThan(0);
        expect(parsed.spaces.length).toBeLessThan(many.length);
    });

    it('drops non-string entries rather than trusting the payload', () => {
        expect(parseSidebarOrder('{"favorites":["a",5,null],"spaces":"nope"}')).toEqual({
            favorites: ['a'],
            spaces: [],
        });
    });
});
