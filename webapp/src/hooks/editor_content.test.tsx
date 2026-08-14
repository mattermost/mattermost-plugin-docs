// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import React from 'react';
import {Provider} from 'react-redux';

import type {Page} from 'types/docs';
import type {Draft} from 'types/drafts';

import {useEditorContent} from './editor_content';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockFetchDraft = jest.fn();
const mockFetchPage = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    fetchPageDraft: (...args: unknown[]) => () => mockFetchDraft(...args as []),
    fetchPage: (...args: unknown[]) => () => mockFetchPage(...args as []),
}));

const page = (id: string, editAt: number) => ({id, title: `${id} title`, body: `${id} body`, edit_at: editAt} as Page);
const draft = (pageId: string, baseEditAt: number) => ({page_id: pageId, title: `${pageId} draft`, body: `${pageId} draft body`, base_edit_at: baseEditAt} as Draft);

const wrapper = ({children}: {children: React.ReactNode}) => (
    <Provider store={makeTestStore()}>{children}</Provider>
);

const renderPageDraft = (pageId = 'page1') => renderHook(
    (props: {pageId: string}) => useEditorContent('space1', props.pageId),
    {initialProps: {pageId}, wrapper},
);

beforeEach(() => {
    mockFetchDraft.mockReset();
    mockFetchPage.mockReset();
});

describe('useEditorContent', () => {
    it('uses the draft baseline rather than the page edit_at when a draft exists', async () => {
        mockFetchDraft.mockResolvedValue(draft('page1', 100));
        mockFetchPage.mockResolvedValue(page('page1', 500));

        const {result} = renderPageDraft();

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.baseEditAt).toBe(100);
    });

    it('falls back to the page edit_at when no draft exists', async () => {
        mockFetchDraft.mockResolvedValue(undefined);
        mockFetchPage.mockResolvedValue(page('page1', 500));

        const {result} = renderPageDraft();

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.baseEditAt).toBe(500);
    });

    it('reports the page as missing when neither a draft nor a page comes back', async () => {
        mockFetchDraft.mockResolvedValue(undefined);
        mockFetchPage.mockResolvedValue(undefined);

        const {result} = renderPageDraft();

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.notFound).toBe(true);
    });

    it('surfaces a draft read that failed rather than opening on the page', async () => {
        mockFetchDraft.mockRejectedValue(new Error('boom'));
        mockFetchPage.mockResolvedValue(page('page1', 500));

        const {result} = renderPageDraft();

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.error).toBeInstanceOf(Error);
        expect(result.current.body).toBe('');
    });

    it('reports loading on the first render after the page id changes', async () => {
        mockFetchDraft.mockResolvedValue(undefined);
        mockFetchPage.mockImplementation((_spaceId: string, pageId: string) => Promise.resolve(page(pageId, 500)));

        const {result, rerender} = renderPageDraft();

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.body).toBe('page1 body');

        rerender({pageId: 'page2'});

        expect(result.current.loading).toBe(true);
        expect(result.current.body).toBe('');
        expect(result.current.baseEditAt).toBeUndefined();

        await waitFor(() => expect(result.current.body).toBe('page2 body'));
    });
});
