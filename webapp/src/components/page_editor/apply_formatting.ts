// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Editor} from '@tiptap/core';
import type {PublishedMarkdownMode} from 'webapp_globals';

// These commands come from the host's StarterKit extensions, which are not
// dependencies here, so the chain they extend is typed structurally.
type FormattingChain = {
    toggleBold: () => FormattingChain;
    toggleItalic: () => FormattingChain;
    toggleStrike: () => FormattingChain;
    toggleHeading: (attrs: {level: number}) => FormattingChain;
    toggleCodeBlock: () => FormattingChain;
    toggleBlockquote: () => FormattingChain;
    toggleBulletList: () => FormattingChain;
    toggleOrderedList: () => FormattingChain;
    run: () => boolean;
};

const selectWordUnderCaret = (editor: Editor) => {
    const {$from} = editor.state.selection;
    const text = $from.parent.textContent;
    const offset = $from.parentOffset;
    if (!text || offset < 0) {
        return;
    }

    const isWordChar = /\S/;
    let start = offset;
    while (start > 0 && isWordChar.test(text[start - 1])) {
        start--;
    }
    let end = offset;
    while (end < text.length && isWordChar.test(text[end])) {
        end++;
    }

    if (start < end) {
        const parentStart = $from.pos - offset;
        editor.chain().focus().setTextSelection({from: parentStart + start, to: parentStart + end}).run();
    }
};

export function applyWysiwygFormatting(editor: Editor, mode: PublishedMarkdownMode): void {
    if (mode === 'bold' || mode === 'italic' || mode === 'strike') {
        if (editor.state.selection.empty) {
            selectWordUnderCaret(editor);
        }
    }

    const chain = editor.chain().focus() as unknown as FormattingChain;
    switch (mode) {
    case 'bold':
        chain.toggleBold().run();
        break;
    case 'italic':
        chain.toggleItalic().run();
        break;
    case 'strike':
        chain.toggleStrike().run();
        break;
    case 'heading':
        chain.toggleHeading({level: 3}).run();
        break;
    case 'code':
        chain.toggleCodeBlock().run();
        break;
    case 'quote':
        chain.toggleBlockquote().run();
        break;
    case 'ul':
        chain.toggleBulletList().run();
        break;
    case 'ol':
        chain.toggleOrderedList().run();
        break;
    }
}
