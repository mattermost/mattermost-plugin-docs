// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, renderHook, screen, waitFor} from '@testing-library/react';
import {PublishConflictError} from 'data/publish_conflict';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {makeDraft, makePage} from 'store/test_fixtures';

import {DocsModalController, closeAllDocsModals} from 'components/modals';
import {getDocsModalStack} from 'components/modals/modal_store';
import {toast} from 'components/toast';

import type {Page} from 'types/docs';

import {usePageDraft, usePublishDraft} from './drafts';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockFetchPageDraft = jest.fn();
const mockPublishDraft = jest.fn();
let mockFetchResult: Promise<unknown> = Promise.resolve(undefined);
let mockPublishResult: Promise<Page> = Promise.resolve(makePage('new', 'eng', 'New'));

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    fetchPageDraft: (...args: unknown[]) => {
        mockFetchPageDraft(...args as []);
        return async () => mockFetchResult;
    },
    publishDraft: (...args: unknown[]) => {
        mockPublishDraft(...args as []);
        return async () => mockPublishResult;
    },
}));

const mockGoToPage = jest.fn();

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({goToPage: mockGoToPage}),
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

// The controller is mounted so the publish-conflict modal, which is opened
// imperatively rather than rendered by the hook's caller, has somewhere to land.
const wrapperWith = (store: ReturnType<typeof makeTestStore>) =>
    ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                {children}
                <DocsModalController/>
            </IntlProvider>
        </Provider>
    );

beforeEach(() => {
    jest.clearAllMocks();
    mockFetchResult = Promise.resolve(undefined);
    mockPublishResult = Promise.resolve(makePage('new', 'eng', 'New'));
});

afterEach(() => act(() => {
    closeAllDocsModals();
}));

describe('usePageDraft', () => {
    const renderDraft = (spaceId?: string, pageId?: string, drafts = {}) => {
        const store = makeTestStore({docs: {drafts}});
        return renderHook(
            ({currentSpaceId, currentPageId}: {currentSpaceId?: string; currentPageId?: string}) =>
                usePageDraft(currentSpaceId, currentPageId),
            {
                initialProps: {currentSpaceId: spaceId, currentPageId: pageId},
                wrapper: wrapperWith(store),
            },
        );
    };

    it('reads the draft for the routed page out of the store', async () => {
        const draft = makeDraft('new', 'eng', 'Unpublished');
        const {result} = renderDraft('eng', 'new', {new: draft});

        await waitFor(() => expect(result.current.loaded).toBe(true));
        expect(result.current.draft).toEqual(draft);
        expect(mockFetchPageDraft).toHaveBeenCalledWith('eng', 'new', expect.anything());
    });

    it('does not fetch without both ids', () => {
        const {result} = renderDraft(undefined, undefined);

        expect(mockFetchPageDraft).not.toHaveBeenCalled();
        expect(result.current.loaded).toBe(false);
    });

    // A URL naming no draft has to be distinguishable from one still loading, or the
    // caller corrects it before the answer is in.
    it('reports loaded with no draft when the page has none', async () => {
        const {result} = renderDraft('eng', 'missing');

        await waitFor(() => expect(result.current.loaded).toBe(true));
        expect(result.current.draft).toBeUndefined();
    });

    it('settles as loaded when the fetch fails, rather than retrying forever', async () => {
        mockFetchResult = Promise.reject(new Error('nope'));
        jest.spyOn(console, 'error').mockImplementation(() => {});

        const {result} = renderDraft('eng', 'new');

        await waitFor(() => expect(result.current.loaded).toBe(true));
    });

    it('invalidates loaded state when leaving and returning to a page', async () => {
        const draft = makeDraft('new', 'eng', 'Unpublished');
        const hook = renderDraft('eng', 'new', {new: draft});
        await waitFor(() => expect(hook.result.current.loaded).toBe(true));

        hook.rerender({currentSpaceId: undefined, currentPageId: undefined});
        await waitFor(() => expect(hook.result.current.loaded).toBe(false));

        let resolve: () => void = () => {};
        mockFetchResult = new Promise<void>((done) => {
            resolve = done;
        });
        hook.rerender({currentSpaceId: 'eng', currentPageId: 'new'});

        expect(hook.result.current.loaded).toBe(false);
        await act(async () => resolve());
        await waitFor(() => expect(hook.result.current.loaded).toBe(true));
    });
});

