// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {cancelImportJob} from 'client/imports';
import {useImportJob, useResumableImportJob} from 'hooks/imports';
import React, {useCallback, useState} from 'react';
import {useIntl} from 'react-intl';

import type {ImportTargetRequest} from 'types/imports';

import ImportProgressStep from './import_progress_step';
import ImportResultStep from './import_result_step';
import ImportReviewStep from './import_review_step';
import ImportSourceStep from './import_source_step';
import {isCancellable, stepForJob, stepsForTarget} from './import_steps';
import ImportUploadStep from './import_upload_step';
import styles from './import_wizard.module.scss';

type Props = {

    // target is where the import will go, resolved before the wizard opens: the server authorizes it on upload
    // and the wizard never lets it change mid-flight, because the job is bound to it from the first request.
    target: ImportTargetRequest;

    // onClose is called when the user leaves. It is not "cancel": leaving does not stop an import, because a
    // running import is server-side work that outlives the page.
    onClose: () => void;
};

// ImportWizard drives a Confluence import from upload to report.
//
// The step comes from the job, never from local navigation state. An import outlives this component — it survives
// a reload and a server restart — and the server can move it *backwards*: a confirmed plan whose source changed
// goes back to awaiting_confirmation on its own. Local step state would drift from all of that, and the drift
// would be worst exactly when it matters.
const ImportWizard = ({target, onClose}: Props) => {
    const {formatMessage} = useIntl();

    // The job is recovered from the server rather than held only here, so the wizard can be closed, reopened,
    // reloaded or opened elsewhere while an import runs. See useResumableImportJob.
    const {jobId, resolving, adopt} = useResumableImportJob(target);
    const {job, loading, error, refresh} = useImportJob(jobId);
    const [cancelling, setCancelling] = useState(false);

    const step = stepForJob(job);
    const steps = stepsForTarget(job);

    const cancel = useCallback(async () => {
        if (!jobId || cancelling) {
            return;
        }
        setCancelling(true);
        try {
            await cancelImportJob(jobId);
        } catch {
            // A cancel that fails is not worth its own error surface: either the job had already finished, in
            // which case the state below is about to say so, or it is transient and the button can be pressed
            // again. Refreshing tells the truth either way.
        } finally {
            setCancelling(false);
            await refresh();
        }
    }, [jobId, cancelling, refresh]);

    return (
        <section
            className={styles.wizard}
            aria-label={formatMessage({id: 'docs.import.title', defaultMessage: 'Import from Confluence'})}
        >
            <ol className={styles.stepper}>
                {steps.map((name) => (
                    <li
                        key={name}
                        className={name === step ? styles.stepperCurrent : styles.stepperItem}
                        aria-current={name === step ? 'step' : undefined}
                    >
                        {stepLabel(name, formatMessage)}
                    </li>
                ))}
            </ol>

            {/* A job that has vanished, or that this user may no longer see, is terminal for the wizard: there
                is nothing to poll and nothing to retry. Actor-only visibility means "gone" and "not yours" are
                deliberately indistinguishable, so the message claims neither. */}
            {error ? (
                <p
                    role='alert'
                    className={styles.error}
                >
                    {error.status === 404 ? formatMessage({
                        id: 'docs.import.missing',
                        defaultMessage: 'This import is no longer available.',
                    }) : formatMessage({
                        id: 'docs.import.unavailable',
                        defaultMessage: 'The import could not be read. It may still be running.',
                    })}
                </p>
            ) : null}

            {(resolving || loading) && !job ? (
                <p className={styles.hint}>
                    {formatMessage({id: 'docs.import.loading', defaultMessage: 'Loading…'})}
                </p>
            ) : null}

            {renderStep()}

            <div className={styles.footer}>
                {isCancellable(job) ? (
                    <button
                        type='button'
                        className={styles.tertiary}
                        disabled={cancelling}
                        onClick={cancel}
                    >
                        {formatMessage({id: 'docs.import.cancel', defaultMessage: 'Cancel import'})}
                    </button>
                ) : null}
                <button
                    type='button'
                    className={styles.tertiary}
                    onClick={onClose}
                >
                    {formatMessage({id: 'docs.import.close', defaultMessage: 'Close'})}
                </button>
            </div>
        </section>
    );

    function renderStep() {
        if (!job) {
            // Nothing is offered until the lookup for an unfinished import settles: showing the upload first
            // would invite a second bundle for an import that is already running, which admission then refuses.
            if (resolving) {
                return null;
            }
            return (
                <ImportUploadStep
                    target={target}
                    onUploaded={(created) => adopt(created.id)}
                />
            );
        }
        switch (step) {
        case 'source':
            return (
                <ImportSourceStep
                    job={job}
                    onSelected={refresh}
                />
            );
        case 'review':
            return (
                <ImportReviewStep
                    job={job}
                    onConfirmed={refresh}
                />
            );
        case 'done':
            return (
                <ImportResultStep
                    job={job}
                    onDone={onClose}
                />
            );
        default:
            return <ImportProgressStep job={job}/>;
        }
    }
};

// stepLabel names a step for the indicator.
function stepLabel(step: string, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (step) {
    case 'upload':
        return formatMessage({id: 'docs.import.step.upload', defaultMessage: 'Upload'});
    case 'source':
        return formatMessage({id: 'docs.import.step.source', defaultMessage: 'Source'});
    case 'review':
        return formatMessage({id: 'docs.import.step.review', defaultMessage: 'Review'});
    case 'running':
        return formatMessage({id: 'docs.import.step.running', defaultMessage: 'Import'});
    default:
        return formatMessage({id: 'docs.import.step.done', defaultMessage: 'Report'});
    }
}

export default ImportWizard;
