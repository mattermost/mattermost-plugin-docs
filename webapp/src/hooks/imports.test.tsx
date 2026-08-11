// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';
import * as importsClient from 'client/imports';
import {RestError} from 'client/rest';

import type {ImportJobState, ImportJobView, ImportTargetRequest} from 'types/imports';

import {
    IMPORT_POLL_ACTIVE_MS,
    IMPORT_POLL_IDLE_MS,
    IMPORT_POLL_RETRY_MS,
    pollIntervalFor,
    requiredAcknowledgementsSatisfied,
    useImportJob,
    useResumableImportJob,
} from './imports';

const makeJob = (state: ImportJobState, overrides: Partial<ImportJobView> = {}): ImportJobView => ({
    id: 'job1',
    state,
    progress: {phase: '', current: 0, total: 0},
    target: {kind: 'new', team_id: 'team1', existed: false},
    bundle: {
        version: 2,
        source: {organization_id: '', space_key: 'DOCS', space_name: 'Docs'},
        space_defaults: {title: 'Docs', description: ''},
        counts: {
            pages: 3,
            comments: 0,
            attachments: 0,
            restricted_manifest_total: 0,
            restricted_emitted_pages: 0,
            restricted_manifest_only: 0,
        },
    },
    source_candidates: [],
    required_acknowledgements: [],
    create_at: 1,
    update_at: 1,
    finished_at: 0,
    ...overrides,
});

describe('pollIntervalFor', () => {
    // A job the worker owns is the only time a user has no other feedback that anything is happening, so
    // it is polled briskly.
    it.each<ImportJobState>(['queued_preflight', 'preflighting', 'queued_import', 'importing', 'terminalizing'])(
        'polls %s briskly',
        (state) => {
            expect(pollIntervalFor(makeJob(state))).toBe(IMPORT_POLL_ACTIVE_MS);
        },
    );

    // A job waiting on a person only changes if something else invalidates its plan, which is rare.
    it.each<ImportJobState>(['awaiting_source', 'awaiting_confirmation'])('polls %s slowly', (state) => {
        expect(pollIntervalFor(makeJob(state))).toBe(IMPORT_POLL_IDLE_MS);
    });

    // A terminal job never changes again, so continuing to poll it would be pure waste.
    it.each<ImportJobState>(['completed', 'completed_with_issues', 'failed', 'canceled'])(
        'stops polling %s',
        (state) => {
            expect(pollIntervalFor(makeJob(state))).toBeNull();
        },
    );

    it('stops when there is no job', () => {
        expect(pollIntervalFor(undefined)).toBeNull();
    });
});

