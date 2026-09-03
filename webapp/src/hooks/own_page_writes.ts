// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSyncExternalStore} from 'react';

const editAtByPage = new Map<string, number>();

const listeners = new Set<() => void>();

const subscribe = (listener: () => void): (() => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

export function recordOwnPageWrite(pageId: string, editAt: number): void {
    if (!pageId || !editAt || (editAtByPage.get(pageId) ?? 0) >= editAt) {
        return;
    }

    editAtByPage.set(pageId, editAt);
    listeners.forEach((listener) => listener());
}

export function useOwnPageWrite(pageId: string): number | undefined {
    return useSyncExternalStore(subscribe, () => editAtByPage.get(pageId));
}

export function clearOwnPageWrites(): void {
    editAtByPage.clear();
    listeners.forEach((listener) => listener());
}
