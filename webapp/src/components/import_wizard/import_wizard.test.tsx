// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import * as importsClient from 'client/imports';
import {RestError} from 'client/rest';
import React from 'react';

import * as actions from 'store/actions';

import type {ImportJobState, ImportJobView, ImportPreflightResultView} from 'types/imports';

import ImportReviewStep from './import_review_step';
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

// An empty page of jobs: this target has no unfinished import, so the wizard starts at the upload step.
const noJobs = () => ({items: [], page: 0, per_page: 20, has_more: false});

// Every render begins by asking the server whether this target already has an import in flight, because an
// import outlives the page that started it. Stubbing "no" by default keeps each test to the flow it is about;
// the resume tests below answer otherwise.
beforeEach(() => {
    jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(noJobs());
});

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

    const input = await screen.findByLabelText('Confluence export bundle');
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

    it('will not upload until a bundle is chosen', async () => {
        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );
        expect(await screen.findByRole('button', {name: 'Upload and inspect'})).toBeDisabled();
    });

    // The chosen filename is rendered by us, not by the browser's file input, whose own text takes a colour the
    // page cannot set and is unreadable on a dark theme. So it has to actually appear.
    it('shows which bundle has been chosen', async () => {
        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByText('No bundle chosen yet')).toBeInTheDocument();

        const input = await screen.findByLabelText('Confluence export bundle');
        await act(async () => {
            fireEvent.change(input, {target: {files: [bundleFile()]}});
        });

        expect(screen.getByText('bundle.zip')).toBeInTheDocument();
        expect(screen.queryByText('No bundle chosen yet')).not.toBeInTheDocument();
    });

    // Reopening the wizard while an import is running must land on that import, not on the upload step. Offering
    // the upload would ask for a second bundle for work already in flight — which admission then refuses — and
    // would leave the running import unreachable, since the only handle on it was this component's state.
    it('resumes a running import instead of asking for another bundle', async () => {
        const running = makeJob('importing', {
            phase: 'writing_pages',
            progress: {phase: 'writing_pages', current: 2, total: 4},
        });
        jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue({
            items: [running], page: 0, per_page: 20, has_more: false,
        });
        const get = jest.spyOn(importsClient, 'getImportJob').mockResolvedValue(running);

        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByText('2 of 4 pages')).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Upload and inspect'})).not.toBeInTheDocument();
        expect(get).toHaveBeenCalledWith('job1');
    });

    // The check for an import already in flight is a courtesy — the server enforces the per-target limit — so a
    // failed check must not disable the feature. Blocking on it meant one broken read left the wizard with
    // nothing but an error and a retry button, which is exactly what shipping it did.
    it('still offers the upload when the check for a running import fails', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockRejectedValue(
            new RestError('/imports', 501, 'Docs is not enabled.', {id: 'api.docs_not_enabled.app_error'}),
        );

        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );

        // The cause is named rather than left as "something went wrong": 501 and 404 need different actions.
        expect(await screen.findByRole('alert')).toHaveTextContent('Docs is not enabled on this server');
        expect(await screen.findByLabelText('Confluence export bundle')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Upload and inspect'})).toBeInTheDocument();
    });

    it('names a server whose plugin has no import API', async () => {
        jest.spyOn(importsClient, 'listImportJobs').mockRejectedValue(
            new RestError('/imports', 404, 'Not found.', {}),
        );

        renderWithContext(
            <ImportWizard
                target={TARGET}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByRole('alert')).toHaveTextContent('does not offer the import API');
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
        const input = await screen.findByLabelText('Confluence export bundle');
        await act(async () => {
            fireEvent.change(input, {target: {files: [bundleFile()]}});
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
        const input = await screen.findByLabelText('Confluence export bundle');
        await act(async () => {
            fireEvent.change(input, {target: {files: [bundleFile()]}});
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

    // Conflicts are fetched by action rather than taken from the plan's first page: they can sit anywhere among
    // thousands of pages, and one at row 3 000 that cannot be reached is one silently decided for the user.
    it('reaches conflicts beyond the first page of the plan', async () => {
        const conflictRow = (id: string): ImportPreflightResultView => ({
            external_id: id,
            title: 'Both edited ' + id,
            planned_action: 'conflict',
            outcome: 'conflict_skipped',
            overwrite_eligible: true,
        });
        const results = jest.spyOn(importsClient, 'getImportPreflightResults').mockImplementation((_jobId, options) => {
            if (options?.plannedAction !== 'conflict') {
                // The plan's own first page happens to contain no conflicts at all.
                return Promise.resolve({items: [], page: 0, per_page: 100, has_more: true});
            }
            return Promise.resolve(options?.page ?
                {items: [conflictRow('900')], page: 1, per_page: 100, has_more: false} :
                {items: [conflictRow('101')], page: 0, per_page: 100, has_more: true});
        });
        const confirmSpy = jest.spyOn(importsClient, 'confirmImportJob').mockResolvedValue(makeJob('queued_import'));

        await uploadAndReach(reviewJob({required_acknowledgements: []}));

        // A conflict is offered even though the plan page it would appear on was never loaded.
        expect(await screen.findByRole('checkbox', {name: /Both edited 101/})).toBeInTheDocument();
        expect(results).toHaveBeenCalledWith('job1', expect.objectContaining({plannedAction: 'conflict'}));

        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Show more conflicts'}));
        });

        const later = await screen.findByRole('checkbox', {name: /Both edited 900/});
        fireEvent.click(later);
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Start import'}));
        });

        expect(confirmSpy.mock.calls[0][1].overwrite_conflicts).toEqual(['900']);
    });

    // Consent names a plan. When the server recomputes one — which it does on its own, whenever the source
    // changes under a review — approvals given for the old plan must not carry into the new one: the same page
    // may now conflict with different edits by a different person, and the user has not seen that. The step is
    // driven directly here because the change under test is the job prop being replaced, which is exactly what
    // the wizard does when a poll returns a new revision.
    it('withdraws consent when the plan is recomputed, and waits for the new rows', async () => {
        const conflict: ImportPreflightResultView = {
            external_id: '101',
            title: 'Both edited',
            planned_action: 'conflict',
            outcome: 'conflict_skipped',
            overwrite_eligible: true,
        };
        jest.spyOn(importsClient, 'getImportPreflightResults').mockResolvedValue({
            items: [conflict], page: 0, per_page: 100, has_more: false,
        });

        const reviewing = (job: ImportJobView) => (
            <ImportReviewStep
                job={job}
                onConfirmed={jest.fn()}
            />
        );
        const first = reviewJob({required_acknowledgements: ['page_only_partial_import']});
        const {rerender} = renderWithContext(reviewing(first));

        fireEvent.click(await screen.findByRole('checkbox', {name: /Comments and attachments/}));
        fireEvent.click(await screen.findByRole('checkbox', {name: /Both edited/}));
        expect(screen.getByRole('button', {name: 'Start import'})).toBeEnabled();

        // A recomputed plan arrives: same job, different revision.
        const republished = reviewJob({required_acknowledgements: ['page_only_partial_import']});
        republished.preflight = {...republished.preflight!, revision: 'b'.repeat(64)};
        await act(async () => {
            rerender(reviewing(republished));
        });

        await waitFor(() => {
            expect(screen.getByRole('checkbox', {name: /Comments and attachments/})).not.toBeChecked();
        });
        expect(screen.getByRole('checkbox', {name: /Both edited/})).not.toBeChecked();

        // And it stays unconfirmable until the acknowledgements are given again for this plan.
        expect(screen.getByRole('button', {name: 'Start import'})).toBeDisabled();
    });

    // A page of conflicts requested against one plan must not be appended to another. The stale rows would sit
    // in the approvable list looking exactly as current as the rest — and the loaded-revision gate cannot help,
    // because by then it matches the *new* plan.
    it('drops a page of conflicts that arrives after the plan changed', async () => {
        const conflictRow = (id: string): ImportPreflightResultView => ({
            external_id: id,
            title: 'Both edited ' + id,
            planned_action: 'conflict',
            outcome: 'conflict_skipped',
            overwrite_eligible: true,
        });

        let releaseSecondPage: (value: {items: ImportPreflightResultView[]; page: number; per_page: number; has_more: boolean}) => void = () => {};
        jest.spyOn(importsClient, 'getImportPreflightResults').mockImplementation((_jobId, options) => {
            if (options?.plannedAction !== 'conflict') {
                return Promise.resolve({items: [], page: 0, per_page: 100, has_more: false});
            }
            if (options?.page) {
                // Held open so the plan can change while this request is in flight.
                return new Promise((resolve) => {
                    releaseSecondPage = resolve;
                });
            }
            return Promise.resolve({items: [conflictRow('101')], page: 0, per_page: 100, has_more: true});
        });

        const first = reviewJob({required_acknowledgements: []});
        const reviewing = (job: ImportJobView) => (
            <ImportReviewStep
                job={job}
                onConfirmed={jest.fn()}
            />
        );
        const {rerender} = renderWithContext(reviewing(first));

        const showMore = await screen.findByRole('button', {name: 'Show more conflicts'});
        await act(async () => {
            fireEvent.click(showMore);
        });

        // The plan is republished while the second page is still outstanding, and then the page lands.
        const republished = reviewJob({required_acknowledgements: []});
        republished.preflight = {...republished.preflight!, revision: 'b'.repeat(64)};
        await act(async () => {
            rerender(reviewing(republished));
        });
        await act(async () => {
            releaseSecondPage({items: [conflictRow('900')], page: 1, per_page: 100, has_more: false});
        });

        expect(screen.queryByRole('checkbox', {name: /Both edited 900/})).not.toBeInTheDocument();
    });

    // A read that fails must not disable confirmation for good. The load only re-runs when the revision changes,
    // and polling returns the same revision, so without a retry there is nothing to wait for and nothing to press.
    it('offers a retry when the review rows cannot be read', async () => {
        const results = jest.spyOn(importsClient, 'getImportPreflightResults').
            mockRejectedValueOnce(new TypeError('network down')).
            mockRejectedValueOnce(new TypeError('network down')).
            mockResolvedValue({items: [], page: 0, per_page: 100, has_more: false});

        await uploadAndReach(reviewJob({required_acknowledgements: []}));

        expect(await screen.findByRole('alert')).toHaveTextContent('could not be read');
        expect(screen.getByRole('button', {name: 'Start import'})).toBeDisabled();

        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Try again'}));
        });

        await waitFor(() => expect(screen.getByRole('button', {name: 'Start import'})).toBeEnabled());
        expect(results.mock.calls.length).toBeGreaterThan(2);
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
    // An import writes Spaces and pages behind the store's back, so a finished one leaves this product's view of
    // them stale: the sidebar would not list the new Space, and the link to it would land on an empty home.
    it('reloads the store once an import has written its Space', async () => {
        const spaces = jest.spyOn(actions, 'fetchSpaces');
        const pages = jest.spyOn(actions, 'fetchPages');

        await uploadAndReach(makeJob('completed_with_issues', {
            target: {kind: 'new', team_id: 'team1', space_id: 'newspace', existed: false},
            final: undefined,
        }));

        await waitFor(() => expect(spaces).toHaveBeenCalled());
        expect(pages).toHaveBeenCalledWith('newspace');
    });

    // A job that stopped short may have had its half-built Space cleaned up, so there is nothing to reload and
    // nowhere to point at.
    it('does not reload the store for an import that did not finish', async () => {
        const spaces = jest.spyOn(actions, 'fetchSpaces');

        await uploadAndReach(makeJob('failed', {
            target: {kind: 'new', team_id: 'team1', space_id: 'newspace', existed: false},
            error: {code: 'execution_failed'},
        }));

        await screen.findByText('Import stopped');
        expect(spaces).not.toHaveBeenCalled();
    });

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
        const input = await screen.findByLabelText('Confluence export bundle');
        await act(async () => {
            fireEvent.change(input, {target: {files: [bundleFile()]}});
        });
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Upload and inspect'}));
        });

        expect(await screen.findByText('This import is no longer available.')).toBeInTheDocument();
    });
});
