// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {useResolveSpacePermissions, useSpaceStats} from './spaces';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockFetchDrafts = jest.fn();
const mockFetchPages = jest.fn();
const mockLoadSpaceAccessMembers = jest.fn();
const mockLoadSpaceAccess = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    fetchDrafts: (spaceId: string) => () => mockFetchDrafts(spaceId),
    fetchPages: (spaceId: string) => () => mockFetchPages(spaceId),
    fetchSpaceMembers: (spaceId: string) => () => mockLoadSpaceAccessMembers(spaceId),
    loadSpaceAccess: (spaceId: string) => () => mockLoadSpaceAccess(spaceId),
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
        expect(mockLoadSpaceAccessMembers).toHaveBeenCalledWith('space-1');
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

    // The read answers a failure without rejecting, and nothing the hook's effect depends on
    // changes when that happens — so without an explicit retry trigger the space stays unresolved
    // for as long as the view is open.
    it('resolves the same space again after a failed attempt', async () => {
        mockLoadSpaceAccess.mockResolvedValueOnce({denied: false});
        mockLoadSpaceAccess.mockResolvedValueOnce({space: {id: 'space-1'}, denied: false});

        renderHook(() => useResolveSpacePermissions('space-1'), {wrapper});
        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(1);

        await advancePastRetry();

        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(2);
        expect(mockLoadSpaceAccess).toHaveBeenNthCalledWith(2, 'space-1');
    });

    // A space that keeps failing must not re-request for as long as it stays open. The expected
    // count is written out rather than read from the hook's cap, so a change to that cap fails here.
    it('gives up after a bounded number of attempts', async () => {
        mockLoadSpaceAccess.mockResolvedValue({denied: false});

        renderHook(() => useResolveSpacePermissions('space-1'), {wrapper});

        for (let i = 0; i < 5; i++) {
            // eslint-disable-next-line no-await-in-loop
            await advancePastRetry();
        }

        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(3);
    });

    it('starts a fresh attempt budget when the space changes', async () => {
        mockLoadSpaceAccess.mockResolvedValue({denied: false});

        const {rerender} = renderHook(({id}) => useResolveSpacePermissions(id), {
            wrapper,
            initialProps: {id: 'space-1'},
        });
        for (let i = 0; i < 5; i++) {
            // eslint-disable-next-line no-await-in-loop
            await advancePastRetry();
        }
        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(3);

        rerender({id: 'space-2'});

        expect(mockLoadSpaceAccess).toHaveBeenNthCalledWith(4, 'space-2');
    });

    // A denial is the server's answer, not a failed attempt: the space is evicted, so retrying
    // would only repeat the refusal for as long as the view stays open.
    it('does not retry a denied space', async () => {
        mockLoadSpaceAccess.mockResolvedValue({denied: true});

        renderHook(() => useResolveSpacePermissions('space-1'), {wrapper});

        for (let i = 0; i < 5; i++) {
            // eslint-disable-next-line no-await-in-loop
            await advancePastRetry();
        }

        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(1);
    });

    it('starts a new request when returning to a space before its previous request settles', async () => {
        let settleFirst: (load: {space: {id: string}; denied: boolean}) => void = () => {};
        mockLoadSpaceAccess.mockImplementationOnce(() => new Promise((resolve) => {
            settleFirst = resolve;
        }));
        mockLoadSpaceAccess.mockResolvedValueOnce({space: {id: 'space-1'}, denied: false});

        const {rerender} = renderHook(({id}: {id?: string}) => useResolveSpacePermissions(id), {
            wrapper,
            initialProps: {id: 'space-1'} as {id?: string},
        });
        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(1);

        rerender({id: undefined});
        rerender({id: 'space-1'});

        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(2);
        expect(mockLoadSpaceAccess).toHaveBeenNthCalledWith(2, 'space-1');

        await act(async () => {
            settleFirst({space: {id: 'space-1'}, denied: false});
            await Promise.resolve();
            jest.runAllTimers();
        });

        expect(mockLoadSpaceAccess).toHaveBeenCalledTimes(2);
    });
});
