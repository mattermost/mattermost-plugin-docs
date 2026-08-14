// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import type {Store, UnknownAction} from 'redux';

import type {GlobalState} from '@mattermost/types/store';
import type {UserProfile} from '@mattermost/types/users';

import {useSpaceMemberProfiles, useUserProfile} from './members';

import {makeTestState} from '../../tests/react_testing_utils';

jest.mock('mattermost-redux/actions/users', () => ({
    getMissingProfilesByIds: (ids: string[]) => ({type: 'test/getMissingProfilesByIds', ids}),
}));

const REPLACE_PROFILES = 'test/replaceProfiles';

type Profiles = GlobalState['entities']['users']['profiles'];
type ReplaceProfilesAction = UnknownAction & {profiles: Profiles};

const profile = (id: string, username: string): UserProfile => ({
    id,
    username,
    first_name: username,
    last_name: '',
    nickname: '',
    last_picture_update: 0,
} as UserProfile);

const makeProfileStore = (memberIds: string[] = []) => {
    const alice = profile('alice', 'alice');
    const state = makeTestState({
        currentUser: alice,
        docs: {spaceMembers: {eng: memberIds}},
    });
    const initialState = {
        ...state,
        entities: {
            ...state.entities,
            general: {config: {}, license: {}},
        },
    } as GlobalState;
    const reducer = (state = initialState, action: UnknownAction): GlobalState => {
        if (action.type !== REPLACE_PROFILES) {
            return state;
        }
        return {
            ...state,
            entities: {
                ...state.entities,
                users: {
                    ...state.entities.users,
                    profiles: (action as ReplaceProfilesAction).profiles,
                },
            },
        };
    };

    return createStore(reducer);
};

const wrapperFor = (store: Store<GlobalState>) =>
    ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>;

const addUnrelatedProfile = (store: Store<GlobalState>) => {
    store.dispatch({
        type: REPLACE_PROFILES,
        profiles: {
            ...store.getState().entities.users.profiles,
            bob: profile('bob', 'bob'),
        },
    });
};

describe('member profile hooks', () => {
    it('does not rerender a single profile for an unrelated user update', () => {
        const store = makeProfileStore();
        let renders = 0;

        const {result} = renderHook(() => {
            renders += 1;
            return useUserProfile('alice');
        }, {wrapper: wrapperFor(store)});

        expect(result.current?.id).toBe('alice');
        expect(renders).toBe(1);

        act(() => addUnrelatedProfile(store));

        expect(renders).toBe(1);
    });

    it('does not rerender member profiles for an unrelated user update', () => {
        const store = makeProfileStore(['alice']);
        let renders = 0;

        const {result} = renderHook(() => {
            renders += 1;
            return useSpaceMemberProfiles('eng');
        }, {wrapper: wrapperFor(store)});

        expect(result.current.map(({id}) => id)).toEqual(['alice']);
        expect(renders).toBe(1);

        act(() => addUnrelatedProfile(store));

        expect(renders).toBe(1);
    });
});
