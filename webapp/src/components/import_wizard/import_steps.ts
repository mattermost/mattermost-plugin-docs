// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ImportJobView} from 'types/imports';

// The wizard's steps, and how a job's state maps onto them.
//
// The mapping is derived from the job rather than tracked as local wizard state, and that is the whole design.
// An import outlives the page that started it: it survives a reload, a navigation away, even a server restart,
// and the server can move it backwards — a confirmed plan whose source changed goes back to awaiting_confirmation
// on its own. Local step state would drift from all of that, and the drift is worst exactly when it matters, so
// the job is the single source of truth for "where am I".

export const IMPORT_STEPS = ['upload', 'source', 'review', 'running', 'done'] as const;

export type ImportStep = (typeof IMPORT_STEPS)[number];

// stepForJob returns the step a job belongs on. Without a job, the wizard is still gathering the upload.
export function stepForJob(job: ImportJobView | undefined): ImportStep {
    if (!job) {
        return 'upload';
    }
    switch (job.state) {
    case 'awaiting_source':
        return 'source';
    case 'awaiting_confirmation':
        return 'review';
    case 'completed':
    case 'completed_with_issues':
    case 'failed':
    case 'canceled':
        return 'done';

    // Everything else — queued_preflight, preflighting, queued_import, importing, terminalizing — is the
    // worker's, and the user's only job is to watch or cancel. They are deliberately one step rather than
    // five: the distinction between them is a phase label, not a different thing to do.
    default:
        return 'running';
    }
}

// stepIndex is the step's position for a progress indicator.
export const stepIndex = (step: ImportStep): number => IMPORT_STEPS.indexOf(step);

// isCancellable reports whether the job can still be given back.
//
// Anything not yet terminal can be: the server decides whether that means finishing immediately or handing the
// job to the terminalizer to reconcile pages it already wrote, and the client does not need to know which.
export const isCancellable = (job: ImportJobView | undefined): boolean =>
    Boolean(job) && stepForJob(job) !== 'done';

// stepsForTarget lists the steps a target kind actually visits, so the indicator does not show a step that
// will never happen.
//
// A new Space has exactly one possible source identity, so it never stops to ask: the server sends it straight
// from upload to preflight, and showing a "choose a source" step it will skip would be a lie about the flow.
export const stepsForTarget = (job: ImportJobView | undefined): readonly ImportStep[] => {
    if (job && !job.target.existed) {
        return IMPORT_STEPS.filter((step) => step !== 'source');
    }
    return IMPORT_STEPS;
};
