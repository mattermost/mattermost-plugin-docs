// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {copyToClipboard} from 'utils/clipboard';

import {useCopyText} from './copy_text';

jest.mock('utils/clipboard', () => ({copyToClipboard: jest.fn()}));

describe('useCopyText', () => {
    beforeEach(() => {
        jest.useFakeTimers();
        jest.clearAllMocks();
    });

    afterEach(() => jest.useRealTimers());

    it('copies and confirms for a moment', () => {
        const {result} = renderHook(() => useCopyText('https://example.com/space'));

        expect(result.current.copied).toBe(false);

        act(() => result.current.copy());

        expect(copyToClipboard).toHaveBeenCalledWith('https://example.com/space');
        expect(result.current.copied).toBe(true);

        act(() => jest.advanceTimersByTime(2000));

        expect(result.current.copied).toBe(false);
    });

    it('holds the confirmation open when copied again', () => {
        const {result} = renderHook(() => useCopyText('link'));

        act(() => result.current.copy());
        act(() => jest.advanceTimersByTime(1500));
        act(() => result.current.copy());
        act(() => jest.advanceTimersByTime(1500));

        expect(result.current.copied).toBe(true);
    });
});
