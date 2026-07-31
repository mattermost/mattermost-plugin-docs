// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {hostGetEditor} from 'webapp_globals';
import type {PublishedFormattingBarHandle, PublishedMarkdownMode} from 'webapp_globals';

import styles from './floating_formatting_bar.module.scss';

const GAP = 8;

const boundaryTop = (editorEl: HTMLElement): number => {
    const scroller = editorEl.closest('[data-docs-scroll]');
    return scroller ? scroller.getBoundingClientRect().top : 0;
};

type Props = {
    editorRef: React.RefObject<HTMLElement>;
    applyFormatting: (mode: PublishedMarkdownMode) => void;
    getEditor: () => unknown;
    barRef: React.Ref<PublishedFormattingBarHandle>;
    additionalControls?: React.ReactNode[];
};

const FloatingFormattingBar = ({editorRef, applyFormatting, getEditor, barRef, additionalControls}: Props) => {
    const wrapperRef = useRef<HTMLDivElement>(null);
    const interactingRef = useRef(false);
    const [position, setPosition] = useState<{top: number; left: number} | null>(null);

    const reposition = useCallback(() => {
        const wrapper = wrapperRef.current;
        const editorEl = editorRef.current;
        if (!wrapper || !editorEl) {
            return;
        }

        if (interactingRef.current || wrapper.contains(document.activeElement)) {
            return;
        }

        const selection = window.getSelection();
        if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
            setPosition(null);
            return;
        }

        const range = selection.getRangeAt(0);
        if (!editorEl.contains(range.commonAncestorContainer)) {
            setPosition(null);
            return;
        }

        const rect = range.getClientRects()[0] ?? range.getBoundingClientRect();
        if (rect.width === 0 && rect.height === 0) {
            setPosition(null);
            return;
        }

        const originRect = editorEl.getBoundingClientRect();
        const {offsetWidth, offsetHeight} = wrapper;
        const selectionCenter = rect.left + (rect.width / 2);
        const centered = selectionCenter - originRect.left - (offsetWidth / 2);
        const maxLeft = Math.max(0, editorEl.clientWidth - offsetWidth);

        const flipBelow = rect.top - offsetHeight - GAP < boundaryTop(editorEl);
        const top = flipBelow ? (rect.bottom - originRect.top) + GAP : rect.top - originRect.top - offsetHeight - GAP;

        setPosition({
            top: Math.round(top),
            left: Math.round(Math.min(Math.max(0, centered), maxLeft)),
        });
    }, [editorRef]);

    const frameRef = useRef(0);
    const schedule = useCallback(() => {
        if (frameRef.current) {
            return;
        }
        frameRef.current = requestAnimationFrame(() => {
            frameRef.current = 0;
            reposition();
        });
    }, [reposition]);

    useEffect(() => {
        document.addEventListener('selectionchange', schedule);
        window.addEventListener('resize', schedule);
        return () => {
            document.removeEventListener('selectionchange', schedule);
            window.removeEventListener('resize', schedule);
            if (frameRef.current) {
                cancelAnimationFrame(frameRef.current);
                frameRef.current = 0;
            }
        };
    }, [schedule]);

    const onMouseDown = useCallback((e: React.MouseEvent) => {
        interactingRef.current = true;
        if (!(e.target as HTMLElement).closest('input, textarea')) {
            e.preventDefault();
        }
    }, []);

    useEffect(() => {
        const release = () => {
            interactingRef.current = false;
        };
        document.addEventListener('mouseup', release);
        return () => document.removeEventListener('mouseup', release);
    }, []);

    const {FormattingBar} = hostGetEditor() ?? {};
    if (!FormattingBar) {
        return null;
    }

    return (
        <div
            ref={wrapperRef}
            className={position ? styles.bar : `${styles.bar} ${styles.hidden}`}
            style={position ? {top: position.top, left: position.left} : undefined}
            onMouseDown={onMouseDown}
        >
            <FormattingBar
                ref={barRef}
                applyFormatting={applyFormatting}
                disableControls={false}
                location='docs'
                getEditor={getEditor}
                additionalControls={additionalControls}
            />
        </div>
    );
};

export default FloatingFormattingBar;
