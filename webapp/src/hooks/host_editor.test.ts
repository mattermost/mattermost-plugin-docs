// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import type {PublishedWysiwygEditorHandle} from 'webapp_globals';

import {useHostEditor} from './host_editor';

jest.mock('webapp_globals', () => ({hostSupportsDocumentEditor: () => true}));
jest.mock('./caret_anchored_suggestions', () => ({useCaretAnchoredSuggestions: () => undefined}));

const handleFor = (editor: unknown) => ({getEditor: () => editor}) as PublishedWysiwygEditorHandle;

describe('useHostEditor', () => {
    it('is not ready while the page is still loading', () => {
        const editorRef = {current: handleFor({})};

        const {result} = renderHook(() => useHostEditor(editorRef, false));

        expect(result.current.editorReady).toBe(false);
    });

    it('does not look for an editor that is never coming', async () => {
        const getEditor = jest.fn(() => ({}));
        const editorRef = {current: {getEditor} as unknown as PublishedWysiwygEditorHandle};

        renderHook(() => useHostEditor(editorRef, false));

        await new Promise((resolve) => requestAnimationFrame(resolve));

        expect(getEditor).not.toHaveBeenCalled();
    });

    it('waits for an editor that mounts after the page does', async () => {
        const editorRef: {current: PublishedWysiwygEditorHandle | null} = {current: null};

        const {result} = renderHook(() => useHostEditor(editorRef, true));

        expect(result.current.editorReady).toBe(false);

        editorRef.current = handleFor({});

        await waitFor(() => expect(result.current.editorReady).toBe(true));
    });

    it('drops readiness when editing stops', async () => {
        const editorRef = {current: handleFor({})};

        const {result, rerender} = renderHook(({ready}) => useHostEditor(editorRef, ready), {
            initialProps: {ready: true},
        });

        await waitFor(() => expect(result.current.editorReady).toBe(true));

        rerender({ready: false});

        expect(result.current.editorReady).toBe(false);
    });
});