describe('usePublishDraft', () => {
    const renderPublish = () => {
        const store = makeTestStore();
        return renderHook(() => usePublishDraft('eng'), {wrapper: wrapperWith(store)}).result;
    };

    // Publishing destroys the draft, so its URL is dead — Back must not return to it.
    it('routes to the published page, replacing the draft URL', async () => {
        const publish = renderPublish();

        await act(async () => {
            await publish.current('new');
        });

        expect(mockPublishDraft).toHaveBeenCalledWith('eng', 'new', false);
        expect(mockGoToPage).toHaveBeenCalledWith('eng', 'new', {replace: true});
    });

    // Retrying a conflict reissues the same refused request, so the user is offered
    // the overwrite instead of a message that can never come true.
    it('offers to publish anyway when the page changed underneath the draft', async () => {
        const conflict = new PublishConflictError({
            error: {id: 'page_changed', message: 'conflict', status_code: 409},
            current_page: makePage('new', 'eng', 'Their version'),
        });
        mockPublishResult = Promise.reject(conflict);

        const publish = renderPublish();

        await act(async () => {
            await publish.current('new');
        });

        expect(await screen.findByText('Publish anyway')).toBeInTheDocument();
        expect(toast.error).not.toHaveBeenCalled();

        mockPublishResult = Promise.resolve(makePage('new', 'eng', 'Mine'));

        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Publish anyway'}));
        });

        await waitFor(() => expect(mockPublishDraft).toHaveBeenCalledWith('eng', 'new', true));
        await waitFor(() => expect(mockGoToPage).toHaveBeenCalledWith('eng', 'new', {replace: true}));

        // The modal runs the confirm handler instead of its own onClose, so a
        // confirm that forgets to close leaves an entry on the stack forever.
        await waitFor(() => expect(getDocsModalStack()).toHaveLength(0));
    });

    it('leaves the draft alone when the conflict is dismissed', async () => {
        mockPublishResult = Promise.reject(new PublishConflictError({
            error: {id: 'page_changed', message: 'conflict', status_code: 409},
            current_page: makePage('new', 'eng', 'Their version'),
        }));

        const publish = renderPublish();

        await act(async () => {
            await publish.current('new');
        });

        await act(async () => {
            fireEvent.click(await screen.findByRole('button', {name: 'Cancel'}));
        });

        expect(mockPublishDraft).toHaveBeenCalledTimes(1);
        expect(mockGoToPage).not.toHaveBeenCalled();
    });

    // force cannot fix an unpublished parent, so this conflict gets the instruction
    // that helps instead of a generic retry message.
    it('explains an unpublished parent instead of failing generically', async () => {
        mockPublishResult = Promise.reject(new PublishConflictError({
            error: {id: 'parent_unpublished', message: 'nope', status_code: 409},
            current_page: null,
        }));
        jest.spyOn(console, 'error').mockImplementation(() => {});

        const publish = renderPublish();

        await act(async () => {
            await publish.current('new');
        });

        expect(toast.error).toHaveBeenCalled();
        expect(mockGoToPage).not.toHaveBeenCalled();
        expect(console.error).not.toHaveBeenCalled();
    });

    it('reports any other failure and stays put', async () => {
        mockPublishResult = Promise.reject(new Error('boom'));
        jest.spyOn(console, 'error').mockImplementation(() => {});

        const publish = renderPublish();

        await act(async () => {
            await publish.current('new');
        });

        expect(toast.error).toHaveBeenCalled();
        expect(mockGoToPage).not.toHaveBeenCalled();
    });
});
