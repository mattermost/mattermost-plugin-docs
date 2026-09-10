// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {createRef} from 'react';

import type {Draft} from 'types/drafts';

import type {EditorContent} from './editor_content';
import {clearOwnPageWrites, recordOwnPageWrite} from './own_page_writes';
import {usePageEditing} from './page_editing';

import type {PublishedWysiwygEditorHandle} from '../webapp_globals';

const mockQueue = jest.fn();

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
        return {status: 'saved', queue: mockQueue, flush: jest.fn(), cancel: jest.fn()};
    },
}));

const baselineSent = () => autosaveOptions[autosaveOptions.length - 1].baseEditAt;

const asStored = '{"content":[{"text":"Hello","type":"text"}],"type":"doc"}';
const asEmitted = '{"type":"doc","content":[{"type":"text","text":"Hello"}]}';
const edited = '{"type":"doc","content":[{"type":"text","text":"Hello there"}]}';

const setup = () => {
    const editorRef = createRef<PublishedWysiwygEditorHandle>();
    return renderHook(() => usePageEditing({spaceId: 'space1', pageId: 'page1', editing: true, editorRef}));
};

beforeEach(() => {
    autosaveOptions.length = 0;
    mockDraft = undefined;
    clearOwnPageWrites();
    mockQueue.mockReset();
    mockContent = {
        loading: false,
        error: null,
        title: 'Page',
        body: asStored,
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

    it('ignores a change that is the loaded content in another key order', () => {
        const {result} = setup();

        act(() => result.current.onContentChange(asEmitted));

        expect(mockQueue).not.toHaveBeenCalled();
    });

    it('saves a real edit', () => {
        const {result} = setup();

        act(() => result.current.onContentChange(edited));

        expect(mockQueue).toHaveBeenCalledWith({body: edited});
    });

    it('saves an edit that is undone back to the loaded content', () => {
        const {result} = setup();

        act(() => result.current.onContentChange(edited));
        act(() => result.current.onContentChange(asEmitted));

        expect(mockQueue).toHaveBeenCalledTimes(2);
        expect(mockQueue).toHaveBeenLastCalledWith({body: asEmitted});
    });

    it('does not save the same edit twice', () => {
        const {result} = setup();

        act(() => result.current.onContentChange(edited));
        act(() => result.current.onContentChange(edited));

        expect(mockQueue).toHaveBeenCalledTimes(1);
    });
});
