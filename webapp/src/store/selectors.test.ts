// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import type {GlobalState} from '@mattermost/types/store';

import {getPagesForSpace, getSpaces, isSlugAvailable} from './selectors';
import {makePage, makeSpace} from './test_fixtures';
import type {DocsPluginState} from './types';

const makeState = (docsState: DocsPluginState): GlobalState =>
    ({['plugins-' + manifest.id]: docsState}) as unknown as GlobalState;

describe('getSpaces', () => {
    it('orders spaces by the order list, ignoring stale ids', () => {
        const spaceA = makeSpace('a', 'Space A');
        const spaceB = makeSpace('b', 'Space B');

        const state = makeState({
            spaces: {byId: {a: spaceA, b: spaceB}, order: ['b', 'a', 'missing']},
            pages: {byId: {}, bySpace: {}},
        });

        expect(getSpaces(state)).toEqual([spaceB, spaceA]);
    });
});

describe('isSlugAvailable', () => {
    it('is false when a space already claims the slug', () => {
        const state = makeState({
            spaces: {byId: {taken: makeSpace('taken', 'Taken')}, order: ['taken']},
            pages: {byId: {}, bySpace: {}},
        });

        expect(isSlugAvailable(state, 'taken')).toBe(false);
        expect(isSlugAvailable(state, 'free')).toBe(true);
    });
});

describe('getPagesForSpace', () => {
    it('resolves page ids for a space, ignoring other spaces', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-b', 'Page 2');

        const state = makeState({
            spaces: {byId: {}, order: []},
            pages: {byId: {p1: page1, p2: page2}, bySpace: {'space-a': ['p1'], 'space-b': ['p2']}},
        });

        expect(getPagesForSpace(state, 'space-a')).toEqual([page1]);
        expect(getPagesForSpace(state, 'space-c')).toEqual([]);
    });
});
