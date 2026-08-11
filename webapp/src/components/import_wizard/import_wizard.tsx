// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {cancelImportJob} from 'client/imports';
import {useImportJob, useResumableImportJob} from 'hooks/imports';
import {useAppDispatch} from 'hooks/redux';
import React, {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';

import {fetchPages, fetchSpaces} from 'store/actions';

import type {ImportJobView, ImportTargetRequest} from 'types/imports';

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
    //
    // importedSpaceId is the Space the finished import filled, when there is one to go and look at. An import
    // into a new Space otherwise ends by putting the user back where they started, with no sign of the thing
    // they just spent minutes creating.
    onClose: (importedSpaceId?: string) => void;
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
    const {jobId, resolving, failed: lookupFailed, failureStatus, adopt, retry} = useResumableImportJob(target);
    const {job, loading, error, refresh} = useImportJob(jobId);
    const [cancelling, setCancelling] = useState(false);

    const step = stepForJob(job);
    const steps = stepsForTarget(job);

    // An import writes Spaces and pages straight to the database, so the store holding this product's view of
    // them is stale the moment it finishes — the sidebar would not list a new Space, and following the link to it
    // would land on an empty product home. Reloading while the user is still reading the report means the Space
    // is there by the time they leave it.
    //
    // This is currently a no-op in practice: the store is still fed from the mock data source, which knows
    // nothing the server wrote. It becomes real with the API-backed data source (PR #12), and is written now so
    // that landing on a freshly imported Space is not a step someone has to remember later.
    const dispatch = useAppDispatch();
    const importedSpace = job ? importedSpaceId(job) : undefined;
    useEffect(() => {
        if (!importedSpace) {
            return;
        }
        dispatch(fetchSpaces());
        dispatch(fetchPages(importedSpace));
    }, [dispatch, importedSpace]);

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

            {/* A lookup that could not answer is reported rather than assumed — "nothing is running" and "I could
                not find out" are different answers. It does not block the upload, though: the server enforces the
                per-target job limit, so this check saves a wasted upload rather than preventing a bad one, and
                letting an unrelated read decide whether the feature works at all is the worse failure. */}
            {lookupFailed ? (
                <div
                    role='alert'
                    className={styles.error}
                >
                    <p className={styles.errorLine}>{lookupFailureMessage(failureStatus, formatMessage)}</p>
                    <p className={styles.errorLine}>
                        {formatMessage({
                            id: 'docs.import.lookupFailedProceed',
                            defaultMessage: 'You can still upload a bundle. If an import is already running here, the server will refuse it.',
                        })}
                    </p>
                    <button
                        type='button'
                        className={styles.secondary}
                        onClick={retry}
                    >
                        {formatMessage({id: 'docs.import.lookupRetry', defaultMessage: 'Check again'})}
                    </button>
                </div>
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

                    // Called with no argument on purpose: leaving mid-import goes back where the user was, not to
                    // a Space the import has not finished filling. Passing the handler directly would hand it a
                    // MouseEvent as the destination.
                    onClick={() => onClose()}
                >
                    {formatMessage({id: 'docs.import.close', defaultMessage: 'Close'})}
                </button>
            </div>
        </section>
    );

    function renderStep() {
        if (!job) {
            // The upload waits for the lookup to settle, but not for it to succeed. A failed check is shown above
            // and the upload offered anyway; refusing to proceed would let one broken read disable the feature.
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
                    onDone={() => onClose(importedSpaceId(job))}
                />
            );
        default:
            return <ImportProgressStep job={job}/>;
        }
    }
};

// lookupFailureMessage names the cause when it can, because the likely ones need different actions and are
// otherwise indistinguishable: Docs switched off server-side, a plugin build without the import API, a session
// that has expired, or a network that did not answer.
function lookupFailureMessage(status: number | undefined, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (status) {
    case 501:
        return formatMessage({
            id: 'docs.import.lookup.notEnabled',
            defaultMessage: 'Docs is not enabled on this server, so imports cannot run.',
        });
    case 404:
        return formatMessage({
            id: 'docs.import.lookup.notFound',
            defaultMessage: 'This server does not offer the import API. Its Docs plugin may be older than this page.',
        });
    case 401:
    case 403:
        return formatMessage({
            id: 'docs.import.lookup.forbidden',
            defaultMessage: 'You are not allowed to list imports here. Your session may have expired.',
        });
    case undefined:
        return formatMessage({
            id: 'docs.import.lookup.unreachable',
            defaultMessage: 'The server could not be reached to check for a running import.',
        });
    default:
        return formatMessage(
            {id: 'docs.import.lookup.failed', defaultMessage: 'Could not check for a running import (error {status}).'},
            {status},
        );
    }
}

// importedSpaceId returns the Space a finished import wrote into, if it is somewhere the user can now go.
//
// Only a completed import qualifies. A failed or canceled one may have created a Space, or may have had its
// half-built one cleaned up, and sending someone to a Space that no longer exists is worse than not offering.
// The id can also be absent entirely: a job whose actor has lost access to the target comes back as a minimal
// projection with no target detail at all.
function importedSpaceId(job: ImportJobView): string | undefined {
    const completed = job.state === 'completed' || job.state === 'completed_with_issues';
    return completed ? job.target?.space_id || undefined : undefined;
}

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
