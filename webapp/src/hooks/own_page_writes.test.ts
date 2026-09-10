// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';

import {clearOwnPageWrites, recordOwnPageWrite, useOwnPageWrite} from './own_page_writes';

beforeEach(() => {
    clearOwnPageWrites();
});

describe('useOwnPageWrite', () => {
    it('knows nothing about a page we have not written', () => {
        const {result} = renderHook(() => useOwnPageWrite('page1'));

        expect(result.current).toBeUndefined();
    });

    it('reports a write that lands while a component is watching', () => {
        const {result} = renderHook(() => useOwnPageWrite('page1'));

        act(() => recordOwnPageWrite('page1', 200));

        expect(result.current).toBe(200);
    });

    it('keeps pages apart', () => {
        const {result} = renderHook(() => useOwnPageWrite('page1'));

        act(() => recordOwnPageWrite('page2', 200));

        expect(result.current).toBeUndefined();
    });

    it('never moves a page backwards', () => {
        const {result} = renderHook(() => useOwnPageWrite('page1'));

        act(() => recordOwnPageWrite('page1', 200));
        act(() => recordOwnPageWrite('page1', 100));

        expect(result.current).toBe(200);
    });
});
