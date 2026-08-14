// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Client-side "recently viewed spaces" store. There is no server view-history
// API yet; space visibility is already driven by backing-channel membership, so
// the eventual source is that channel's ChannelMember.LastViewedAt surfaced by
// the server. Callers use recordSpaceView / getSpaceViews as the seam, so that
// swap won't touch them.

export type SpaceView = {
    spaceId: string;
    lastViewedAt: number;
};

const KEY_PREFIX = 'docs_recent_spaces_';
const MAX_TRACKED = 50;

// Per-user so multiple accounts on one browser don't share a history.
const storageKey = (userId: string): string => `${KEY_PREFIX}${userId}`;

const read = (userId: string): Record<string, number> => {
    try {
        const raw = window.localStorage.getItem(storageKey(userId));
        return raw ? JSON.parse(raw) as Record<string, number> : {};
    } catch {
        // Storage unavailable (private mode / quota) or corrupt — treat as empty.
        return {};
    }
};

const write = (userId: string, map: Record<string, number>): void => {
    try {
        window.localStorage.setItem(storageKey(userId), JSON.stringify(map));
    } catch {
        // Best-effort; recency is non-critical, so a write failure is ignored.
    }
};

export function recordSpaceView(userId: string, spaceId: string, at: number): void {
    if (!userId || !spaceId) {
        return;
    }
    const map = read(userId);
    map[spaceId] = at;

    // Cap the history: drop the oldest entries beyond MAX_TRACKED.
    const entries = Object.entries(map);
    if (entries.length > MAX_TRACKED) {
        entries.sort((a, b) => b[1] - a[1]);
        write(userId, Object.fromEntries(entries.slice(0, MAX_TRACKED)));
        return;
    }
    write(userId, map);
}

// Most-recently-viewed first.
export function getSpaceViews(userId: string): SpaceView[] {
    return Object.entries(read(userId)).
        map(([spaceId, lastViewedAt]) => ({spaceId, lastViewedAt})).
        sort((a, b) => b.lastViewedAt - a.lastViewedAt);
}
