// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Client-side "collapsed pages" store for the page tree. Which nodes a user has
// collapsed is a per-user UI preference with no server model, so it lives in
// localStorage (mirroring data/recent_spaces). Stored as a flat list of the
// collapsed page ids.

const KEY_PREFIX = 'docs_collapsed_pages_';

// Per-user so multiple accounts on one browser don't share collapse state.
const storageKey = (userId: string): string => `${KEY_PREFIX}${userId}`;

const read = (userId: string): string[] => {
    try {
        const raw = window.localStorage.getItem(storageKey(userId));
        return raw ? JSON.parse(raw) as string[] : [];
    } catch {
        // Storage unavailable (private mode / quota) or corrupt — treat as empty.
        return [];
    }
};

const write = (userId: string, ids: string[]): void => {
    try {
        window.localStorage.setItem(storageKey(userId), JSON.stringify(ids));
    } catch {
        // Best-effort; collapse state is non-critical, so a write failure is ignored.
    }
};

export function getCollapsed(userId: string): Set<string> {
    return new Set(read(userId));
}

export function toggleCollapsed(userId: string, pageId: string): Set<string> {
    const next = getCollapsed(userId);
    if (next.has(pageId)) {
        next.delete(pageId);
    } else {
        next.add(pageId);
    }
    write(userId, [...next]);
    return next;
}

// Sets the collapsed state for many pages at once (a subtree). Backs shift-click
// expand/collapse-all on a node.
export function setCollapsedFor(userId: string, ids: string[], collapsed: boolean): Set<string> {
    const next = getCollapsed(userId);
    for (const id of ids) {
        if (collapsed) {
            next.add(id);
        } else {
            next.delete(id);
        }
    }
    write(userId, [...next]);
    return next;
}
