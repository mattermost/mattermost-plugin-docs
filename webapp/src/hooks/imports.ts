// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getImportJob, listImportJobs} from 'client/imports';
import {RestError} from 'client/rest';
import {useCallback, useEffect, useRef, useState} from 'react';

import type {ImportJobView, ImportTargetRequest} from 'types/imports';
import {isAwaitingUserImportState, isTerminalImportState} from 'types/imports';

// Poll intervals, chosen from what the server is actually doing rather than from a single compromise.
//
// The worker picks work up within a couple of seconds and can then write thousands of pages, so a job it
// owns is polled briskly: this is the only feedback a user gets while an import runs. A job waiting on a
// *person* changes only when something else invalidates its plan, which is rare, so it is polled slowly —
// and a terminal job never changes at all, so it is not polled.
export const IMPORT_POLL_ACTIVE_MS = 2000;
export const IMPORT_POLL_IDLE_MS = 15000;

// IMPORT_POLL_RETRY_MS is how long to wait after a read that failed inconclusively. It is slower than the
// active cadence, because a read that just failed is more likely to fail again than a job is to change, and
// faster than the idle one, because something is wrong and the user is watching.
export const IMPORT_POLL_RETRY_MS = 5000;

// pollIntervalFor returns how long to wait before the next read of this job, or null to stop polling.
//
// Polling stops for exactly one reason: the job has reached a state it can never leave. Without a job there
// is no answer here — see intervalAfter, which distinguishes "this job is gone" from "I could not tell".
export const pollIntervalFor = (job: ImportJobView | undefined): number | null => {
    if (!job) {
        return null;
    }
    if (isTerminalImportState(job.state)) {
        return null;
    }
    return isAwaitingUserImportState(job.state) ? IMPORT_POLL_IDLE_MS : IMPORT_POLL_ACTIVE_MS;
};

// The result of one read attempt, which is what decides whether and when to read again.
//
// The distinction that matters is between a definitive answer and a failure to get one. A 404 means the job is
// gone or is no longer this user's to see, and nothing will change that; anything else — a 500, a dropped
// connection — is a fact about the network rather than about the job, and an import may well still be running
// behind it. Collapsing the two is how a poller stops for good on a blip.
type ImportReadOutcome =
    | {kind: 'job'; job: ImportJobView}
    | {kind: 'gone'}
    | {kind: 'failed'}
    | {kind: 'skipped'};

// intervalAfter returns how long to wait after one read, or null to stop.
const intervalAfter = (outcome: ImportReadOutcome): number | null => {
    switch (outcome.kind) {
    case 'job':
        return pollIntervalFor(outcome.job);
    case 'gone':
        return null;
    case 'failed':
        return IMPORT_POLL_RETRY_MS;

    // A read that never ran — one already in flight — has learned nothing, so the loop simply comes back.
    // Dropping the schedule here would end polling every time a user action overlapped a tick.
    default:
        return IMPORT_POLL_ACTIVE_MS;
    }
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

    // tickRef holds the current loop body, so anything that reschedules revives the *loop* rather than
    // replacing it with a single read. A one-off read is how polling silently ends.
    const tickRef = useRef<() => void>(() => {});

    // inFlight stops a slow response from being overtaken by the next tick: two overlapping reads can
    // resolve out of order and move the displayed state backwards, which during an import looks like
    // progress going in reverse.
    const inFlight = useRef(false);

    const read = useCallback(async (id: string): Promise<ImportReadOutcome> => {
        if (inFlight.current) {
            return {kind: 'skipped'};
        }
        inFlight.current = true;
        try {
            const next = await getImportJob(id);
            if (!mounted.current || activeJobId.current !== id) {
                return {kind: 'skipped'};
            }
            setJob(next);
            setError(undefined);
            return {kind: 'job', job: next};
        } catch (err) {
            if (!mounted.current || activeJobId.current !== id) {
                return {kind: 'skipped'};
            }

            // A RestError is a real answer about this job — gone, or no longer visible — and is shown.
            // Anything else is a transport failure, which the next poll may well recover from, so the last
            // known job is kept rather than replaced with an error.
            if (err instanceof RestError) {
                setError(err);
                return err.status === 404 ? {kind: 'gone'} : {kind: 'failed'};
            }
            return {kind: 'failed'};
        } finally {
            inFlight.current = false;
            if (mounted.current && activeJobId.current === id) {
                setLoading(false);
            }
        }
    }, []);

    // schedule replaces whatever was pending with one wake-up, or with nothing when polling is over.
    const schedule = useCallback((interval: number | null) => {
        if (timer.current) {
            clearTimeout(timer.current);
            timer.current = undefined;
        }
        if (interval === null) {
            return;
        }
        timer.current = setTimeout(() => tickRef.current(), interval);
    }, []);

    // The read/schedule loop. It reschedules from the *outcome* of each read, so the cadence follows the
    // job's current state and stops of its own accord once the job is terminal or gone.
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
            const outcome = await read(jobId);
            if (!mounted.current || activeJobId.current !== jobId) {
                return;
            }
            schedule(intervalAfter(outcome));
        };
        tickRef.current = () => {
            tick().catch(() => {
                // read() already funnels every failure into state; nothing is left to handle here.
            });
        };
        tickRef.current();

        return () => {
            mounted.current = false;
            if (timer.current) {
                clearTimeout(timer.current);
                timer.current = undefined;
            }
        };
    }, [jobId, read, schedule]);

    const refresh = useCallback(async () => {
        if (!jobId) {
            return undefined;
        }
        const outcome = await read(jobId);
        if (!mounted.current || activeJobId.current !== jobId) {
            return undefined;
        }

        // An action that advanced the job usually means the next interesting change is imminent, so the
        // pending slow tick is replaced with a prompt one. It is still the recurring tick: scheduling a bare
        // read here would poll exactly once more and then stop.
        const soon = outcome.kind === 'job' && !isTerminalImportState(outcome.job.state);
        schedule(soon ? IMPORT_POLL_ACTIVE_MS : intervalAfter(outcome));

        return outcome.kind === 'job' ? outcome.job : undefined;
    }, [jobId, read, schedule]);

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

