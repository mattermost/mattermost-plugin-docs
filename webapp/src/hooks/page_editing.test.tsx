// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {createRef} from 'react';

import type {EditorContent} from './editor_content';
import {usePageEditing} from './page_editing';

import type {PublishedWysiwygEditorHandle} from '../webapp_globals';

const mockQueue = jest.fn();

let mockContent: EditorContent;

jest.mock('./editor_content', () => ({
    useEditorContent: () => mockContent,
}));

jest.mock('./draft_autosave', () => ({
    useDraftAutosave: () => ({status: 'saved', queue: mockQueue, flush: jest.fn(), cancel: jest.fn()}),
}));

const asStored = '{"content":[{"text":"Hello","type":"text"}],"type":"doc"}';
const asEmitted = '{"type":"doc","content":[{"type":"text","text":"Hello"}]}';
const edited = '{"type":"doc","content":[{"type":"text","text":"Hello there"}]}';

const setup = () => {
    const editorRef = createRef<PublishedWysiwygEditorHandle>();
    return renderHook(() => usePageEditing({spaceId: 'space1', pageId: 'page1', editing: true, editorRef}));
};

beforeEach(() => {
    mockQueue.mockReset();
    mockContent = {
        loading: false,
        error: null,
        title: 'Page',
        body: asStored,
        page: null,
        fromDraft: false,
        notFound: false,
    };
});

describe('usePageEditing', () => {
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
