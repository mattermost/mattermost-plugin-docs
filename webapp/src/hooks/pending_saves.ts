// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

type Flush = () => Promise<boolean>;

const pending = new Set<Flush>();

export function registerPendingSave(flush: Flush): () => void {
    pending.add(flush);
    return () => {
        pending.delete(flush);
    };
}

export async function flushPendingSaves(): Promise<boolean> {
    const results = await Promise.all(Array.from(pending, (flush) => flush()));
    return results.every(Boolean);
}
