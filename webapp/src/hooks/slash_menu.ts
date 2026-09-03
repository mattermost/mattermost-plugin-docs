// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Editor} from '@tiptap/core';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import type {BlockRange, InsertBlock} from 'components/page_editor/insert_blocks';
import {INSERT_BLOCKS} from 'components/page_editor/insert_blocks';

export const TRIGGER = '/';

// A leaf (image, horizontal rule) occupies one position in the document, so it has to
// occupy one character here too, or every offset after it is wrong.
const LEAF = '\ufffc';

export type SlashMatch = BlockRange & {query: string};

export const findSlashMatch = (editor: Editor): SlashMatch | null => {
    const {selection, doc} = editor.state;
    if (!selection.empty) {
        return null;
    }

    const start = selection.$from.start();
    const text = doc.textBetween(start, selection.from, undefined, LEAF);
    const trigger = text.lastIndexOf(TRIGGER);

    if (trigger === -1) {
        return null;
    }

    if (trigger > 0 && !(/\s/).test(text[trigger - 1])) {
        return null;
    }

    const query = text.slice(trigger + 1);
    if ((/\s/).test(query)) {
        return null;
    }

    return {from: start + trigger, to: selection.from, query};
};

export const filterBlocks = (query: string, label: (block: InsertBlock) => string): InsertBlock[] => {
    if (!query) {
        return INSERT_BLOCKS;
    }

    const needle = query.toLowerCase();
    return INSERT_BLOCKS.filter((block) => label(block).toLowerCase().includes(needle));
};

type Options = {
    editing: boolean;
    getEditor: () => unknown;
};

export type SlashMenu = {
    open: boolean;
    blocks: InsertBlock[];
    active: number;

    setActive: (index: number) => void;
    select: (index?: number) => void;
    close: () => void;

    // Where the trigger character sits on screen, for anchoring the menu.
    rect: () => DOMRect | null;
};

export const useSlashMenu = ({editing, getEditor}: Options): SlashMenu => {
    const {formatMessage} = useIntl();
    const [match, setMatch] = useState<SlashMatch | null>(null);
    const [active, setActive] = useState(0);

    const dismissed = useRef<number | null>(null);

    const editor = getEditor() as Editor | null;

    const blocks = useMemo(
        () => filterBlocks(match?.query ?? '', (block) => formatMessage(block.title)),
        [match?.query, formatMessage],
    );

    useEffect(() => {
        if (!editor || editor.isDestroyed || !editing) {
            setMatch(null);
            return undefined;
        }

        const sync = () => {
            const found = findSlashMatch(editor);

            if (!found || found.from !== dismissed.current) {
                dismissed.current = null;
            }

            setMatch(found && found.from === dismissed.current ? null : found);
            setActive(0);
        };

        sync();
        editor.on('transaction', sync);
        return () => {
            editor.off('transaction', sync);
        };
    }, [editor, editing]);

    const close = useCallback(() => {
        dismissed.current = match?.from ?? null;
        setMatch(null);
    }, [match?.from]);

    const select = useCallback((index?: number) => {
        const block = blocks[index ?? active];
        if (!editor || editor.isDestroyed || !match || !block) {
            return;
        }

        setMatch(null);
        block.insert(editor, {from: match.from, to: match.to});
    }, [blocks, active, editor, match]);

    const rect = useCallback(() => {
        if (!editor || editor.isDestroyed || !match) {
            return null;
        }

        const {top, bottom, left} = editor.view.coordsAtPos(match.from);
        return new DOMRect(left, top, 0, bottom - top);
    }, [editor, match]);

    return {
        open: Boolean(match) && blocks.length > 0,
        blocks,
        active: Math.min(active, Math.max(blocks.length - 1, 0)),
        setActive,
        select,
        close,
        rect,
    };
};
