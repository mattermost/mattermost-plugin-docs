// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ImportJobPhase, ImportJobState} from 'types/imports';

// The actor-scoped progress event the import worker publishes (server/app/ws_events.go). It is deliberately
// thin: the worker sends it at most once per second or every 25 pages, and the job view carries far more than
// this — target, bundle summary, plan counts, required acknowledgements.
//
// So it is treated as a *nudge to read*, never as the new state. Applying these fields directly would build a
// partial job view out of an event that is best-effort and lossy by design, and a dropped one would leave that
// view wrong until the next poll. Polling remains the correctness floor; this only removes the wait.
export type ImportJobUpdatedEvent = {
    job_id: string;
    state: ImportJobState;
    phase: ImportJobPhase | '';
    progress_current: number;
    progress_total: number;
};

type Listener = (event: ImportJobUpdatedEvent) => void;

const listeners = new Set<Listener>();

export function subscribeToImportJobUpdates(listener: Listener): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

export function publishImportJobUpdate(event: ImportJobUpdatedEvent): void {
    listeners.forEach((listener) => listener(event));
}