describe('useImportJob', () => {
    // renderImportJob renders the hook and lets its first read settle inside act(). The initial read is
    // kicked off by an effect, so without this every test would report a state update outside act — noise
    // that hides the real ones.
    const renderImportJob = async (jobId: string | undefined) => {
        const rendered = renderHook(() => useImportJob(jobId));
        await act(async () => {
            await Promise.resolve();
        });
        return rendered;
    };

    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        // Discard pending timers rather than running them. This describe's afterEach runs before React
        // Testing Library's auto-unmount, so *running* a scheduled poll here would update a still-mounted
        // component from outside act() — a warning about the teardown, not about anything under test.
        jest.clearAllTimers();
        jest.useRealTimers();
        jest.restoreAllMocks();
    });

    it('reads the job once and stops polling when it is already terminal', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').mockResolvedValue(makeJob('completed'));

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.job?.state).toBe('completed');
        expect(get).toHaveBeenCalledTimes(1);

        // Advancing well past any interval must produce no further reads.
        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_IDLE_MS * 5);
        });
        expect(get).toHaveBeenCalledTimes(1);
    });

    it('keeps polling a running job and stops once it reaches a terminal state', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').
            mockResolvedValueOnce(makeJob('importing')).
            mockResolvedValueOnce(makeJob('importing')).
            mockResolvedValue(makeJob('completed_with_issues'));

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.job?.state).toBe('importing'));

        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS);
        });
        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS);
        });
        await waitFor(() => expect(result.current.job?.state).toBe('completed_with_issues'));

        const callsAtTerminal = get.mock.calls.length;
        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS * 5);
        });
        expect(get).toHaveBeenCalledTimes(callsAtTerminal);
    });

    // Two overlapping reads can resolve out of order, and during an import that shows progress running
    // backwards. The race that can actually happen is a user action calling refresh() while a scheduled
    // poll is still in flight, so that is what this drives.
    it('does not start a second read while one is in flight', async () => {
        const pending: Array<(job: ImportJobView) => void> = [];
        const get = jest.spyOn(importsClient, 'getImportJob').mockImplementation(() =>
            new Promise<ImportJobView>((resolve) => {
                pending.push(resolve);
            }),
        );

        const {result} = await renderImportJob('job1');
        expect(get).toHaveBeenCalledTimes(1);

        // The first read has not resolved yet. refresh() must not issue a competing one.
        let refreshed: ImportJobView | undefined;
        await act(async () => {
            refreshed = await result.current.refresh();
        });
        expect(get).toHaveBeenCalledTimes(1);
        expect(refreshed).toBeUndefined();

        // Once it resolves, reads work normally again.
        await act(async () => {
            pending[0](makeJob('importing'));
        });
        await waitFor(() => expect(result.current.job?.state).toBe('importing'));
    });

    it('stops polling after unmount', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').mockResolvedValue(makeJob('importing'));

        const {result, unmount} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.job?.state).toBe('importing'));
        const callsAtUnmount = get.mock.calls.length;

        unmount();
        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS * 5);
        });
        expect(get).toHaveBeenCalledTimes(callsAtUnmount);
    });

    // A job that is gone, or no longer visible, is a real answer about it and is surfaced.
    it('surfaces an API error', async () => {
        jest.spyOn(importsClient, 'getImportJob').mockRejectedValue(
            new RestError('/imports/job1', 404, 'Not found.', {id: 'app.store.not_found.app_error'}, 'app.store.not_found.app_error'),
        );

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.error?.status).toBe(404));
        expect(result.current.job).toBeUndefined();
        expect(result.current.loading).toBe(false);
    });

    // A transport failure says nothing about the job, and the next poll may recover, so the last known
    // state is kept rather than replaced by an error screen.
    it('keeps the last known job when a poll fails at the transport level', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').
            mockResolvedValueOnce(makeJob('importing')).
            mockRejectedValueOnce(new TypeError('network down')).
            mockResolvedValue(makeJob('completed'));

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.job?.state).toBe('importing'));

        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS);
        });
        expect(result.current.job?.state).toBe('importing');
        expect(result.current.error).toBeUndefined();
        expect(get.mock.calls.length).toBeGreaterThan(1);
    });

    // The failure above must not be the end of the polling. A 500 or a dropped connection says nothing about
    // the job, so treating it as "nothing more will happen" freezes the view for the rest of an import that is
    // still running — and the only way out is a reload the user has no reason to think they need.
    it('keeps polling after a read fails inconclusively', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').
            mockResolvedValueOnce(makeJob('importing')).
            mockRejectedValueOnce(new TypeError('network down')).
            mockResolvedValue(makeJob('completed'));

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.job?.state).toBe('importing'));

        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS);
        });
        expect(get).toHaveBeenCalledTimes(2);

        // The failed read backs the cadence off rather than ending it, so the recovery arrives on its own.
        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_RETRY_MS);
        });
        await waitFor(() => expect(result.current.job?.state).toBe('completed'));
        expect(get).toHaveBeenCalledTimes(3);
    });

    // The one failure that *is* final: a job that is gone, or is no longer this user's to see, will not come
    // back, so retrying would be a request per interval forever with a known answer.
    it('stops polling a job that is gone', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').mockRejectedValue(
            new RestError('/imports/job1', 404, 'Not found.', {id: 'app.store.not_found.app_error'}, 'app.store.not_found.app_error'),
        );

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.error?.status).toBe(404));

        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_RETRY_MS * 5);
        });
        expect(get).toHaveBeenCalledTimes(1);
    });

    it('does not read anything without a job id', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').mockResolvedValue(makeJob('importing'));
        const {result} = await renderImportJob(undefined);

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(get).not.toHaveBeenCalled();
        expect(result.current.job).toBeUndefined();
    });

    it('refresh reads immediately', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').
            mockResolvedValueOnce(makeJob('awaiting_confirmation')).
            mockResolvedValue(makeJob('queued_import'));

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.job?.state).toBe('awaiting_confirmation'));

        // Without refresh this would wait out the slow idle interval.
        await act(async () => {
            await result.current.refresh();
        });
        expect(result.current.job?.state).toBe('queued_import');
        expect(get.mock.calls.length).toBeGreaterThanOrEqual(2);
    });

    // refresh has to bring the *loop* forward, not replace it with a single read. Every normal path through the
    // wizard goes through it — selecting a source, confirming a plan, cancelling — so a refresh that ended the
    // polling would leave the progress step frozen at the moment the import actually started.
    it('keeps polling after a refresh', async () => {
        const get = jest.spyOn(importsClient, 'getImportJob').
            mockResolvedValueOnce(makeJob('awaiting_confirmation')).
            mockResolvedValueOnce(makeJob('queued_import')).
            mockResolvedValueOnce(makeJob('importing')).
            mockResolvedValue(makeJob('completed'));

        const {result} = await renderImportJob('job1');
        await waitFor(() => expect(result.current.job?.state).toBe('awaiting_confirmation'));

        await act(async () => {
            await result.current.refresh();
        });
        expect(result.current.job?.state).toBe('queued_import');

        // From here nothing else touches the hook: the job must reach its terminal state on the poll alone.
        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS);
        });
        await waitFor(() => expect(result.current.job?.state).toBe('importing'));

        await act(async () => {
            jest.advanceTimersByTime(IMPORT_POLL_ACTIVE_MS);
        });
        await waitFor(() => expect(result.current.job?.state).toBe('completed'));
        expect(get).toHaveBeenCalledTimes(4);
    });
});

