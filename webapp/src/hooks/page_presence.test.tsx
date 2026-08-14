// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import {getPageActiveEditors} from 'client/presence';

import type {PageActiveEditors} from 'types/drafts';

import {usePagePresence} from './page_presence';

jest.mock('client/presence', () => ({
    getPageActiveEditors: jest.fn(),
}));

jest.mock('client/presence_events', () => ({
    subscribeToPagePresence: () => () => undefined,
}));

const mockGetActiveEditors = getPageActiveEditors as jest.MockedFunction<typeof getPageActiveEditors>;

beforeEach(() => {
    mockGetActiveEditors.mockReset();
});

describe('usePagePresence', () => {
    it('reports the other editors on the page', async () => {
        mockGetActiveEditors.mockResolvedValue({
            active_editors: ['user1', 'user2'],
            snapshot_at: Date.now(),
            active_timeout_ms: 60000,
        });

        const {result} = renderHook(() => usePagePresence('space1', 'page1', 'user1'));

        await waitFor(() => expect(result.current).toEqual(['user2']));
    });

    it('drops the editors once the snapshot reaches its timeout', async () => {
        mockGetActiveEditors.mockResolvedValue({
            active_editors: ['user1', 'user2'],
            snapshot_at: Date.now() - 60000,
            active_timeout_ms: 60000,
        });

        const {result} = renderHook(() => usePagePresence('space1', 'page1', 'user1'));

        await waitFor(() => expect(mockGetActiveEditors).toHaveBeenCalled());
        expect(result.current).toEqual([]);
    });

    it('treats a null editor list as nobody rather than throwing', async () => {
        mockGetActiveEditors.mockResolvedValue({
            active_editors: null,
            snapshot_at: Date.now(),
            active_timeout_ms: 60000,
        } as unknown as PageActiveEditors);

        const {result} = renderHook(() => usePagePresence('space1', 'page1', 'user1'));

        await waitFor(() => expect(mockGetActiveEditors).toHaveBeenCalled());
        expect(result.current).toEqual([]);
    });
});
