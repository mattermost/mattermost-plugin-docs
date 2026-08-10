// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {readSidebarWidth, writeSidebarWidth} from 'data/sidebar_widths';
import {useAppSelector} from 'hooks/redux';
import {useCallback, useSyncExternalStore} from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

// Widths live in a module-level store rather than component state so that every
// reader of a given panel sees the same live value — the pages sidebar owns the
// drag, while other chrome (e.g. the page header) can align to its edge without
// the width being threaded through props. Seeded from localStorage on first read.
const widths = new Map<string, number>();
const listeners = new Set<() => void>();

const storeKey = (userId: string, name: string): string => `${userId}:${name}`;

const subscribe = (listener: () => void): (() => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

const setStoredWidth = (key: string, width: number): void => {
    if (widths.get(key) !== width) {
        widths.set(key, width);
        listeners.forEach((listener) => listener());
    }
};

type SidebarWidthBounds = {
    minWidth: number;
    maxWidth: number;
};

const clampWidth = (width: number, {minWidth, maxWidth}: SidebarWidthBounds): number =>
    Math.min(maxWidth, Math.max(minWidth, width));

// Idempotent: memoizes the initial localStorage read so getSnapshot stays stable.
const currentWidth = (userId: string, name: string, defaultWidth: number, bounds: SidebarWidthBounds): number => {
    const key = storeKey(userId, name);
    const known = widths.get(key);
    if (known !== undefined) {
        return clampWidth(known, bounds);
    }
    const initial = clampWidth(readSidebarWidth(userId, name) ?? defaultWidth, bounds);
    widths.set(key, initial);
    return initial;
};

type SidebarWidth = {
    width: number;

    /** Transient update while dragging — shared immediately, not persisted. */
    setWidth: (width: number) => void;

    /** Persists the final width for this user. */
    commitWidth: (width: number) => void;
};

/**
 * A resizable panel's width for the current user, restored from and saved to
 * localStorage (see data/sidebar_widths). Drag with `setWidth` and persist once
 * on release with `commitWidth`, so a drag isn't a write per frame.
 *
 * Calling this with the same `name` anywhere returns the same live value, so a
 * read-only consumer can just call it instead of receiving the width as a prop.
 */
export function useSidebarWidth(name: string, defaultWidth: number, bounds: SidebarWidthBounds): SidebarWidth {
    const userId = useAppSelector(getCurrentUserId);
    const key = storeKey(userId, name);
    const {minWidth, maxWidth} = bounds;
    const clamp = useCallback((next: number) => clampWidth(next, {minWidth, maxWidth}), [minWidth, maxWidth]);

    const width = useSyncExternalStore(subscribe, () => currentWidth(userId, name, defaultWidth, {minWidth, maxWidth}));

    const setWidth = useCallback((next: number) => setStoredWidth(key, clamp(next)), [key, clamp]);

    const commitWidth = useCallback((next: number) => {
        const clamped = clamp(next);
        setStoredWidth(key, clamped);
        writeSidebarWidth(userId, name, clamped);
    }, [key, userId, name, clamp]);

    return {width, setWidth, commitWidth};
}