// How many of the actor's recent jobs to look through when resuming. An unfinished import is almost always
// the newest one; this is a bound, not a search.
const IMPORT_RESUME_SCAN = 20;

export type ImportResumeState = {

    // jobId is the unfinished import found for this target, if any.
    jobId?: string;

    // resolving is true until the lookup settles, so the upload step is not offered to someone who already
    // has an import running.
    resolving: boolean;

    // adopt records a newly created job as the one to follow.
    adopt: (jobId: string) => void;
};

// useResumableImportJob finds the actor's unfinished import for a target, so the wizard can be re-entered.
//
// An import is server-side work that outlives the page that started it: it survives a reload, a navigation
// away, and a server restart. If the only record of which job that is were this component's state, then
// closing the wizard — or refreshing the page — would strand a running import with no way back to it, while
// admission limits refused the new upload the user would then try. The job id is therefore recovered from the
// server rather than remembered, which also works on a different device.
export function useResumableImportJob(target: ImportTargetRequest): ImportResumeState {
    const [jobId, setJobId] = useState<string | undefined>();
    const [resolving, setResolving] = useState(true);

    const adopt = useCallback((id: string) => {
        setJobId(id);
        setResolving(false);
    }, []);

    // Only the identifying parts of the target are dependencies, so a caller passing a fresh object literal
    // each render does not restart the lookup.
    const targetKind = target.kind;
    const targetTeamId = target.kind === 'new' ? target.team_id : '';
    const targetSpaceId = target.kind === 'existing' ? target.space_id : '';

    useEffect(() => {
        let cancelled = false;

        // The team is only known for a new-Space target; for an existing Space the filter is the Space itself,
        // applied below, and narrowing by team as well would need a team id the caller did not give us.
        listImportJobs({teamId: targetTeamId || undefined, perPage: IMPORT_RESUME_SCAN}).then((page) => {
            if (cancelled) {
                return;
            }
            const unfinished = (page.items ?? []).find((candidate) =>
                !isTerminalImportState(candidate.state) && matchesImportTarget(candidate, targetKind, targetTeamId, targetSpaceId));
            setJobId(unfinished?.id);
            setResolving(false);
        }).catch(() => {
            // A failed lookup must not block starting an import. The worst case is an upload the server
            // refuses on admission, which says so plainly — better than a wizard that will not open.
            if (!cancelled) {
                setResolving(false);
            }
        });

        return () => {
            cancelled = true;
        };
    }, [targetKind, targetTeamId, targetSpaceId]);

    return {jobId, resolving, adopt};
}

// matchesImportTarget reports whether a job is an import into the same place.
//
// A job is only offered for resuming when its target is the one being imported into now. Adopting any
// unfinished import would show a user work belonging to a different Space and offer its plan for
// confirmation — and a new-Space job's space_id is one the server minted for it, so identity there is the
// team plus the fact that the Space did not exist.
function matchesImportTarget(job: ImportJobView, kind: string, teamId: string, spaceId: string): boolean {
    if (job.target.kind !== kind) {
        return false;
    }
    return kind === 'new' ? job.target.team_id === teamId : job.target.space_id === spaceId;
}
