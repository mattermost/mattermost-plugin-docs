// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {subscribeToImportJobUpdates} from 'client/import_events';
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

// useImportJob follows one import job for as long as it can still change.
//
// It polls *and* listens. The server publishes an actor-scoped import_job_updated, but at most once per second
// or every 25 pages and with no delivery guarantee, so a client that only listened could sit on a stale view
// indefinitely if one were dropped — during a reconnect, say. Polling is therefore the correctness floor and the
// event is pure latency relief: it re-enters the same loop early rather than feeding state of its own.
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

    // The worker's progress event, used as a nudge to read rather than as the new state.
    //
    // It arrives at most once per second or every 25 pages and carries five fields, where the job view carries
    // the plan, the counts and the acknowledgements — so applying it directly would build a partial view from an
    // event that is best-effort by design, and a dropped one would leave that view wrong until the next poll.
    // Re-entering the loop instead keeps polling as the floor and takes only the waiting out: a fifteen-second
    // idle poll becomes immediate when the plan is published, and progress advances as pages land rather than on
    // the next tick.
    useEffect(() => {
        if (!jobId) {
            return undefined;
        }
        return subscribeToImportJobUpdates((event) => {
            if (event.job_id !== jobId || !mounted.current) {
                return;
            }

            // Through the loop, not a bare read: the cadence has to be recomputed from whatever the read finds,
            // and a terminal state has to stop the polling rather than leave a timer running.
            tickRef.current();
        });
    }, [jobId]);

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

// How many jobs one discovery request reads, and how many requests it will make.
//
// An unfinished import is almost always among the newest, but "almost always" is not a search: several targets
// can each hold an import (admission allows several per target, and more per actor), so the match may be past
// the first page. The page count bounds the walk without leaving the answer to luck.
const IMPORT_RESUME_PER_PAGE = 50;
const IMPORT_RESUME_MAX_PAGES = 10;

export type ImportResumeState = {

    // jobId is the unfinished import found for this target, if any.
    jobId?: string;

    // resolving is true until the lookup settles, so the upload step is not offered to someone who already
    // has an import running.
    resolving: boolean;

    // failed is true when the lookup could not answer — which is not the same as "nothing is running", and is
    // reported rather than assumed. It does not stop an upload: the server enforces the per-target job limit, so
    // this check saves a wasted upload rather than preventing a bad one.
    failed: boolean;

    // failureStatus is the HTTP status behind that failure, when there was one. Absent for a transport failure.
    // Carried because the likely causes are indistinguishable to a user otherwise: 501 is Docs switched off,
    // 404 is a server whose plugin predates the import API.
    failureStatus?: number;

    // adopt records a newly created job as the one to follow.
    adopt: (jobId: string) => void;

    // retry runs the lookup again, for use after a failure.
    retry: () => void;
};

type ImportResumeInternalState = {
    identity: string;
    jobId?: string;
    resolving: boolean;
    failed: boolean;
    failureStatus?: number;
};

// targetIdentity reduces a target to the string that decides which import belongs to it.
const targetIdentity = (target: ImportTargetRequest): string =>
    (target.kind === 'new' ? `new:${target.team_id}` : `existing:${target.space_id}`);

// useResumableImportJob finds the actor's unfinished import for a target, so the wizard can be re-entered.
//
// An import is server-side work that outlives the page that started it: it survives a reload, a navigation
// away, and a server restart. If the only record of which job that is were this component's state, then
// closing the wizard — or refreshing the page — would strand a running import with no way back to it, while
// admission limits refused the new upload the user would then try. The job id is therefore recovered from the
// server rather than remembered, which also works on a different device.
export function useResumableImportJob(target: ImportTargetRequest): ImportResumeState {
    const identity = targetIdentity(target);

    // The identity is held *with* the answer, so a target change cannot leave the previous target's answer on
    // screen. Two pieces of state updated by an effect would show the old job until the new lookup returned —
    // and a lookup that then failed would leave it there for good, with the previous import's cancel button
    // live under a heading about a different Space.
    const [state, setState] = useState<ImportResumeInternalState>({identity, resolving: true, failed: false});
    const [attempt, setAttempt] = useState(0);

    // Resetting during render rather than in an effect is deliberate: an effect runs after the render that
    // already showed the wrong thing. React discards this render and redoes it with the new state.
    if (state.identity !== identity) {
        setState({identity, resolving: true, failed: false});
    }

    const adopt = useCallback((id: string) => {
        setState({identity, jobId: id, resolving: false, failed: false});
    }, [identity]);

    const retry = useCallback(() => {
        setState({identity, resolving: true, failed: false});
        setAttempt((n) => n + 1);
    }, [identity]);

    useEffect(() => {
        let cancelled = false;

        findUnfinishedImport(target).then((found) => {
            if (!cancelled) {
                setState({identity, jobId: found?.id, resolving: false, failed: false});
            }
        }).catch((err: unknown) => {
            if (!cancelled) {
                // Reported rather than treated as "nothing running", and reported with enough detail to act on.
                setState({
                    identity,
                    resolving: false,
                    failed: true,
                    failureStatus: err instanceof RestError ? err.status : undefined,
                });
            }
        });

        return () => {
            cancelled = true;
        };

        // target is intentionally not a dependency: it is a fresh object every render, and `identity` is the
        // part of it that decides the answer.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [identity, attempt]);

    const current = state.identity === identity;
    return {
        jobId: current ? state.jobId : undefined,
        resolving: !current || state.resolving,
        failed: current && state.failed,
        failureStatus: current ? state.failureStatus : undefined,
        adopt,
        retry,
    };
}

// findUnfinishedImport walks the actor's jobs, newest first, for one still running against this target.
async function findUnfinishedImport(target: ImportTargetRequest): Promise<ImportJobView | undefined> {
    const teamId = target.kind === 'new' ? target.team_id : undefined;

    for (let page = 0; page < IMPORT_RESUME_MAX_PAGES; page++) {
        // The team filter only applies to a new-Space target; for an existing Space the filter is the Space
        // itself, applied by the match below, and narrowing by team would need an id the caller did not give us.
        //
        // Sequential on purpose: each request's answer decides whether another is needed at all, and the usual
        // case is one page. Firing them in parallel would trade a rare second request for ten guaranteed ones.
        // eslint-disable-next-line no-await-in-loop
        const answer = await listImportJobs({teamId, page, perPage: IMPORT_RESUME_PER_PAGE});
        const items = answer.items ?? [];

        const unfinished = items.find((candidate) =>
            !isTerminalImportState(candidate.state) && matchesImportTarget(candidate, target));
        if (unfinished) {
            return unfinished;
        }
        if (!answer.has_more || items.length === 0) {
            return undefined;
        }
    }
    return undefined;
}

// matchesImportTarget reports whether a job is an import into the same place.
//
// A job is only offered for resuming when its target is the one being imported into now. Adopting any
// unfinished import would show a user work belonging to a different Space and offer its plan for
// confirmation — and a new-Space job's space_id is one the server minted for it, so identity there is the
// team plus the fact that the Space did not exist.
function matchesImportTarget(job: ImportJobView, target: ImportTargetRequest): boolean {
    if (job.target.kind !== target.kind) {
        return false;
    }
    return target.kind === 'new' ? job.target.team_id === target.team_id : job.target.space_id === target.space_id;
}
