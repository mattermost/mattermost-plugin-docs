// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import type {GlobalState} from '@mattermost/types/store';

import {getPagesForSpace, getSpacesInTeam} from './selectors';
import {makePage, makeSpace} from './test_fixtures';
import type {DocsPluginState} from './types';

const makeState = (docsState: DocsPluginState): GlobalState =>
    ({['plugins-' + manifest.id]: docsState}) as unknown as GlobalState;

describe('getSpacesInTeam', () => {
    it('resolves a team\'s ids to spaces ordered by sort_order, ignoring stale ids', () => {
        const spaceA = makeSpace('a', 'Space A', 't1', 1);
        const spaceB = makeSpace('b', 'Space B', 't1', 0);

        const state = makeState({
            spaces: {a: spaceA, b: spaceB},
            spacesInTeam: {t1: new Set(['a', 'b', 'missing'])},
            pages: {},
            pagesInSpace: {},
            spaceMembers: {},
        });

        expect(getSpacesInTeam(state, 't1')).toEqual([spaceB, spaceA]);
        expect(getSpacesInTeam(state, 't2')).toEqual([]);
    });
});

describe('getPagesForSpace', () => {
    it('resolves page ids for a space, ignoring other spaces', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-b', 'Page 2');

        const state = makeState({
            spaces: {},
            spacesInTeam: {},
            pages: {p1: page1, p2: page2},
            pagesInSpace: {'space-a': new Set(['p1']), 'space-b': new Set(['p2'])},
            spaceMembers: {},
        });

        expect(getPagesForSpace(state, 'space-a')).toEqual([page1]);
        expect(getPagesForSpace(state, 'space-c')).toEqual([]);
    });
});
