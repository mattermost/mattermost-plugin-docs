// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import * as importsClient from 'client/imports';
import {RestError} from 'client/rest';
import React from 'react';

import type {ImportJobState, ImportJobView} from 'types/imports';

import {isCancellable, stepForJob, stepsForTarget} from './import_steps';
import ImportWizard from './import_wizard';

import {renderWithContext} from '../../../tests/react_testing_utils';

const TARGET = {kind: 'new', team_id: 'team1'} as const;

const makeJob = (state: ImportJobState, overrides: Partial<ImportJobView> = {}): ImportJobView => ({
    id: 'job1',
    state,
    phase: '',
    progress: {phase: '', current: 0, total: 0},
    target: {kind: 'new', team_id: 'team1', existed: false},
    bundle: {
        version: 2,
        source: {organization_id: '', space_key: 'DOCS', space_name: 'Docs'},
        space_defaults: {title: 'Imported Docs', description: 'From Confluence'},
        counts: {
            pages: 3,
            comments: 1,
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

const bundleFile = () => new File([new Uint8Array([1, 2, 3])], 'bundle.zip', {type: 'application/zip'});

// uploadAndReach uploads a bundle and settles on whatever step the returned job implies, which is how the
// wizard is always entered: there is no way to reach a later step without a job.
const uploadAndReach = async (job: ImportJobView) => {
    jest.spyOn(importsClient, 'uploadImportBundle').mockResolvedValue(job);
    jest.spyOn(importsClient, 'getImportJob').mockResolvedValue(job);

    renderWithContext(
        <ImportWizard
            target={TARGET}
            onClose={jest.fn()}
        />,
    );

    const input = screen.getByLabelText('Confluence export bundle');
    await act(async () => {
        fireEvent.change(input, {target: {files: [bundleFile()]}});
    });
    await act(async () => {
        fireEvent.click(screen.getByRole('button', {name: 'Upload and inspect'}));
    });
};

describe('stepForJob', () => {
    // The step is derived from the job because an import outlives the page that started it — and because the
    // server can move it backwards, which local step state could never follow.
    it('maps each state onto the step the user can act on', () => {
        expect(stepForJob(undefined)).toBe('upload');
        expect(stepForJob(makeJob('awaiting_source'))).toBe('source');
        expect(stepForJob(makeJob('awaiting_confirmation'))).toBe('review');
        expect(stepForJob(makeJob('completed'))).toBe('done');
        expect(stepForJob(makeJob('canceled'))).toBe('done');
    });

    // Every worker-owned state is one step: there is nothing different to do while a plan is computed versus
    // while pages are written.
    it.each<ImportJobState>(['queued_preflight', 'preflighting', 'queued_import', 'importing', 'terminalizing'])(
        'treats %s as one running step',
        (state) => {
            expect(stepForJob(makeJob(state))).toBe('running');
        },
    );

    // A confirmed plan whose source changed is sent back by the server. The wizard must follow it back rather
    // than stay on a progress screen for a job that is waiting on the user again.
    it('follows the job backwards when the server rewinds it', () => {
        expect(stepForJob(makeJob('queued_import'))).toBe('running');
        expect(stepForJob(makeJob('awaiting_confirmation'))).toBe('review');
    });

    it('hides the source step for a new Space, which never stops to ask', () => {
        expect(stepsForTarget(makeJob('queued_preflight'))).not.toContain('source');
        expect(stepsForTarget(makeJob('awaiting_source', {
            target: {kind: 'existing', team_id: 'team1', space_id: 'space1', existed: true},
        }))).toContain('source');
    });

    it('allows cancelling anything that is not finished', () => {
        expect(isCancellable(makeJob('importing'))).toBe(true);
        expect(isCancellable(makeJob('awaiting_confirmation'))).toBe(true);
        expect(isCancellable(makeJob('completed'))).toBe(false);
        expect(isCancellable(undefined)).toBe(false);
    });
});

describe('ImportWizard upload step', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    it('will not upload until a bundle is chosen', () => {
        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );
        expect(screen.getByRole('button', {name: 'Upload and inspect'})).toBeDisabled();
    });

    // An admission rejection must say when to try again; without the wait, the only advice available is
    // "retry now", which fails identically.
    it('reports an admission rejection with the wait the server asked for', async () => {
        jest.spyOn(importsClient, 'uploadImportBundle').mockRejectedValue(
            new importsClient.ImportAdmissionError('/imports/preflight', 429, 'Too many imports.', {id: 'x'}, 'x', 90),
        );

        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );
        await act(async () => {
            fireEvent.change(screen.getByLabelText('Confluence export bundle'), {target: {files: [bundleFile()]}});
        });
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Upload and inspect'}));
        });

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Too many imports are in progress.');
        expect(alert).toHaveTextContent('Try again in 90 seconds.');
    });

    // A rejected bundle shares one message id with every other rejected bundle, so the stable code is what
    // tells anyone which rule it broke.
    it('surfaces the importer code for a rejected bundle', async () => {
        jest.spyOn(importsClient, 'uploadImportBundle').mockRejectedValue(
            new RestError('/imports/preflight', 422, 'Over a limit.', {
                id: 'app.import.bundle_content_not_processable.app_error',
                params: {Code: 'too_many_pages'},
            }, 'app.import.bundle_content_not_processable.app_error'),
        );

        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );
        await act(async () => {
            fireEvent.change(screen.getByLabelText('Confluence export bundle'), {target: {files: [bundleFile()]}});
        });
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Upload and inspect'}));
        });

        expect(await screen.findByRole('alert')).toHaveTextContent('too_many_pages');
    });
});

