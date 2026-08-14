// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {useAppDispatch} from 'hooks/redux';

import type {UserProfile} from '@mattermost/types/users';

import {searchProfiles} from 'mattermost-redux/actions/users';

import {useUserSearch} from './user_search';

jest.mock('hooks/redux', () => ({
    useAppDispatch: jest.fn(),
    useAppSelector: (selector: (state: unknown) => unknown) => selector({}),
}));

jest.mock('mattermost-redux/actions/users', () => ({
    searchProfiles: jest.fn((term: string) => ({type: 'test/searchProfiles', term})),
}));

jest.mock('mattermost-redux/selectors/entities/preferences', () => ({
    getTeammateNameDisplaySetting: () => '',
}));

jest.mock('mattermost-redux/selectors/entities/teams', () => ({
    getCurrentTeamId: () => 'team',
}));

const mockUseAppDispatch = useAppDispatch as jest.MockedFunction<typeof useAppDispatch>;
const mockSearchProfiles = searchProfiles as jest.MockedFunction<typeof searchProfiles>;
const mockDispatch = jest.fn();

const alice = {
    id: 'alice',
    username: 'alice',
    first_name: 'Alice',
    last_name: '',
    nickname: '',
    last_picture_update: 0,
} as UserProfile;

describe('useUserSearch', () => {
    beforeEach(() => {
        jest.useFakeTimers();
        mockDispatch.mockResolvedValue({data: [alice]});
        mockUseAppDispatch.mockReturnValue(mockDispatch as ReturnType<typeof useAppDispatch>);
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    it('clears results immediately when the search term changes', async () => {
        const hook = renderHook(
            ({term}: {term: string}) => useUserSearch(term, []),
            {initialProps: {term: 'alice'}},
        );

        await act(async () => {
            jest.advanceTimersByTime(300);
            await Promise.resolve();
        });

        expect(mockSearchProfiles).toHaveBeenCalledWith('alice', {team_id: 'team', limit: 20});
        expect(hook.result.current.results.map(({id}) => id)).toEqual(['alice']);

        hook.rerender({term: 'bob'});

        expect(hook.result.current.results).toEqual([]);
        expect(hook.result.current.loading).toBe(true);
    });
});
