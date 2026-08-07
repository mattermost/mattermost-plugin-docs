// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Client-side store for resizable sidebar widths. How wide a user drags a panel
// is a per-user UI preference with no server model, so it lives in localStorage
// (mirroring data/collapsed_pages). Keyed per user and per panel name.

const KEY_PREFIX = 'docs_sidebar_width_';

const storageKey = (userId: string, name: string): string => `${KEY_PREFIX}${name}_${userId}`;

/** The stored width for a panel, or undefined when unset/unavailable. */
export function readSidebarWidth(userId: string, name: string): number | undefined {
    try {
        const raw = window.localStorage.getItem(storageKey(userId, name));
        if (!raw) {
            return undefined;
        }
        const width = Number.parseInt(raw, 10);
        return Number.isFinite(width) ? width : undefined;
    } catch {
        // Storage unavailable (private mode / quota) — fall back to the default.
        return undefined;
    }
}

export function writeSidebarWidth(userId: string, name: string, width: number): void {
    try {
        window.localStorage.setItem(storageKey(userId, name), String(Math.round(width)));
    } catch {
        // Best-effort; a width is non-critical, so a write failure is ignored.
    }
}
