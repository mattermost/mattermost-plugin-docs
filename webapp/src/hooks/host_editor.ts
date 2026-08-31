// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {hostSupportsDocumentEditor} from 'webapp_globals';
import type {PublishedFormattingBarHandle, PublishedMarkdownMode, PublishedWysiwygEditorHandle} from 'webapp_globals';

import {applyWysiwygFormatting} from 'components/page_editor/apply_formatting';

import {useCaretAnchoredSuggestions} from './caret_anchored_suggestions';

export type EditorRef = React.MutableRefObject<PublishedWysiwygEditorHandle | null>;

export type HostEditor = {
    formattingBarRef: React.MutableRefObject<PublishedFormattingBarHandle | null>;
    surfaceRef: React.RefObject<HTMLDivElement>;

    getEditor: () => unknown;
    applyFormatting: (mode: PublishedMarkdownMode) => void;
    editorReady: boolean;

    documentMode: boolean | null;
};

export function useHostEditor(editorRef: EditorRef, ready: boolean): HostEditor {
    const formattingBarRef = useRef<PublishedFormattingBarHandle | null>(null);
    const surfaceRef = useRef<HTMLDivElement>(null);

    const [documentMode, setDocumentMode] = useState<boolean | null>(null);
    const [editorReady, setEditorReady] = useState(false);

    useCaretAnchoredSuggestions(surfaceRef, ready);

    useEffect(() => {
        if (!ready) {
            setEditorReady(false);
            return undefined;
        }

        let frame = 0;
        const look = () => {
            if (editorRef.current?.getEditor?.()) {
                setEditorReady(true);
                return;
            }
            frame = requestAnimationFrame(look);
        };
        look();

        return () => cancelAnimationFrame(frame);
    }, [editorRef, ready]);

    useEffect(() => {
        if (!ready) {
            return;
        }
        setDocumentMode(hostSupportsDocumentEditor(editorRef.current));
    }, [editorRef, ready]);

    const getEditor = useCallback(() => editorRef.current?.getEditor?.() ?? null, [editorRef]);

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

    return {formattingBarRef, surfaceRef, getEditor, applyFormatting, editorReady, documentMode};
}
