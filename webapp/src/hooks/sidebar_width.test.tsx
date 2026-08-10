// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import {useSidebarWidth} from './sidebar_width';

import {makeTestStore} from '../../tests/react_testing_utils';

describe('useSidebarWidth', () => {
    beforeEach(() => window.localStorage.clear());

    it('clamps restored and committed widths to the panel bounds', () => {
        const store = makeTestStore({currentUser: {id: 'me'}});
        const wrapper = ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>;
        window.localStorage.setItem('docs_sidebar_width_test_me', '900');

        const {result} = renderHook(
            () => useSidebarWidth('test', 300, {minWidth: 200, maxWidth: 400}),
            {wrapper},
        );

        expect(result.current.width).toBe(400);

        act(() => result.current.commitWidth(100));

        expect(result.current.width).toBe(200);
        expect(window.localStorage.getItem('docs_sidebar_width_test_me')).toBe('200');
    });
});
