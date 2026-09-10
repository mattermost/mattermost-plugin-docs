// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSidebarWidth} from 'hooks/sidebar_width';
import {useSyncExternalStore} from 'react';

export const DEFAULT_SIDEBAR_WIDTH = 232;
export const MIN_SIDEBAR_WIDTH = 200;
export const MAX_SIDEBAR_WIDTH = 400;

// The cap is also a share of the window, so a sidebar widened on a large screen
// can't crowd the page content once the window shrinks. Applied on top of the
// stored width rather than written back to it, so re-widening the window restores
// what the user actually chose.
const MAX_SIDEBAR_VIEWPORT_SHARE = 0.4;

const subscribeToViewport = (listener: () => void): (() => void) => {
    window.addEventListener('resize', listener);
    return () => window.removeEventListener('resize', listener);
};

const viewportWidth = (): number => window.innerWidth;

type PagesSidebarWidth = {

    /** The width to render: the stored preference under the viewport cap. */
    width: number;

    /** The cap in force right now — what the resize handle may drag up to. */
    maxWidth: number;

    /** Transient update while dragging — shared immediately, not persisted. */
    setWidth: (width: number) => void;

    /** Persists the final width for this user. */
    commitWidth: (width: number) => void;
};

/**
 * The pages sidebar's width. Both the sidebar itself and the chrome that aligns
 * to its edge call this, so they resolve the same width and the same live cap.
 */
export function usePagesSidebarWidth(): PagesSidebarWidth {
    const {width: preferred, setWidth, commitWidth} = useSidebarWidth('pages', DEFAULT_SIDEBAR_WIDTH, {
        minWidth: MIN_SIDEBAR_WIDTH,
        maxWidth: MAX_SIDEBAR_WIDTH,
    });

    const available = useSyncExternalStore(subscribeToViewport, viewportWidth);

    // Floored at the minimum: a cap below it would let the clamp pull the sidebar
    // narrower than the minimum on a very small window.
    const maxWidth = Math.max(
        MIN_SIDEBAR_WIDTH,
        Math.min(MAX_SIDEBAR_WIDTH, Math.round(available * MAX_SIDEBAR_VIEWPORT_SHARE)),
    );

    return {width: Math.min(preferred, maxWidth), maxWidth, setWidth, commitWidth};
}
