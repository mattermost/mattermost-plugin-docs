// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {SpaceTypes} from './action_types';
import reducer from './reducer';
import {makeSpace} from './test_fixtures';

describe('reducer', () => {
    it('nests the entity slices under `entities`', () => {
        const initialState = reducer(undefined, {type: '@@INIT'});

        expect(Object.keys(initialState)).toEqual(['entities']);
        expect(Object.keys(initialState.entities)).toEqual([
            'spaces',
            'spacesInTeam',
            'pages',
            'pagesInSpace',
            'spaceMembers',
        ]);
    });

    it('routes entity actions into the `entities` subtree', () => {
        const spaceA = makeSpace('a', 'Space A', 't1');

        const next = reducer(undefined, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA]});

        expect(next.entities.spaces).toEqual({a: spaceA});
        expect(next.entities.spacesInTeam.t1).toEqual(new Set(['a']));
    });
});
