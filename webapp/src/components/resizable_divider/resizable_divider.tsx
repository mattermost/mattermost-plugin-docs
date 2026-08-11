// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React, {useEffect, useRef, useState} from 'react';

import styles from './resizable_divider.module.scss';

// Matches core's resizable sidebars (components/resizable_sidebar): a wide
// invisible grab area straddling the edge, with a thin accent line on hover and
// while dragging. Core builds this with styled-components, which the Docs plugin
// can't use, so the same behaviour is reimplemented over CSS Modules.

export type ResizeSide = 'left' | 'right';

type Props = {
    ariaLabel: string;

    /** Which edge of the resized container the handle sits on. */
    side: ResizeSide;
    width: number;
    minWidth: number;
    maxWidth: number;
    defaultWidth: number;

    /** Fires continuously while dragging; keep this cheap (state only). */
    onResize: (width: number) => void;

    /** Fires once when the drag ends — the place to persist the result. */
    onResizeEnd: (width: number) => void;

    /**
     * Pixels to slide the handle outward, off the panel it resizes. Pass this when
     * the panel scrolls: its scrollbar sits on the same edge the handle straddles,
     * and the handle would otherwise take the clicks meant for it. 6 is enough to
     * clear the panel entirely, whatever width the platform's scrollbar is.
     */
    scrollbarClearance?: number;
};

// Keyboard resizing steps by this many pixels per arrow press.
const KEYBOARD_STEP = 16;

// Dragging within this distance of the default width snaps to it, so the default
// is easy to land on by feel (core's sidebars do the same).
const SNAP_DISTANCE = 10;

const ResizableDivider = ({ariaLabel, side, width, minWidth, maxWidth, defaultWidth, onResize, onResizeEnd, scrollbarClearance = 0}: Props) => {
    const [dragging, setDragging] = useState(false);
    const [snapped, setSnapped] = useState(false);
    const startX = useRef(0);
    const startWidth = useRef(0);
    const lastWidth = useRef(width);

    const clamp = (value: number) => Math.min(maxWidth, Math.max(minWidth, Math.round(value)));

    // Snapping is applied to the pointer's raw width so it can't compound: the
    // snap zone stays anchored to the default rather than following the result.
    const snap = (value: number) => (Math.abs(value - defaultWidth) <= SNAP_DISTANCE ? defaultWidth : value);

    // Dragging over the document needs a body-level cursor and no text
    // selection, otherwise the pointer flickers over the content it crosses.
    useEffect(() => {
        if (!dragging) {
            return undefined;
        }
        document.body.classList.add(styles.resizing);
        return () => document.body.classList.remove(styles.resizing);
    }, [dragging]);

    const widthFor = (clientX: number) => {
        const delta = side === 'left' ? clientX - startX.current : startX.current - clientX;
        return clamp(snap(startWidth.current + delta));
    };

    const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
        if (event.button !== 0) {
            return;
        }
        event.preventDefault();
        startX.current = event.clientX;
        startWidth.current = width;
        lastWidth.current = width;
        setDragging(true);
        event.currentTarget.setPointerCapture(event.pointerId);
    };

    const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
        if (!dragging) {
            return;
        }
        const next = widthFor(event.clientX);
        lastWidth.current = next;
        setSnapped(next === defaultWidth);
        onResize(next);
    };

    const endDrag = (event: React.PointerEvent<HTMLDivElement>, finalWidth: number) => {
        if (!dragging) {
            return;
        }
        setDragging(false);
        setSnapped(false);
        if (event.currentTarget.hasPointerCapture(event.pointerId)) {
            event.currentTarget.releasePointerCapture(event.pointerId);
        }
        onResizeEnd(finalWidth);
    };

    // Double-click restores the default width, as core's divider does.
    const onDoubleClick = () => {
        onResize(defaultWidth);
        onResizeEnd(defaultWidth);
    };

    const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
        const grow = side === 'left' ? 'ArrowRight' : 'ArrowLeft';
        const shrink = side === 'left' ? 'ArrowLeft' : 'ArrowRight';

        let next: number | undefined;
        if (event.key === grow) {
            next = clamp(width + KEYBOARD_STEP);
        } else if (event.key === shrink) {
            next = clamp(width - KEYBOARD_STEP);
        } else if (event.key === 'Home') {
            next = defaultWidth;
        }

        if (next !== undefined) {
            event.preventDefault();
            onResize(next);
            onResizeEnd(next);
        }
    };

    return (
        <div
            className={classNames(styles.divider, styles[side], {
                [styles.active]: dragging,
                [styles.snapped]: dragging && snapped,
            })}
            style={{'--docs-divider-clearance': `${scrollbarClearance}px`} as React.CSSProperties}
            role='separator'
            tabIndex={0}
            aria-label={ariaLabel}
            aria-orientation='vertical'
            aria-valuenow={width}
            aria-valuemin={minWidth}
            aria-valuemax={maxWidth}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={(event) => {
                const finalWidth = widthFor(event.clientX);
                onResize(finalWidth);
                endDrag(event, finalWidth);
            }}
            onPointerCancel={(event) => endDrag(event, lastWidth.current)}
            onDoubleClick={onDoubleClick}
            onKeyDown={onKeyDown}
        />
    );
};

export default ResizableDivider;
