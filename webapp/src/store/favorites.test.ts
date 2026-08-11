// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {FAVORITES_CATEGORY} from 'data/favorites';

import {makePage, makeSpace} from 'store/test_fixtures';

import type {Page} from 'types/docs';

import {
    getDocsFavoriteIds,
    getDocsFavoritePagesBySpace,
    getDocsFavoriteSpaceIds,
    getDocsFavorites,
    getSpaceFavoriteState,
} from './favorites';

import {makeTestState} from '../../tests/react_testing_utils';

const ENG = makeSpace('eng', 'Engineering');
const OPS = makeSpace('ops', 'Operations');

const pageIn = (id: string, spaceId: string): Page => makePage(id, spaceId, id);

const PAGES = [pageIn('engPage', ENG.id), pageIn('opsPage', OPS.id)];

// Favorites are user preferences: the preference *name* is the favorited id and
// its *value* is the kind of thing it points at (see data/favorites parseFavorite).
const favorite = (id: string, type: string) => ({category: FAVORITES_CATEGORY, name: id, value: type});

const stateWith = (favorites: Array<[string, string]>) => makeTestState({
    docs: {
        spaces: {[ENG.id]: ENG, [OPS.id]: OPS},
        pages: Object.fromEntries(PAGES.map((page) => [page.id, page])),
    },
    preferences: favorites.map(([id, type]) => favorite(id, type)),
});

describe('getDocsFavorites', () => {
    it('parses stored favorites and skips unknown types', () => {
        const state = stateWith([['eng', 'space'], ['engPage', 'page'], ['xyz', 'wat']]);

        expect(getDocsFavorites(state)).toEqual([
            {type: 'space', id: 'eng'},
            {type: 'page', id: 'engPage'},
        ]);
        expect(getDocsFavoriteIds(state)).toEqual(new Set(['eng', 'engPage']));
    });
});

describe('getDocsFavoritePagesBySpace', () => {
    it('groups favorited pages under the space that owns them', () => {
        const state = stateWith([['engPage', 'page'], ['opsPage', 'page']]);

        expect(getDocsFavoritePagesBySpace(state)).toEqual(new Map([
            [ENG.id, ['engPage']],
            [OPS.id, ['opsPage']],
        ]));
    });

    it('omits a favorited page whose space has not loaded', () => {
        const state = makeTestState({preferences: [favorite('engPage', 'page')]});

        expect(getDocsFavoritePagesBySpace(state).size).toBe(0);
    });
});

describe('getSpaceFavoriteState', () => {
    it('is on when the space itself is favorited', () => {
        expect(getSpaceFavoriteState(stateWith([['eng', 'space']]), ENG.id)).toBe('on');
    });

    // Nested-checkbox semantics: the space isn't favorited, but something inside it is.
    it('is partial when only a page inside it is favorited', () => {
        expect(getSpaceFavoriteState(stateWith([['engPage', 'page']]), ENG.id)).toBe('partial');
    });

    it('is on, not partial, when both the space and one of its pages are favorited', () => {
        expect(getSpaceFavoriteState(stateWith([['eng', 'space'], ['engPage', 'page']]), ENG.id)).toBe('on');
    });

    it('is off when neither the space nor its pages are favorited', () => {
        expect(getSpaceFavoriteState(stateWith([['ops', 'space']]), ENG.id)).toBe('off');
    });
});

describe('getDocsFavoriteSpaceIds', () => {
    it('includes spaces that only hold a favorited page, without duplicating', () => {
        const state = stateWith([['eng', 'space'], ['engPage', 'page'], ['opsPage', 'page']]);

        expect(getDocsFavoriteSpaceIds(state)).toEqual([ENG.id, OPS.id]);
    });
});
