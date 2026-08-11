// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {makePage, makeSpace} from 'store/test_fixtures';

import type {Page, Space} from 'types/docs';

import {useRoutedPage} from './pages';
import {useRoutedSpace} from './spaces';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockGetSpace = jest.fn();
const mockGetPage = jest.fn();

jest.mock('data', () => ({
    docsDataSource: {
        getSpace: (...args: unknown[]) => mockGetSpace(...args as []),
        getPage: (...args: unknown[]) => mockGetPage(...args as []),
    },
}));

// The test store is a fixed-state store, so a dispatched entity doesn't land in it.
// That's the point here: these tests assert what the hooks ask the server for and
// when they consider a routed id answered — not the reducer, which entities.test
// covers.
const wrapperFor = (spaces: Space[] = [], pages: Page[] = []) => {
    const store = makeTestStore({
        docs: {
            spaces: Object.fromEntries(spaces.map((space) => [space.id, space])),
            pages: Object.fromEntries(pages.map((page) => [page.id, page])),
        },
    });

    return ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>;
};

beforeEach(() => jest.clearAllMocks());

describe('useRoutedSpace', () => {
    it('fetches a space the store does not hold, by id', async () => {
        mockGetSpace.mockResolvedValue(makeSpace('eng', 'Engineering'));

        const {result} = renderHook(() => useRoutedSpace('eng'), {wrapper: wrapperFor()});

        await waitFor(() => expect(result.current.resolved).toBe(true));
        expect(mockGetSpace).toHaveBeenCalledWith('eng');
    });

    // A space in the store is already an answer; asking again would be a request per
    // navigation into it.
    it('does not fetch a space the store already holds', () => {
        const space = makeSpace('eng', 'Engineering');

        const {result} = renderHook(() => useRoutedSpace('eng'), {wrapper: wrapperFor([space])});

        expect(result.current).toEqual({space, resolved: true});
        expect(mockGetSpace).not.toHaveBeenCalled();
    });

    // The URL is only corrected once the id has an answer, so a failed or empty
    // lookup has to resolve rather than hang — otherwise a bad id renders nothing
    // forever.
    it('resolves with no space when the server cannot produce one', async () => {
        jest.spyOn(console, 'error').mockImplementation(() => {});
        mockGetSpace.mockRejectedValue(new Error('403'));

        const {result} = renderHook(() => useRoutedSpace('gone'), {wrapper: wrapperFor()});

        await waitFor(() => expect(result.current.resolved).toBe(true));
        expect(result.current.space).toBeUndefined();
    });

    it('does nothing without a routed id', () => {
        const {result} = renderHook(() => useRoutedSpace(undefined), {wrapper: wrapperFor()});

        expect(result.current).toEqual({space: undefined, resolved: false});
        expect(mockGetSpace).not.toHaveBeenCalled();
    });
});

describe('useRoutedPage', () => {
    it('fetches a page the space list does not hold, by id', async () => {
        mockGetPage.mockResolvedValue(makePage('intro', 'eng', 'Intro'));

        const {result} = renderHook(() => useRoutedPage('eng', 'intro'), {wrapper: wrapperFor()});

        await waitFor(() => expect(result.current.resolved).toBe(true));
        expect(mockGetPage).toHaveBeenCalledWith('eng', 'intro');
    });

    it('does not fetch a page the store already holds', () => {
        const page = makePage('intro', 'eng', 'Intro');

        const {result} = renderHook(() => useRoutedPage('eng', 'intro'), {wrapper: wrapperFor([], [page])});

        expect(result.current).toEqual({page, resolved: true});
        expect(mockGetPage).not.toHaveBeenCalled();
    });

    // Page ids are globally unique, so a URL can name a real page in another space.
    // Resolving it here would render it inside the wrong space.
    it('ignores a held page belonging to another space', async () => {
        mockGetPage.mockResolvedValue(undefined);
        const stray = makePage('intro', 'other-space', 'Intro');

        const {result} = renderHook(() => useRoutedPage('eng', 'intro'), {wrapper: wrapperFor([], [stray])});

        await waitFor(() => expect(result.current.resolved).toBe(true));
        expect(result.current.page).toBeUndefined();
        expect(mockGetPage).toHaveBeenCalledWith('eng', 'intro');
    });

    it('resolves with no page when the server cannot produce one', async () => {
        jest.spyOn(console, 'error').mockImplementation(() => {});
        mockGetPage.mockRejectedValue(new Error('403'));

        const {result} = renderHook(() => useRoutedPage('eng', 'gone'), {wrapper: wrapperFor()});

        await waitFor(() => expect(result.current.resolved).toBe(true));
        expect(result.current.page).toBeUndefined();
    });

    // On the draft route the page legitimately does not exist yet — the draft
    // reserved its id — so asking for it would 404 on every draft opened.
    it('skips the fetch when fetchMissing is off', () => {
        const {result} = renderHook(
            () => useRoutedPage('eng', 'intro', {fetchMissing: false}),
            {wrapper: wrapperFor()},
        );

        expect(result.current).toEqual({page: undefined, resolved: false});
        expect(mockGetPage).not.toHaveBeenCalled();
    });
});
