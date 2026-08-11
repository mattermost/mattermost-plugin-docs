// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {useSpaceStats} from './spaces';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockFetchDrafts = jest.fn();
const mockFetchPages = jest.fn();
const mockFetchSpaceMembers = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    fetchDrafts: (spaceId: string) => () => mockFetchDrafts(spaceId),
    fetchPages: (spaceId: string) => () => mockFetchPages(spaceId),
    fetchSpaceMembers: (spaceId: string) => () => mockFetchSpaceMembers(spaceId),
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
