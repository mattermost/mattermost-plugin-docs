// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {copyToClipboard} from 'utils/clipboard';

import {clearReadout, getReadoutMessage} from 'components/readout/readout_store';

import {useCopyText} from './copy_text';

jest.mock('utils/clipboard', () => ({copyToClipboard: jest.fn()}));

const mockCopy = copyToClipboard as jest.MockedFunction<typeof copyToClipboard>;

describe('useCopyText', () => {
    beforeEach(() => {
        jest.useFakeTimers();
        jest.clearAllMocks();
        mockCopy.mockResolvedValue(true);
        clearReadout();
    });

    afterEach(() => jest.useRealTimers());

    it('copies and confirms for a moment', async () => {
        const {result} = renderHook(() => useCopyText('https://example.com/space'));

        expect(result.current.copied).toBe(false);

        await act(async () => result.current.copy());

        expect(mockCopy).toHaveBeenCalledWith('https://example.com/space');
        expect(result.current.copied).toBe(true);

        act(() => jest.advanceTimersByTime(2000));

        expect(result.current.copied).toBe(false);
    });

    // The clipboard write can fail on permissions or an insecure context, and
    // confirming a copy that never happened is worse than staying quiet.
    it('says nothing when the copy fails', async () => {
        mockCopy.mockResolvedValue(false);

        const {result} = renderHook(() => useCopyText('link', {announcement: 'Copied'}));

        await act(async () => result.current.copy());

        expect(result.current.copied).toBe(false);
        expect(getReadoutMessage()).toBe('');
    });

    it('announces the confirmation through the live region', async () => {
        const {result} = renderHook(() => useCopyText('link', {announcement: 'Copied'}));

        await act(async () => result.current.copy());

        expect(getReadoutMessage()).toBe('Copied');
    });

    it('holds the confirmation open when copied again', async () => {
        const {result} = renderHook(() => useCopyText('link'));

        await act(async () => result.current.copy());
        act(() => jest.advanceTimersByTime(1500));
        await act(async () => result.current.copy());
        act(() => jest.advanceTimersByTime(1500));

        expect(result.current.copied).toBe(true);
    });
});
