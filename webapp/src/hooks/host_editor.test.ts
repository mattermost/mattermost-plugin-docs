// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, waitFor} from '@testing-library/react';
import type {PublishedWysiwygEditorHandle} from 'webapp_globals';

import {useHostEditor} from './host_editor';

jest.mock('webapp_globals', () => ({
    hostSupportsDocumentEditor: (handle: {getEditor?: unknown} | null) => typeof handle?.getEditor === 'function',
}));
jest.mock('./caret_anchored_suggestions', () => ({useCaretAnchoredSuggestions: () => undefined}));

const handleFor = (editor: unknown) => ({getEditor: () => editor}) as PublishedWysiwygEditorHandle;

describe('useHostEditor', () => {
    it('is not ready while the page is still loading', () => {
        const editorRef = {current: handleFor({})};

        const {result} = renderHook(() => useHostEditor(editorRef, {editing: true, loaded: false}));

        expect(result.current.editorReady).toBe(false);
    });

    it('does not look for an editor that is never coming', async () => {
        const getEditor = jest.fn(() => ({}));
        const editorRef = {current: {getEditor} as unknown as PublishedWysiwygEditorHandle};

        renderHook(() => useHostEditor(editorRef, {editing: true, loaded: false}));

        await new Promise((resolve) => requestAnimationFrame(resolve));

        expect(getEditor).not.toHaveBeenCalled();
    });

    it('waits for an editor that mounts after the page does', async () => {
        const editorRef: {current: PublishedWysiwygEditorHandle | null} = {current: null};

        const {result} = renderHook(() => useHostEditor(editorRef, {editing: true, loaded: true}));

        expect(result.current.editorReady).toBe(false);

        editorRef.current = handleFor({});

        await waitFor(() => expect(result.current.editorReady).toBe(true));
    });

    it('waits for the handle before calling a host legacy', async () => {
        const editorRef: {current: PublishedWysiwygEditorHandle | null} = {current: null};

        const {result} = renderHook(() => useHostEditor(editorRef, {editing: true, loaded: true}));

        expect(result.current.documentMode).toBeNull();

        editorRef.current = handleFor({});

        await waitFor(() => expect(result.current.documentMode).toBe(true));
    });

    it('settles on legacy for a handle that has no editor to give', async () => {
        const editorRef = {current: {} as PublishedWysiwygEditorHandle};

        const {result} = renderHook(() => useHostEditor(editorRef, {editing: true, loaded: true}));

        await waitFor(() => expect(result.current.documentMode).toBe(false));
        expect(result.current.editorReady).toBe(false);
    });

    it('drops readiness when editing stops', async () => {
        const editorRef = {current: handleFor({})};

        const {result, rerender} = renderHook(({editing}) => useHostEditor(editorRef, {editing, loaded: true}), {
            initialProps: {editing: true},
        });

        await waitFor(() => expect(result.current.editorReady).toBe(true));

        rerender({editing: false});

        expect(result.current.editorReady).toBe(false);
        expect(result.current.documentMode).toBeNull();
    });
});
