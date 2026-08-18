// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSyncExternalStore} from 'react';

import type {AutosaveStatus} from './draft_autosave';

const listeners = new Set<() => void>();

let status: AutosaveStatus | null = null;

const subscribe = (listener: () => void): (() => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

export function publishAutosaveStatus(next: AutosaveStatus | null): void {
    if (status !== next) {
        status = next;
        listeners.forEach((listener) => listener());
    }
}

export function useAutosaveStatus(): AutosaveStatus | null {
    return useSyncExternalStore(subscribe, () => status);
}
