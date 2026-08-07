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

            const maxLeft = Math.max(0, surface.clientWidth - list.offsetWidth);
            const left = Math.min(Math.max(0, caret.left - surfaceRect.left), maxLeft);

            list.style.bottom = 'auto';
            list.style.top = `${Math.round(flipAbove ? above : below)}px`;
            list.style.left = `${Math.round(left)}px`;
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

        const sync = () => {
            const found = surface.querySelector<HTMLElement>(SELECTOR);
            if (found !== list) {
                list = found;
                if (list) {
                    document.addEventListener('selectionchange', schedule);
                    window.addEventListener('resize', schedule);
                    surface.closest('[data-docs-scroll]')?.addEventListener('scroll', schedule);
                } else {
                    document.removeEventListener('selectionchange', schedule);
                    window.removeEventListener('resize', schedule);
                    surface.closest('[data-docs-scroll]')?.removeEventListener('scroll', schedule);
                }
            }

            if (list) {
                schedule();
            }
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

            sync();
        };

        const observer = new MutationObserver(onMutation);
        observer.observe(surface, {childList: true, subtree: true});
        sync();

        return () => {
            observer.disconnect();
            document.removeEventListener('selectionchange', schedule);
            window.removeEventListener('resize', schedule);
            surface.closest('[data-docs-scroll]')?.removeEventListener('scroll', schedule);
            if (frame) {
                cancelAnimationFrame(frame);
            }
        };
    }, [surfaceRef, enabled]);
};