describe('ImportWizard review step', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    const reviewJob = (overrides: Partial<ImportJobView> = {}) => makeJob('awaiting_confirmation', {
        preflight: {
            stage: 'preflight',
            generated_at: 1,
            revision: 'a'.repeat(64),
            fidelity: {
                scope: 'pages_only',
                comments: 'counted_not_imported',
                attachments: 'counted_not_imported',
                restricted_emitted_pages: 'space_level_access_if_present',
                restricted_manifest_only_entries: 'reported_not_imported',
                full_fidelity: false,
            },
            counts: {
                pages: 3,
                comments: 1,
                attachments: 0,
                restricted_manifest_total: 0,
                restricted_emitted_pages: 0,
                restricted_manifest_only: 0,
                actions: {create: 2, conflict: 1},
            },
        },
        ...overrides,
    });

    // The required set comes from the job. A fixed list of checkboxes would eventually miss a key the server
    // demands, or offer one it refuses — and either makes the import unconfirmable.
    it('renders exactly the acknowledgements the job asks for and gates confirm on them', async () => {
        jest.spyOn(importsClient, 'getImportPreflightResults').mockResolvedValue({
            items: [], page: 0, per_page: 100, has_more: false,
        });
        await uploadAndReach(reviewJob({
            required_acknowledgements: ['confirm_new_space_metadata', 'page_only_partial_import'],
        }));

        const confirm = await screen.findByRole('button', {name: 'Start import'});
        expect(confirm).toBeDisabled();

        const newSpaceAck = screen.getByRole('checkbox', {name: /A new Space will be created/});
        const pageOnlyAck = screen.getByRole('checkbox', {name: /Comments and attachments/});

        // One of two is not enough.
        fireEvent.click(newSpaceAck);
        expect(confirm).toBeDisabled();

        fireEvent.click(pageOnlyAck);
        expect(confirm).toBeEnabled();

        // And nothing the job did not ask for is offered.
        expect(screen.queryByRole('checkbox', {name: /restricted in Confluence/})).not.toBeInTheDocument();
    });

    // Each approval discards a specific person's edits, so they are per page and default to off. There is no
    // approve-all, by design.
    it('sends only the conflicts the user ticked, and the revision it was shown', async () => {
        jest.spyOn(importsClient, 'getImportPreflightResults').mockResolvedValue({
            items: [
                {external_id: '101', title: 'Both edited', planned_action: 'conflict', outcome: 'conflict_skipped', overwrite_eligible: true},
                {external_id: '102', title: 'Also both', planned_action: 'conflict', outcome: 'conflict_skipped', overwrite_eligible: true},
                {external_id: '103', title: 'Fresh', planned_action: 'create', outcome: 'created'},
            ],
            page: 0,
            per_page: 100,
            has_more: false,
        });
        const confirmSpy = jest.spyOn(importsClient, 'confirmImportJob').mockResolvedValue(makeJob('queued_import'));

        await uploadAndReach(reviewJob({required_acknowledgements: []}));

        // Only the approvable rows get a checkbox; a plain create is not a decision.
        const first = await screen.findByRole('checkbox', {name: /Both edited/});
        expect(screen.queryByRole('checkbox', {name: /Fresh/})).not.toBeInTheDocument();

        fireEvent.click(first);
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Start import'}));
        });

        expect(confirmSpy).toHaveBeenCalledTimes(1);
        const request = confirmSpy.mock.calls[0][1];
        expect(request.preflight_revision).toBe('a'.repeat(64));
        expect(request.overwrite_conflicts).toEqual(['101']);
        expect(request.new_space).toEqual({title: 'Imported Docs', description: 'From Confluence'});
    });

    // The server has already requeued the job, so there is nothing to retry: the message has to say that
    // rather than invite a click that cannot work.
    it('explains a stale plan instead of offering a retry', async () => {
        jest.spyOn(importsClient, 'getImportPreflightResults').mockResolvedValue({
            items: [], page: 0, per_page: 100, has_more: false,
        });
        jest.spyOn(importsClient, 'confirmImportJob').mockRejectedValue(
            new RestError('/confirm', 409, 'stale', {
                error: {
                    id: 'app.import.confirm.preflight_stale_recomputing.app_error',
                    params: {Code: 'preflight_stale_recomputing'},
                },
            }),
        );

        await uploadAndReach(reviewJob({required_acknowledgements: []}));
        await act(async () => {
            fireEvent.click(await screen.findByRole('button', {name: 'Start import'}));
        });

        expect(await screen.findByRole('alert')).toHaveTextContent('being recalculated');
    });
});

