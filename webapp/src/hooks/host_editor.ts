// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {hostSupportsDocumentEditor} from 'webapp_globals';
import type {PublishedFormattingBarHandle, PublishedMarkdownMode, PublishedWysiwygEditorHandle} from 'webapp_globals';

import {applyWysiwygFormatting} from 'components/page_editor/apply_formatting';

import {useCaretAnchoredSuggestions} from './caret_anchored_suggestions';

export type EditorRef = React.MutableRefObject<PublishedWysiwygEditorHandle | null>;

export type HostEditorState = {
    editing: boolean;
    loaded: boolean;
};

export type HostEditor = {
    formattingBarRef: React.MutableRefObject<PublishedFormattingBarHandle | null>;
    surfaceRef: React.RefObject<HTMLDivElement>;

    getEditor: () => unknown;
    applyFormatting: (mode: PublishedMarkdownMode) => void;

    documentMode: boolean | null;
};

export function useHostEditor(editorRef: EditorRef, {editing, loaded}: HostEditorState): HostEditor {
    const formattingBarRef = useRef<PublishedFormattingBarHandle | null>(null);
    const surfaceRef = useRef<HTMLDivElement>(null);

    const [documentMode, setDocumentMode] = useState<boolean | null>(null);

    const ready = editing && loaded;

    const getEditor = useCallback(() => editorRef.current?.getEditor?.() ?? null, [editorRef]);

    useCaretAnchoredSuggestions(surfaceRef, {editing, loaded, getEditor});

    useEffect(() => {
        if (!ready) {
            return;
        }
        setDocumentMode(hostSupportsDocumentEditor(editorRef.current));
    }, [editorRef, ready]);

    const applyFormatting = useCallback((mode: PublishedMarkdownMode) => {
        const editor = getEditor() as Parameters<typeof applyWysiwygFormatting>[0] | null;
        if (!editor || editor.isDestroyed) {
            return;
        }
        if (mode === 'link') {
            formattingBarRef.current?.openLinkPopover();
            return;
        }
        applyWysiwygFormatting(editor, mode);
    }, [getEditor]);

    return {formattingBarRef, surfaceRef, getEditor, applyFormatting, documentMode};
}
