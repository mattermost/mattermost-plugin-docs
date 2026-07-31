// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';

const GAP = 4;
const ESTIMATED_HEIGHT = 240;
const SELECTOR = '.suggestion-list';

export const useCaretAnchoredSuggestions = (surfaceRef: React.RefObject<HTMLElement>, enabled: boolean) => {
    useEffect(() => {
        const surface = enabled ? surfaceRef.current : null;
        if (!surface) {
            return undefined;
        }

        let list: HTMLElement | null = null;
        let frame = 0;

        const position = () => {
            const selection = window.getSelection();
            if (!list || !selection || selection.rangeCount === 0) {
                return;
            }

            const range = selection.getRangeAt(0);
            const rect = range.getBoundingClientRect();
            const caret = rect.height === 0 ? (range.startContainer.parentElement?.getBoundingClientRect() ?? rect) : rect;

            const surfaceRect = surface.getBoundingClientRect();
            const height = list.offsetHeight || ESTIMATED_HEIGHT;
            const flipAbove = window.innerHeight - caret.bottom < height && caret.top > height;
            const above = caret.top - surfaceRect.top - (height + GAP);
            const below = (caret.bottom - surfaceRect.top) + GAP;

            list.style.bottom = 'auto';
            list.style.top = `${flipAbove ? above : below}px`;
            list.style.left = `${caret.left - surfaceRect.left}px`;
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

        const onMutation = (records: MutationRecord[]) => {
            let touched = false;
            for (const record of records) {
                for (const node of [...record.addedNodes, ...record.removedNodes]) {
                    if (node.nodeType === globalThis.Node.ELEMENT_NODE) {
                        touched = true;
                        break;
                    }
                }
                if (touched) {
                    break;
                }
            }

            if (!touched) {
                return;
            }

            const found = surface.querySelector<HTMLElement>(SELECTOR);
            if (found !== list) {
                list = found;
                if (list) {
                    document.addEventListener('selectionchange', schedule);
                } else {
                    document.removeEventListener('selectionchange', schedule);
                }
            }

            if (!list) {
                return;
            }
            schedule();
        };

        const observer = new MutationObserver(onMutation);
        observer.observe(surface, {childList: true, subtree: true});

        return () => {
            observer.disconnect();
            document.removeEventListener('selectionchange', schedule);
            if (frame) {
                cancelAnimationFrame(frame);
            }
        };
    }, [surfaceRef, enabled]);
};
