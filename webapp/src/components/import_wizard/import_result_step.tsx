// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {importReportUrl} from 'client/imports';
import React from 'react';
import {useIntl} from 'react-intl';

import {PrimaryButton} from 'components/form_controls/button';

import type {ImportJobView} from 'types/imports';

import styles from './import_wizard.module.scss';

type Props = {
    job: ImportJobView;
    onDone: () => void;
};

// ImportResultStep is the report.
//
// It never says "success" unqualified. completed_with_issues is the common outcome for a real Space — a preserved
// local edit, an author who could not be matched, comments that were counted but not imported — and presenting
// that as a clean success would hide exactly the findings the user needs to look at.
const ImportResultStep = ({job, onDone}: Props) => {
    const {formatMessage} = useIntl();
    const counts = job.final?.counts;

    return (
        <div className={styles.step}>
            <h3 className={styles.stepTitle}>{outcomeTitle(job, formatMessage)}</h3>

            {job.error ? (
                <p
                    role='alert'
                    className={styles.error}
                >
                    {formatMessage(
                        {id: 'docs.import.result.reason', defaultMessage: 'Reason: {code}'},
                        {code: job.error.code},
                    )}
                </p>
            ) : null}

            {counts ? (
                <ul className={styles.counts}>
                    {Object.entries(counts.outcomes ?? {}).map(([outcome, value]) => (
                        <li
                            key={outcome}
                            className={styles.count}
                        >
                            <span className={styles.countValue}>{value}</span>
                            <span className={styles.countLabel}>{outcomeLabel(outcome, formatMessage)}</span>
                        </li>
                    ))}
                </ul>
            ) : null}

            {/* Both reports stay downloadable: the plan and the outcome answer different questions, and
                comparing them is how a user checks that what happened is what they approved. Plain links,
                because the server streams these as attachments and one can be megabytes. */}
            <div className={styles.reportLinks}>
                <a
                    className={styles.link}
                    href={importReportUrl(job.id, 'final')}
                    download={true}
                >
                    {formatMessage({id: 'docs.import.result.downloadFinal', defaultMessage: 'Download the full report'})}
                </a>
                {job.preflight ? (
                    <a
                        className={styles.link}
                        href={importReportUrl(job.id, 'preflight')}
                        download={true}
                    >
                        {formatMessage({id: 'docs.import.result.downloadPreflight', defaultMessage: 'Download the plan you approved'})}
                    </a>
                ) : null}
            </div>

            <div className={styles.actions}>
                <PrimaryButton
                    type='button'
                    onClick={onDone}
                >
                    {formatMessage({id: 'docs.import.result.done', defaultMessage: 'Done'})}
                </PrimaryButton>
            </div>
        </div>
    );
};

// outcomeTitle distinguishes the four terminal states, keeping "with issues" visibly different from clean.
function outcomeTitle(job: ImportJobView, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (job.state) {
    case 'completed':
        return formatMessage({id: 'docs.import.result.completed', defaultMessage: 'Import finished'});
    case 'completed_with_issues':
        return formatMessage({id: 'docs.import.result.completedWithIssues', defaultMessage: 'Import finished, with things to review'});
    case 'canceled':
        return formatMessage({id: 'docs.import.result.canceled', defaultMessage: 'Import canceled'});
    default:
        return formatMessage({id: 'docs.import.result.failed', defaultMessage: 'Import stopped'});
    }
}

// outcomeLabel names a per-page outcome in a reader's terms.
function outcomeLabel(outcome: string, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (outcome) {
    case 'created':
        return formatMessage({id: 'docs.import.outcome.created', defaultMessage: 'pages created'});
    case 'updated':
        return formatMessage({id: 'docs.import.outcome.updated', defaultMessage: 'pages updated'});
    case 'unchanged':
        return formatMessage({id: 'docs.import.outcome.unchanged', defaultMessage: 'already up to date'});
    case 'local_preserved':
        return formatMessage({id: 'docs.import.outcome.localPreserved', defaultMessage: 'your version kept'});
    case 'conflict_skipped':
        return formatMessage({id: 'docs.import.outcome.conflictSkipped', defaultMessage: 'left alone (edited in both places)'});
    case 'blocked':
        return formatMessage({id: 'docs.import.outcome.blocked', defaultMessage: 'skipped'});
    case 'stale':
        return formatMessage({id: 'docs.import.outcome.stale', defaultMessage: 'no longer in Confluence'});
    case 'not_attempted_canceled':
    case 'not_attempted_failed':
        return formatMessage({id: 'docs.import.outcome.notAttempted', defaultMessage: 'not attempted'});
    default:
        return outcome;
    }
}

export default ImportResultStep;
