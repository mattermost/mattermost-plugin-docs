// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {sameContent} from 'utils/content';

import type {DraftAutosave} from './draft_autosave';
import {useDraftAutosave} from './draft_autosave';
import type {EditorContent} from './editor_content';
import {useEditorContent} from './editor_content';
import type {EditorRef} from './host_editor';

type Options = {
    spaceId: string;
    pageId: string;
    editing: boolean;
    editorRef: EditorRef;
};

export type PageEditing = {
    load: EditorContent;
    autosave: DraftAutosave;

    contentError: boolean;
    actionError: unknown;

    onContentChange: (content: string) => void;
    onContentError: () => void;
};

export function usePageEditing({spaceId, pageId, editing, editorRef}: Options): PageEditing {
    const load = useEditorContent(spaceId, pageId);

    const [contentError, setContentError] = useState(false);
    const [actionError, setActionError] = useState<unknown>(null);
    const [baseEditAt, setBaseEditAt] = useState<number | undefined>(undefined);

    const autosave = useDraftAutosave({
        spaceId,
        pageId,
        enabled: editing && !load.loading && !load.error && !contentError,
        baseEditAt,
        onError: setActionError,
    });

    useEffect(() => {
        setBaseEditAt(load.baseEditAt);
    }, [load.baseEditAt]);

    useEffect(() => {
        setContentError(false);
        setActionError(null);
    }, [spaceId, pageId]);

    const savedBody = useRef(load.body);
    useEffect(() => {
        savedBody.current = load.body;
    }, [load.body]);

    const onContentChange = useCallback((content: string) => {
        if (contentError || editorRef.current?.hasContentError?.()) {
            setContentError(true);
            return;
        }

        if (sameContent(content, savedBody.current)) {
            return;
        }

        savedBody.current = content;
        autosave.queue({body: content});
    }, [autosave, contentError, editorRef]);

    const onContentError = useCallback(() => {
        setContentError(true);
    }, []);

    return {load, autosave, contentError, actionError, onContentChange, onContentError};
}
