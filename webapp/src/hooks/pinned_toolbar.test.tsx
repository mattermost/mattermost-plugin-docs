// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {savePreferences} from 'mattermost-redux/actions/preferences';

import {usePinnedToolbar} from './pinned_toolbar';

jest.mock('mattermost-redux/actions/preferences', () => ({
    savePreferences: jest.fn(() => ({type: 'MOCK_SAVE_PREFERENCES'})),
}));

const mockSavePreferences = savePreferences as jest.MockedFunction<typeof savePreferences>;

const storeWith = (preferences: Record<string, {category: string; name: string; value: string}>) => ({
    getState: () => ({
        entities: {
            users: {currentUserId: 'user1', profiles: {}},
            preferences: {myPreferences: preferences},
            general: {config: {}},
        },
    }),
    subscribe: () => () => undefined,
    dispatch: jest.fn(),
});

const wrapperFor = (store: ReturnType<typeof storeWith>) => ({children}: {children: React.ReactNode}) => (
    <Provider store={store as never}>{children}</Provider>
);

describe('usePinnedToolbar', () => {
    it('is pinned when the user has never chosen', () => {
        const {result} = renderHook(() => usePinnedToolbar(), {wrapper: wrapperFor(storeWith({}))});

        expect(result.current[0]).toBe(true);
    });

    it('reads the unpinned choice back from the user preference', () => {
        const store = storeWith({
            'docs_editor--toolbar_pinned': {category: 'docs_editor', name: 'toolbar_pinned', value: 'false'},
        });

        const {result} = renderHook(() => usePinnedToolbar(), {wrapper: wrapperFor(store)});

        expect(result.current[0]).toBe(false);
    });

    it('saves the opposite choice as a user preference', () => {
        const {result} = renderHook(() => usePinnedToolbar(), {wrapper: wrapperFor(storeWith({}))});

        act(() => result.current[1]());

        expect(mockSavePreferences).toHaveBeenCalledWith('user1', [{
            user_id: 'user1',
            category: 'docs_editor',
            name: 'toolbar_pinned',
            value: 'false',
        }]);
    });

    it('inverts the value it last asked for when toggled twice before the store catches up', () => {
        const {result} = renderHook(() => usePinnedToolbar(), {wrapper: wrapperFor(storeWith({}))});

        act(() => {
            result.current[1]();
            result.current[1]();
        });

        expect(mockSavePreferences.mock.calls.map((call) => call[1][0].value)).toEqual(['false', 'true']);
    });
});
