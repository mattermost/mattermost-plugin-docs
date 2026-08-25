// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {useResolveSpacePermissions, useSpaceStats} from './spaces';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockFetchDrafts = jest.fn();
const mockFetchPages = jest.fn();
const mockFetchSpaceMembers = jest.fn();
const mockFetchSpace = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    fetchDrafts: (spaceId: string) => () => mockFetchDrafts(spaceId),
    fetchPages: (spaceId: string) => () => mockFetchPages(spaceId),
    fetchSpaceMembers: (spaceId: string) => () => mockFetchSpaceMembers(spaceId),
    fetchSpace: (spaceId: string) => () => mockFetchSpace(spaceId),
}));

const wrapper = ({children}: {children: React.ReactNode}) => (
    <Provider store={makeTestStore()}>{children}</Provider>
);

describe('useSpaceStats', () => {
    beforeEach(() => jest.clearAllMocks());

    it('loads the caller\'s drafts with the rest of the space data', async () => {
        renderHook(() => useSpaceStats('space-1'), {wrapper});

        await waitFor(() => expect(mockFetchDrafts).toHaveBeenCalledWith('space-1'));
        expect(mockFetchPages).toHaveBeenCalledWith('space-1');
        expect(mockFetchSpaceMembers).toHaveBeenCalledWith('space-1');
    });
});

describe('useResolveSpacePermissions', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        jest.useFakeTimers();
    });

    afterEach(() => jest.useRealTimers());

    // Settle the resolution's promise callback, then fire the retry it scheduled.
    const advancePastRetry = () => act(async () => {
        await Promise.resolve();
        jest.runAllTimers();
    });

    // fetchSpace answers a failure with undefined rather than by rejecting, and nothing the hook's
    // effect depends on changes when that happens — so without an explicit retry trigger the space
    // stays unresolved for as long as the view is open.
    it('resolves the same space again after a failed attempt', async () => {
        mockFetchSpace.mockResolvedValueOnce(undefined);
        mockFetchSpace.mockResolvedValueOnce({id: 'space-1'});

        renderHook(() => useResolveSpacePermissions('space-1'), {wrapper});
        expect(mockFetchSpace).toHaveBeenCalledTimes(1);

        await advancePastRetry();

        expect(mockFetchSpace).toHaveBeenCalledTimes(2);
        expect(mockFetchSpace).toHaveBeenNthCalledWith(2, 'space-1');
    });

    // A space that keeps failing must not re-request for as long as it stays open. The expected
    // count is written out rather than read from the hook's cap, so a change to that cap fails here.
    it('gives up after a bounded number of attempts', async () => {
        mockFetchSpace.mockResolvedValue(undefined);

        renderHook(() => useResolveSpacePermissions('space-1'), {wrapper});

        for (let i = 0; i < 5; i++) {
            // eslint-disable-next-line no-await-in-loop
            await advancePastRetry();
        }

        expect(mockFetchSpace).toHaveBeenCalledTimes(3);
    });

    it('starts a fresh attempt budget when the space changes', async () => {
        mockFetchSpace.mockResolvedValue(undefined);

        const {rerender} = renderHook(({id}) => useResolveSpacePermissions(id), {
            wrapper,
            initialProps: {id: 'space-1'},
        });
        for (let i = 0; i < 5; i++) {
            // eslint-disable-next-line no-await-in-loop
            await advancePastRetry();
        }
        expect(mockFetchSpace).toHaveBeenCalledTimes(3);

        rerender({id: 'space-2'});

        expect(mockFetchSpace).toHaveBeenNthCalledWith(4, 'space-2');
    });
});
