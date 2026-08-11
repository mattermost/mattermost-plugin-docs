// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getImportJob} from 'client/imports';
import {RestError} from 'client/rest';
import {useCallback, useEffect, useRef, useState} from 'react';

import type {ImportJobView} from 'types/imports';
import {isAwaitingUserImportState, isTerminalImportState} from 'types/imports';

// Poll intervals, chosen from what the server is actually doing rather than from a single compromise.
//
// The worker picks work up within a couple of seconds and can then write thousands of pages, so a job it
// owns is polled briskly: this is the only feedback a user gets while an import runs. A job waiting on a
// *person* changes only when something else invalidates its plan, which is rare, so it is polled slowly —
// and a terminal job never changes at all, so it is not polled.
export const IMPORT_POLL_ACTIVE_MS = 2000;
export const IMPORT_POLL_IDLE_MS = 15000;

// pollIntervalFor returns how long to wait before the next read, or null to stop polling entirely.
export const pollIntervalFor = (job: ImportJobView | undefined): number | null => {
    if (!job) {
        return null;
    }
    if (isTerminalImportState(job.state)) {
        return null;
    }
    return isAwaitingUserImportState(job.state) ? IMPORT_POLL_IDLE_MS : IMPORT_POLL_ACTIVE_MS;
};

export type ImportJobPollState = {
    job?: ImportJobView;

    // loading is true only for the first read. A poll that refreshes an already-loaded job must not put the
    // UI back into a loading state, or the whole wizard would flicker every couple of seconds.
    loading: boolean;
    error?: RestError;

    // refresh reads the job immediately, for use after an action that changes it. Polling continues
    // afterwards on the cadence the new state implies.
    refresh: () => Promise<ImportJobView | undefined>;
};

// useImportJob polls one import job for as long as it can still change.
//
// Polling rather than relying on the WebSocket event is deliberate for this first pass: the server does
// publish an actor-scoped import_job_updated, but it is best-effort and at most one per second or 25
// pages, so a client that only listened could sit on a stale view indefinitely if one were dropped.
// Polling is the correctness floor; the event is a latency optimization to layer on top.
export function useImportJob(jobId: string | undefined): ImportJobPollState {
    const [job, setJob] = useState<ImportJobView | undefined>();
    const [loading, setLoading] = useState<boolean>(Boolean(jobId));
    const [error, setError] = useState<RestError | undefined>();

    // Guards against writing state after unmount, and against a scheduled poll firing once the id changes.
    const activeJobId = useRef(jobId);
    const mounted = useRef(true);
    const timer = useRef<ReturnType<typeof setTimeout> | undefined>();

    // inFlight stops a slow response from being overtaken by the next tick: two overlapping reads can
    // resolve out of order and move the displayed state backwards, which during an import looks like
    // progress going in reverse.
    const inFlight = useRef(false);

    const read = useCallback(async (id: string): Promise<ImportJobView | undefined> => {
        if (inFlight.current) {
            return undefined;
        }
        inFlight.current = true;
        try {
            const next = await getImportJob(id);
            if (!mounted.current || activeJobId.current !== id) {
                return undefined;
            }
            setJob(next);
            setError(undefined);
            return next;
        } catch (err) {
            if (!mounted.current || activeJobId.current !== id) {
                return undefined;
            }

            // A RestError is a real answer about this job — gone, or no longer visible — and is shown.
            // Anything else is a transport failure, which the next poll may well recover from, so the last
            // known job is kept rather than replaced with an error.
            if (err instanceof RestError) {
                setError(err);
            }
            return undefined;
        } finally {
            inFlight.current = false;
            if (mounted.current && activeJobId.current === id) {
                setLoading(false);
            }
        }
    }, []);

    // The read/schedule loop. It reschedules from the *result* of each read, so the cadence follows the
    // job's current state and stops of its own accord once the job is terminal.
    useEffect(() => {
        mounted.current = true;
        activeJobId.current = jobId;

        if (!jobId) {
            setJob(undefined);
            setError(undefined);
            setLoading(false);
            return undefined;
        }

        setLoading(true);

        const tick = async () => {
            const next = await read(jobId);
            if (!mounted.current || activeJobId.current !== jobId) {
                return;
            }
            const interval = pollIntervalFor(next);
            if (interval !== null) {
                timer.current = setTimeout(tick, interval);
            }
        };
        tick().catch(() => {
            // read() already funnels every failure into state; nothing is left to handle here.
        });

        return () => {
            mounted.current = false;
            if (timer.current) {
                clearTimeout(timer.current);
                timer.current = undefined;
            }
        };
    }, [jobId, read]);

    const refresh = useCallback(async () => {
        if (!jobId) {
            return undefined;
        }
        const next = await read(jobId);

        // An action that advanced the job usually means the next interesting change is imminent, so the
        // pending slow tick is replaced with a prompt one rather than waited out.
        if (next && !isTerminalImportState(next.state)) {
            if (timer.current) {
                clearTimeout(timer.current);
            }
            timer.current = setTimeout(() => {
                read(jobId).catch(() => {
                    // Handled inside read(); see above.
                });
            }, IMPORT_POLL_ACTIVE_MS);
        }
        return next;
    }, [jobId, read]);

    return {job, loading, error, refresh};
}

// requiredAcknowledgementsSatisfied reports whether every key the job demands has been ticked.
//
// The required set comes from the job, never from the client's own reading of the bundle: the server
// derives it from persisted counts and refuses a confirmation missing any key — and equally refuses keys
// it did not ask for, so a client that offered a fixed list of checkboxes could make the import
// unconfirmable.
export function requiredAcknowledgementsSatisfied(
    job: ImportJobView | undefined,
    checked: Record<string, boolean>,
): boolean {
    if (!job) {
        return false;
    }
    return job.required_acknowledgements.every((key) => checked[key] === true);
}
