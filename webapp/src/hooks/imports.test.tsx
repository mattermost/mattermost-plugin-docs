// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';
import * as importsClient from 'client/imports';
import {RestError} from 'client/rest';

import type {ImportJobState, ImportJobView} from 'types/imports';

import {
    IMPORT_POLL_ACTIVE_MS,
    IMPORT_POLL_IDLE_MS,
    pollIntervalFor,
    requiredAcknowledgementsSatisfied,
    useImportJob,
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