describe('ImportWizard running and result steps', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    it('shows measured progress once there is a total to measure against', async () => {
        await uploadAndReach(makeJob('importing', {
            phase: 'writing_pages',
            progress: {phase: 'writing_pages', current: 3, total: 12},
        }));

        expect(await screen.findByText('Importing pages…')).toBeInTheDocument();
        const bar = screen.getByRole('progressbar', {name: 'Import progress'});
        expect(bar).toHaveAttribute('aria-valuenow', '3');
        expect(bar).toHaveAttribute('aria-valuemax', '12');
    });

    // Before pages start, a count would be meaningless, so none is shown rather than a bar stuck at zero.
    it('omits the bar while there is nothing to measure', async () => {
        await uploadAndReach(makeJob('preflighting', {phase: 'computing_actions'}));

        expect(await screen.findByText('Working out what this will change…')).toBeInTheDocument();
        expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    });

    // completed_with_issues is the common outcome for a real Space; presenting it as a clean success would
    // hide the findings the user needs to look at.
    it('distinguishes a finish with issues from a clean one, and offers both reports', async () => {
        await uploadAndReach(makeJob('completed_with_issues', {
            final: {
                stage: 'execution',
                generated_at: 2,
                fidelity: {
                    scope: 'pages_only',
                    comments: 'counted_not_imported',
                    attachments: 'counted_not_imported',
                    restricted_emitted_pages: 'space_level_access_if_present',
                    restricted_manifest_only_entries: 'reported_not_imported',
                    full_fidelity: false,
                },
                counts: {
                    pages: 3,
                    comments: 1,
                    attachments: 0,
                    restricted_manifest_total: 0,
                    restricted_emitted_pages: 0,
                    restricted_manifest_only: 0,
                    actions: {create: 2, preserve_local: 1},
                    outcomes: {created: 2, local_preserved: 1},
                },
            },
            preflight: {stage: 'preflight',
                generated_at: 1,
                revision: 'b'.repeat(64),
                fidelity: {
                    scope: 'pages_only',
                    comments: 'counted_not_imported',
                    attachments: 'counted_not_imported',
                    restricted_emitted_pages: 'space_level_access_if_present',
                    restricted_manifest_only_entries: 'reported_not_imported',
                    full_fidelity: false,
                },
                counts: {
                    pages: 3,
                    comments: 1,
                    attachments: 0,
                    restricted_manifest_total: 0,
                    restricted_emitted_pages: 0,
                    restricted_manifest_only: 0,
                    actions: {},
                }},
        }));

        expect(await screen.findByText('Import finished, with things to review')).toBeInTheDocument();
        expect(screen.getByText('your version kept')).toBeInTheDocument();
        expect(screen.getByRole('link', {name: 'Download the full report'})).toBeInTheDocument();
        expect(screen.getByRole('link', {name: 'Download the plan you approved'})).toBeInTheDocument();

        // A finished job cannot be cancelled.
        expect(screen.queryByRole('button', {name: 'Cancel import'})).not.toBeInTheDocument();
    });

    it('reports the stable reason a job stopped', async () => {
        await uploadAndReach(makeJob('failed', {error: {code: 'authorization_revoked'}}));

        expect(await screen.findByText('Import stopped')).toBeInTheDocument();
        expect(screen.getByRole('alert')).toHaveTextContent('authorization_revoked');
    });

    it('cancels a running import and refreshes rather than guessing the outcome', async () => {
        const cancel = jest.spyOn(importsClient, 'cancelImportJob').mockResolvedValue(makeJob('terminalizing'));
        await uploadAndReach(makeJob('importing', {progress: {phase: 'writing_pages', current: 1, total: 4}}));

        await act(async () => {
            fireEvent.click(await screen.findByRole('button', {name: 'Cancel import'}));
        });
        expect(cancel).toHaveBeenCalledWith('job1');

        // The wizard does not assume "canceled": the server decides whether committed pages need reconciling
        // first, so the state is re-read.
        await waitFor(() => expect(importsClient.getImportJob).toHaveBeenCalled());
    });

    // Actor-only visibility makes "gone" and "not yours" deliberately indistinguishable, so the message
    // claims neither.
    it('reports an unavailable job without guessing why', async () => {
        jest.spyOn(importsClient, 'uploadImportBundle').mockResolvedValue(makeJob('queued_preflight'));
        jest.spyOn(importsClient, 'getImportJob').mockRejectedValue(
            new RestError('/imports/job1', 404, 'Not found.', {id: 'app.store.not_found.app_error'}),
        );

        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );
        await act(async () => {
            fireEvent.change(screen.getByLabelText('Confluence export bundle'), {target: {files: [bundleFile()]}});
        });
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Upload and inspect'}));
        });

        expect(await screen.findByText('This import is no longer available.')).toBeInTheDocument();
    });
});
