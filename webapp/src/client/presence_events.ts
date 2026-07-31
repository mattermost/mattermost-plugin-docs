// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {PageActiveEditors} from 'types/drafts';

export type PagePresenceEvent = PageActiveEditors & {
    page_id: string;
};

type Listener = (event: PagePresenceEvent) => void;

const listeners = new Set<Listener>();

export function subscribeToPagePresence(listener: Listener): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

export function publishPagePresence(event: PagePresenceEvent): void {
    listeners.forEach((listener) => listener(event));
}
