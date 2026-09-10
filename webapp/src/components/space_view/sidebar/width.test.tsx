// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {MAX_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH, usePagesSidebarWidth} from './width';

import {makeTestStore} from '../../../../tests/react_testing_utils';

const setViewportWidth = (width: number) => {
    Object.defineProperty(window, 'innerWidth', {configurable: true, writable: true, value: width});
    act(() => {
        window.dispatchEvent(new Event('resize'));
    });
};

const renderPagesSidebarWidth = () => {
    const store = makeTestStore({currentUser: {id: 'me'}});
    const wrapper = ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>;
    return renderHook(() => usePagesSidebarWidth(), {wrapper});
};

describe('usePagesSidebarWidth', () => {
    const originalWidth = window.innerWidth;

    beforeEach(() => {
        window.localStorage.clear();
        Object.defineProperty(window, 'innerWidth', {configurable: true, writable: true, value: 1600});
    });

    afterAll(() => {
        Object.defineProperty(window, 'innerWidth', {configurable: true, writable: true, value: originalWidth});
    });

    it('lets a wide window reach the full cap', () => {
        const {result} = renderPagesSidebarWidth();

        expect(result.current.maxWidth).toBe(MAX_SIDEBAR_WIDTH);

        act(() => result.current.commitWidth(MAX_SIDEBAR_WIDTH));

        expect(result.current.width).toBe(MAX_SIDEBAR_WIDTH);
    });

    it('caps the width at a share of a narrower window', () => {
        const {result} = renderPagesSidebarWidth();
        act(() => result.current.commitWidth(MAX_SIDEBAR_WIDTH));

        setViewportWidth(800);

        expect(result.current.maxWidth).toBe(320);
        expect(result.current.width).toBe(320);
    });

    it('never caps below the minimum width', () => {
        const {result} = renderPagesSidebarWidth();

        setViewportWidth(400);

        expect(result.current.maxWidth).toBe(MIN_SIDEBAR_WIDTH);
        expect(result.current.width).toBe(MIN_SIDEBAR_WIDTH);
    });

    it('restores the stored width when the window widens again', () => {
        const {result} = renderPagesSidebarWidth();
        act(() => result.current.commitWidth(MAX_SIDEBAR_WIDTH));

        setViewportWidth(800);
        expect(result.current.width).toBe(320);

        setViewportWidth(1600);

        expect(result.current.width).toBe(MAX_SIDEBAR_WIDTH);
    });
});
