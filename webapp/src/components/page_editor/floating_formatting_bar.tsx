// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {autoUpdate, flip, inline, offset, shift, useFloating} from '@floating-ui/react';
import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {hostGetEditor} from 'webapp_globals';
import type {PublishedFormattingBarHandle, PublishedMarkdownMode} from 'webapp_globals';

import styles from './floating_formatting_bar.module.scss';

const GAP = 8;
const TRACK_WIDTH = 660;
const TRAILING_PAD = 7;

const contentWidth = (container: HTMLElement): number | null => {
    const laidOut = Array.from(container.children).
        filter((child) => window.getComputedStyle(child).position !== 'absolute');
    if (laidOut.length === 0) {
        return null;
    }

    const left = container.getBoundingClientRect().left;
    const right = Math.max(...laidOut.map((child) => child.getBoundingClientRect().right));
    return Math.ceil(right - left) + TRAILING_PAD;
};

type Props = {
    editorRef: React.RefObject<HTMLElement>;
    applyFormatting: (mode: PublishedMarkdownMode) => void;
    getEditor: () => unknown;
    barRef: React.Ref<PublishedFormattingBarHandle>;
    additionalControls?: React.ReactNode[];
};

const FloatingFormattingBar = ({editorRef, applyFormatting, getEditor, barRef, additionalControls}: Props) => {
    const wrapperRef = useRef<HTMLDivElement | null>(null);
    const interactingRef = useRef(false);
    const rangeRef = useRef<Range | null>(null);
    const [open, setOpen] = useState(false);
    const [editorEl, setEditorEl] = useState<HTMLElement | null>(null);
    const trackRef = useRef<HTMLDivElement | null>(null);
    const [width, setWidth] = useState<number | null>(null);

    useEffect(() => setEditorEl(editorRef.current), [editorRef]);

    const middleware = useMemo(() => [
        inline(),
        offset(GAP),
        flip({boundary: editorEl?.closest('[data-docs-scroll]') ?? undefined, padding: GAP}),
        shift({boundary: editorEl ?? undefined, padding: 0}),
    ], [editorEl]);

    const {refs, floatingStyles, update} = useFloating({
        open,
        placement: 'top',
        strategy: 'absolute',
        middleware,
        whileElementsMounted: autoUpdate,
    });

    const {setReference, setFloating} = refs;

    const reference = useMemo(() => ({
        contextElement: editorEl ?? undefined,
        getBoundingClientRect: () => rangeRef.current?.getBoundingClientRect() ?? new DOMRect(),
        getClientRects: () => Array.from(rangeRef.current?.getClientRects() ?? []),
    }), [editorEl]);

    useEffect(() => setReference(reference), [setReference, reference]);

    const setWrapper = useCallback((node: HTMLDivElement | null) => {
        wrapperRef.current = node;
        setFloating(node);
    }, [setFloating]);

    useLayoutEffect(() => {
        const container = trackRef.current?.firstElementChild as HTMLElement | null;
        if (!container) {
            return undefined;
        }

        const measure = () => setWidth((current) => contentWidth(container) ?? current);

        measure();

        const observer = new MutationObserver(measure);
        observer.observe(container, {childList: true});
        return () => observer.disconnect();
    }, [additionalControls]);

    useEffect(() => {
        update();
    }, [width, update]);

    const sync = useCallback(() => {
        const surface = editorRef.current;
        if (!surface || interactingRef.current || wrapperRef.current?.contains(document.activeElement)) {
            return;
        }

        const selection = window.getSelection();
        if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
            setOpen(false);
            return;
        }

        const range = selection.getRangeAt(0);
        const rect = range.getBoundingClientRect();
        if (!surface.contains(range.commonAncestorContainer) || (rect.width === 0 && rect.height === 0)) {
            setOpen(false);
            return;
        }

        rangeRef.current = range;
        setOpen(true);
        update();
    }, [editorRef, update]);

    useEffect(() => {
        document.addEventListener('selectionchange', sync);
        return () => document.removeEventListener('selectionchange', sync);
    }, [sync]);

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
            ref={setWrapper}
            className={open ? styles.bar : `${styles.bar} ${styles.hidden}`}
            style={{...floatingStyles, width: width ?? TRACK_WIDTH}}
            onMouseDown={onMouseDown}
        >
            <div
                ref={trackRef}
                className={styles.track}
                style={{width: TRACK_WIDTH}}
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
        </div>
    );
};

export default FloatingFormattingBar;
