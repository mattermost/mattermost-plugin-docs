// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {createRef} from 'react';

import type {Draft} from 'types/drafts';

import type {EditorContent} from './editor_content';
import {clearOwnPageWrites, recordOwnPageWrite} from './own_page_writes';
import {usePageEditing} from './page_editing';

import type {PublishedWysiwygEditorHandle} from '../webapp_globals';

let mockContent: EditorContent;
let mockDraft: Draft | undefined;

jest.mock('./editor_content', () => ({
    useEditorContent: () => mockContent,
}));

jest.mock('./redux', () => ({
    useAppSelector: (selector: (state: unknown) => unknown) => selector({}),
}));

jest.mock('store/selectors', () => ({
    getDraftForPage: () => mockDraft,
}));

const autosaveOptions: Array<{baseEditAt?: number}> = [];

jest.mock('./draft_autosave', () => ({
    useDraftAutosave: (options: {baseEditAt?: number}) => {
        autosaveOptions.push(options);
        return {status: 'saved', queue: jest.fn(), flush: jest.fn(), cancel: jest.fn()};
    },
}));

const baselineSent = () => autosaveOptions[autosaveOptions.length - 1].baseEditAt;

const setup = () => {
    const editorRef = createRef<PublishedWysiwygEditorHandle>();
    return renderHook(() => usePageEditing({spaceId: 'space1', pageId: 'page1', editing: true, editorRef}));
};

beforeEach(() => {
    autosaveOptions.length = 0;
    mockDraft = undefined;
    clearOwnPageWrites();
    mockContent = {
        loading: false,
        error: null,
        title: 'Page',
        body: '{"type":"doc"}',
        page: null,
        fromDraft: false,
        notFound: false,
        baseEditAt: 100,
    };
});

describe('usePageEditing', () => {
    it('writes against the version the page loaded with', () => {
        setup();

        expect(baselineSent()).toBe(100);
    });

    it('writes against the version our own publish produced', () => {
        const {rerender} = setup();

        act(() => recordOwnPageWrite('page1', 250));
        rerender();

        expect(baselineSent()).toBe(250);
    });

    it('leaves a draft on the baseline it opened with', () => {
        mockDraft = {base_edit_at: 100} as Draft;
        const {rerender} = setup();

        act(() => recordOwnPageWrite('page1', 250));
        rerender();

        expect(baselineSent()).toBe(100);
    });

    it('ignores a version another page produced', () => {
        const {rerender} = setup();

        act(() => recordOwnPageWrite('page2', 250));
        rerender();

        expect(baselineSent()).toBe(100);
    });
});
