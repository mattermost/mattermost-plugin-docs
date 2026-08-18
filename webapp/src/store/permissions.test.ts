// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getCanManageSpaceMembers} from './permissions';

import {makeTestState} from '../../tests/react_testing_utils';

describe('getCanManageSpaceMembers', () => {
    it('allows a current space member', () => {
        const state = makeTestState({
            currentUser: {id: 'me'},
            docs: {spaceMembers: {'space-1': ['me', 'other']}},
        });

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(true);
    });

    it('denies a user outside the space', () => {
        const state = makeTestState({
            currentUser: {id: 'me'},
            docs: {spaceMembers: {'space-1': ['other']}},
        });

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(false);
        expect(getCanManageSpaceMembers(state, 'unloaded')).toBe(false);
    });
});
