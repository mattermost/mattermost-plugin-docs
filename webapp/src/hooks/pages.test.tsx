// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {makePage, makeSpace} from 'store/test_fixtures';

import {toast} from 'components/toast';

import {SPACE_PROP_DEFAULT_PAGE_ID} from 'types/docs';
import type {Page, Space} from 'types/docs';

import {useCreateRootPage, useDefaultPagePath} from './pages';

import {makeTestStore} from '../../tests/react_testing_utils';

let mockRoute: {pageId?: string; isOverview: boolean} = {isOverview: false};
const mockGoToPage = jest.fn();
const mockGoToEditPage = jest.fn();

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({
        ...mockRoute,
        goToPage: mockGoToPage,
        goToEditPage: mockGoToEditPage,
        paths: {page: (spaceId: string, pageId: string) => `/docs/${spaceId}/${pageId}`},
    }),
}));

const mockCreatePage = jest.fn();
let mockCreateResult: Promise<Page> = Promise.resolve(makePage('new', 'eng', 'Untitled'));

// createPage is a thunk the hook awaits for the created page, so the mock has to
// be a thunk that resolves to one too.
jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    createPage: (...args: unknown[]) => {
        mockCreatePage(...args as []);
        return async () => mockCreateResult;
    },
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const withDefault = (pageId: string): Space => ({
    ...makeSpace('eng', 'Engineering'),
    props: {[SPACE_PROP_DEFAULT_PAGE_ID]: pageId},
});

const render = (space: Space, pages: Page[]) => {
    const store = makeTestStore({
        docs: {pages: Object.fromEntries(pages.map((page) => [page.id, page]))},
    });

    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                {children}
            </IntlProvider>
        </Provider>
    );

    return renderHook(() => useDefaultPagePath(space), {wrapper}).result.current;
};

describe('useDefaultPagePath', () => {
    beforeEach(() => {
        mockRoute = {isOverview: false};
    });

    it('redirects space home to the default page', () => {
        const home = makePage('home', 'eng', 'Home');

        expect(render(withDefault('home'), [home])).toBe('/docs/eng/home');
    });

    it('stays put when a page is already routed', () => {
        mockRoute = {pageId: 'other', isOverview: false};
        const home = makePage('home', 'eng', 'Home');

        expect(render(withDefault('home'), [home])).toBeUndefined();
    });

    // An explicit /overview request has to win, or the front door is unreachable
    // once a default page is set.
    it('stays put on an explicit overview request', () => {
        mockRoute = {isOverview: true};
        const home = makePage('home', 'eng', 'Home');

        expect(render(withDefault('home'), [home])).toBeUndefined();
    });

    it('ignores a default page that has not loaded', () => {
        expect(render(withDefault('home'), [])).toBeUndefined();
    });

    it('ignores a default page belonging to another space', () => {
        const stray = makePage('home', 'other-space', 'Home');

        expect(render(withDefault('home'), [stray])).toBeUndefined();
    });

    it('does nothing when the space has no default page', () => {
        expect(render(makeSpace('eng', 'Engineering'), [])).toBeUndefined();
    });
});

describe('useCreateRootPage', () => {
    const renderCreate = () => {
        const store = makeTestStore();

        const wrapper = ({children}: {children: React.ReactNode}) => (
            <Provider store={store}>
                <IntlProvider
                    locale='en'
                    messages={{}}
                >
                    {children}
                </IntlProvider>
            </Provider>
        );

        return renderHook(() => useCreateRootPage('eng'), {wrapper}).result;
    };

    beforeEach(() => {
        jest.clearAllMocks();
        mockCreateResult = Promise.resolve(makePage('new', 'eng', 'Untitled'));
    });

    // A new page is empty and titled "Untitled", so landing on it in reading mode
    // means a second click before anything can be written.
    it('opens the new page in edit mode', async () => {
        const create = renderCreate();

        await act(async () => {
            await create.current();
        });

        expect(mockGoToEditPage).toHaveBeenCalledWith('eng', 'new');
        expect(mockGoToPage).not.toHaveBeenCalled();
    });

    it('surfaces a failure without navigating', async () => {
        mockCreateResult = Promise.reject(new Error('nope'));
        jest.spyOn(console, 'error').mockImplementation(() => {});

        const create = renderCreate();

        await act(async () => {
            await create.current();
        });

        expect(toast.error).toHaveBeenCalled();
        expect(mockGoToEditPage).not.toHaveBeenCalled();
    });
});
