// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import {getPageDraft} from 'client/drafts';
import {getPage} from 'client/pages';
import {RestError} from 'client/rest';

import type {Page} from 'types/docs';
import type {Draft} from 'types/drafts';

import {usePageDraft} from './page_draft';

jest.mock('client/drafts', () => ({
    getPageDraft: jest.fn(),
}));

jest.mock('client/pages', () => ({
    getPage: jest.fn(),
}));

const mockGetDraft = getPageDraft as jest.MockedFunction<typeof getPageDraft>;
const mockGetPage = getPage as jest.MockedFunction<typeof getPage>;

const page = (id: string, editAt: number) => ({id, title: `${id} title`, body: `${id} body`, edit_at: editAt} as Page);
const draft = (pageId: string, baseEditAt: number) => ({page_id: pageId, title: `${pageId} draft`, body: `${pageId} draft body`, base_edit_at: baseEditAt} as Draft);

const notFound = () => Promise.reject(new RestError('/pages', 404, 'not found', null));

beforeEach(() => {
    mockGetDraft.mockReset();
    mockGetPage.mockReset();
});

describe('usePageDraft', () => {
    it('uses the draft baseline rather than the page edit_at when a draft exists', async () => {
        mockGetDraft.mockResolvedValue(draft('page1', 100));
        mockGetPage.mockResolvedValue(page('page1', 500));

        const {result} = renderHook(() => usePageDraft('space1', 'page1'));

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.baseEditAt).toBe(100);
    });

    it('falls back to the page edit_at when no draft exists', async () => {
        mockGetDraft.mockImplementation(notFound);
        mockGetPage.mockResolvedValue(page('page1', 500));

        const {result} = renderHook(() => usePageDraft('space1', 'page1'));

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.baseEditAt).toBe(500);
    });

    it('reports loading on the first render after the page id changes', async () => {
        mockGetDraft.mockImplementation(notFound);
        mockGetPage.mockImplementation((_spaceId, pageId) => Promise.resolve(page(pageId, 500)));

        const {result, rerender} = renderHook(({pageId}) => usePageDraft('space1', pageId), {
            initialProps: {pageId: 'page1'},
        });

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.body).toBe('page1 body');

        rerender({pageId: 'page2'});

        expect(result.current.loading).toBe(true);
        expect(result.current.body).toBe('');
        expect(result.current.baseEditAt).toBeUndefined();

        await waitFor(() => expect(result.current.body).toBe('page2 body'));
    });
});