describe('useResumableImportJob', () => {
    const renderResume = async (target: Parameters<typeof useResumableImportJob>[0]) => {
        const rendered = renderHook(() => useResumableImportJob(target));
        await act(async () => {
            await Promise.resolve();
        });
        return rendered;
    };

    afterEach(() => {
        jest.restoreAllMocks();
    });

    const page = (items: ImportJobView[], hasMore = false) => ({items, page: 0, per_page: 50, has_more: hasMore});

    // An import outlives the page that started it. If the only record of which job it is were component state,
    // closing the wizard or reloading would strand a running import out of reach — while admission refused the
    // new upload the user would then reasonably try.
    it('adopts an unfinished import for the same target', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(page([makeJob('importing')]));

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.resolving).toBe(false));
        expect(result.current.jobId).toBe('job1');
    });

    // A finished import is history, not something to resume: the user asking to import again means a new one.
    it('ignores jobs that have already finished', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(page([
            makeJob('completed'),
            makeJob('canceled', {id: 'job2'}),
        ]));

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.resolving).toBe(false));
        expect(result.current.jobId).toBeUndefined();
    });

    // Adopting any unfinished import would show someone a plan for a different Space and invite them to
    // confirm it.
    it('ignores an unfinished import into somewhere else', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(page([
            makeJob('importing', {target: {kind: 'new', team_id: 'other-team', existed: false}}),
            makeJob('importing', {id: 'job2', target: {kind: 'existing', team_id: 'team1', space_id: 'space9', existed: true}}),
        ]));

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.resolving).toBe(false));
        expect(result.current.jobId).toBeUndefined();
    });

    it('matches an existing-Space target by its Space', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(page([
            makeJob('awaiting_confirmation', {target: {kind: 'existing', team_id: 'team1', space_id: 'space9', existed: true}}),
        ]));

        const {result} = await renderResume({kind: 'existing', space_id: 'space9'});
        await waitFor(() => expect(result.current.resolving).toBe(false));
        expect(result.current.jobId).toBe('job1');
    });

    // "No import running" and "I could not find out" are different answers, and only the first justifies
    // offering an upload. Treating a failed lookup as the former is how one import quietly becomes two, since
    // admission allows several per target.
    it('reports a failed lookup instead of claiming nothing is running', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockRejectedValue(new TypeError('network down'));

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.resolving).toBe(false));
        expect(result.current.failed).toBe(true);
        expect(result.current.jobId).toBeUndefined();

        // No status for a transport failure, which is itself the distinction the caller renders.
        expect(result.current.failureStatus).toBeUndefined();
    });

    // The cause has to survive the hook, because 501 (Docs switched off) and 404 (a plugin without the import
    // API) need different actions and look identical from the UI otherwise.
    it('carries the status behind a failed lookup', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockRejectedValue(
            new RestError('/imports', 501, 'Docs is not enabled.', {id: 'api.docs_not_enabled.app_error'}),
        );

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.failed).toBe(true));
        expect(result.current.failureStatus).toBe(501);
    });

    it('finds the job again when a failed lookup is retried', async () => {
        const list = jest.spyOn(importsClient, 'listImportJobs').
            mockRejectedValueOnce(new TypeError('network down')).
            mockResolvedValue(page([makeJob('importing')]));

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.failed).toBe(true));

        await act(async () => {
            result.current.retry();
        });
        await waitFor(() => expect(result.current.jobId).toBe('job1'));
        expect(result.current.failed).toBe(false);
        expect(list).toHaveBeenCalledTimes(2);
    });

    // An unfinished import need not be among the newest jobs: several targets can each hold one, so stopping
    // after the first page can miss it — and missing it offers an upload for an import already in flight.
    it('keeps looking past the first page of jobs', async () => {
        const elsewhere = makeJob('importing', {
            id: 'other',
            target: {kind: 'new', team_id: 'other-team', existed: false},
        });
        jest.spyOn(importsClient, 'listImportJobs').mockImplementation((options) =>
            Promise.resolve(options?.page ?
                page([makeJob('importing', {id: 'wanted'})]) :
                page([elsewhere], true)),
        );

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.jobId).toBe('wanted'));
    });

    it('stops looking when the pages run out', async () => {
        const list = jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(page([]));

        const {result} = await renderResume({kind: 'new', team_id: 'team1'});
        await waitFor(() => expect(result.current.resolving).toBe(false));
        expect(result.current.jobId).toBeUndefined();
        expect(list).toHaveBeenCalledTimes(1);
    });

    // Switching targets must not leave the previous target's import on screen. It is not merely stale: the job
    // it points at belongs to another Space, and the wizard would offer its plan for confirmation and its
    // import for cancellation under a heading about this one.
    it('drops the previous target\'s job the moment the target changes', async () => {
        const list = jest.spyOn(importsClient, 'listImportJobs').
            mockResolvedValueOnce(page([makeJob('awaiting_confirmation', {
                target: {kind: 'existing', team_id: 'team1', space_id: 'spaceA', existed: true},
            })]));

        const {result, rerender} = renderHook(
            ({target}) => useResumableImportJob(target),
            {initialProps: {target: {kind: 'existing', space_id: 'spaceA'} as ImportTargetRequest}},
        );
        await waitFor(() => expect(result.current.jobId).toBe('job1'));

        // The second target's lookup never settles, which is the window that matters.
        list.mockImplementation(() => new Promise(() => {}));
        rerender({target: {kind: 'existing', space_id: 'spaceB'} as ImportTargetRequest});

        expect(result.current.jobId).toBeUndefined();
        expect(result.current.resolving).toBe(true);
    });
});

describe('requiredAcknowledgementsSatisfied', () => {
    // The required set comes from the job. A client offering a fixed list of checkboxes would either miss
    // a key the server demands or send one it refuses, and either makes the import unconfirmable.
    it('requires exactly the keys the job asks for', () => {
        const job = makeJob('awaiting_confirmation', {
            required_acknowledgements: ['confirm_new_space_metadata', 'page_only_partial_import'],
        });

        expect(requiredAcknowledgementsSatisfied(job, {})).toBe(false);
        expect(requiredAcknowledgementsSatisfied(job, {confirm_new_space_metadata: true})).toBe(false);
        expect(requiredAcknowledgementsSatisfied(job, {
            confirm_new_space_metadata: true,
            page_only_partial_import: true,
        })).toBe(true);
    });

    it('is satisfied when the job asks for nothing', () => {
        expect(requiredAcknowledgementsSatisfied(makeJob('awaiting_confirmation'), {})).toBe(true);
    });

    it('is never satisfied without a job', () => {
        expect(requiredAcknowledgementsSatisfied(undefined, {anything: true})).toBe(false);
    });
});
