// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';

const GAP = 4;
const MARGIN = 8;
const MAX_HEIGHT = 320;
const LIST = '.suggestion-list';
const CONTENT = '.suggestion-list__content';

type PretextEditor = {
    isDestroyed?: boolean;
    state?: {
        selection?: {from: number; $from: {start: () => number}};
        doc?: {textBetween: (from: number, to: number, sep: string) => string};
    };
};

const startsCommand = (getEditor: () => unknown): boolean => {
    const editor = getEditor() as PretextEditor | null;
    const selection = editor?.state?.selection;
    const doc = editor?.state?.doc;
    if (!editor || editor.isDestroyed || !selection || !doc) {
        return false;
    }

    try {
        return doc.textBetween(selection.$from.start(), selection.from, '\n').startsWith('/');
    } catch {
        return false;
    }
};

const caretRect = (): DOMRect | null => {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0) {
        return null;
    }

    const range = selection.getRangeAt(0);
    const rect = range.getBoundingClientRect();
    if (rect.height > 0) {
        return rect;
    }

    const start = range.startContainer;
    const candidates: Array<Node | null> = start.nodeType === globalThis.Node.ELEMENT_NODE ? [
        start.childNodes[range.startOffset],
        start.childNodes[range.startOffset - 1],
        start,
    ] : [start.parentElement];

    for (const node of candidates) {
        if (node?.nodeType !== globalThis.Node.ELEMENT_NODE) {
            continue;
        }
        const box = (node as Element).getBoundingClientRect();
        if (box.height > 0) {
            return box;
        }
    }

    return null;
};

type Options = {
    editing: boolean;
    loaded: boolean;

    getEditor: () => unknown;
};

export const useCaretAnchoredSuggestions = (
    surfaceRef: React.RefObject<HTMLElement>,
    {editing, loaded, getEditor}: Options,
) => {
    useEffect(() => {
        const surface = loaded ? surfaceRef.current : null;
        if (!surface) {
            return undefined;
        }

        let list: HTMLElement | null = null;
        let frame = 0;

        const position = () => {
            if (!list) {
                return;
            }

            if (!editing || startsCommand(getEditor)) {
                list.style.display = 'none';
                return;
            }
            list.style.display = '';

            const content = list.querySelector<HTMLElement>(CONTENT);
            const caret = caretRect();
            if (!content || !caret) {
                return;
            }

            const below = window.innerHeight - caret.bottom - GAP - MARGIN;
            const above = caret.top - GAP - MARGIN;
            const wanted = Math.min(MAX_HEIGHT, content.scrollHeight);
            const flip = below < wanted && above > below;

            content.style.position = 'fixed';
            content.style.maxHeight = `${Math.max(0, Math.min(MAX_HEIGHT, flip ? above : below))}px`;
            content.style.left = `${Math.round(Math.max(MARGIN, Math.min(caret.left, window.innerWidth - content.offsetWidth - MARGIN)))}px`;

            if (flip) {
                content.style.top = 'auto';
                content.style.bottom = `${Math.round((window.innerHeight - caret.top) + GAP)}px`;
            } else {
                content.style.bottom = 'auto';
                content.style.top = `${Math.round(caret.bottom + GAP)}px`;
            }
        };

        const schedule = () => {
            if (frame) {
                return;
            }
            frame = requestAnimationFrame(() => {
                frame = 0;
                position();
            });
        };

        const scroller = surface.closest('[data-docs-scroll]');

        const sync = () => {
            const found = surface.querySelector<HTMLElement>(LIST);
            if (found !== list) {
                list = found;
                if (list) {
                    document.addEventListener('selectionchange', schedule);
                    window.addEventListener('resize', schedule);
                    scroller?.addEventListener('scroll', schedule);
                } else {
                    document.removeEventListener('selectionchange', schedule);
                    window.removeEventListener('resize', schedule);
                    scroller?.removeEventListener('scroll', schedule);
                }
            }

            if (list) {
                schedule();
            }
        };

        const onMutation = (records: MutationRecord[]) => {
            for (const record of records) {
                for (const node of [...record.addedNodes, ...record.removedNodes]) {
                    if (node.nodeType === globalThis.Node.ELEMENT_NODE) {
                        sync();
                        return;
                    }
                }
            }
        };

        const observer = new MutationObserver(onMutation);
        observer.observe(surface, {childList: true, subtree: true});
        sync();

        return () => {
            observer.disconnect();
            document.removeEventListener('selectionchange', schedule);
            window.removeEventListener('resize', schedule);
            scroller?.removeEventListener('scroll', schedule);
            if (frame) {
                cancelAnimationFrame(frame);
            }
        };
    }, [surfaceRef, editing, loaded, getEditor]);
};
