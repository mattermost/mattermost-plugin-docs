// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import type {ImportJobPhase, ImportJobView} from 'types/imports';

import styles from './import_wizard.module.scss';

type Props = {
    job: ImportJobView;
};

// ImportProgressStep is what the user watches while the worker owns the job.
//
// It covers every worker-owned state rather than one per state, because the difference between them is a label:
// there is nothing different to *do* while a plan is computed versus while pages are written. What does differ
// is whether a count is meaningful, which is why the bar only appears once there is a total to measure against.
const ImportProgressStep = ({job}: Props) => {
    const {formatMessage} = useIntl();
    const {current, total} = job.progress;
    const measurable = total > 0;
    const percent = measurable ? Math.min(100, Math.round((current / total) * 100)) : 0;

    return (
        <div className={styles.step}>
            <h3 className={styles.stepTitle}>{phaseTitle(job, formatMessage)}</h3>

            {measurable ? (
                <>
                    <div
                        className={styles.progressTrack}
                        role='progressbar'
                        aria-valuemin={0}
                        aria-valuemax={total}
                        aria-valuenow={current}
                        aria-label={formatMessage({id: 'docs.import.progress.label', defaultMessage: 'Import progress'})}
                    >
                        <div
                            className={styles.progressFill}
                            style={{width: `${percent}%`}}
                        />
                    </div>
                    <p className={styles.hint}>
                        {formatMessage(
                            {id: 'docs.import.progress.count', defaultMessage: '{current} of {total} pages'},
                            {current, total},
                        )}
                    </p>
                </>
            ) : (
                <p className={styles.hint}>
                    {formatMessage({
                        id: 'docs.import.progress.indeterminate',
                        defaultMessage: 'This can take a few minutes for a large Space. You can leave this page — the import keeps running.',
                    })}
                </p>
            )}
        </div>
    );
};

// phaseTitle says what is happening now. The phase is advisory — the state is what gates the UI — so an
// unrecognized one falls back to something true rather than to the raw value.
function phaseTitle(job: ImportJobView, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (job.phase as ImportJobPhase) {
    case 'inspecting':
        return formatMessage({id: 'docs.import.phase.inspecting', defaultMessage: 'Reading the bundle…'});
    case 'resolving_users':
        return formatMessage({id: 'docs.import.phase.resolvingUsers', defaultMessage: 'Matching Confluence authors…'});
    case 'computing_actions':
        return formatMessage({id: 'docs.import.phase.computingActions', defaultMessage: 'Working out what this will change…'});
    case 'provisioning_space':
        return formatMessage({id: 'docs.import.phase.provisioning', defaultMessage: 'Creating the Space…'});
    case 'writing_pages':
        return formatMessage({id: 'docs.import.phase.writingPages', defaultMessage: 'Importing pages…'});
    case 'finalizing':
        return formatMessage({id: 'docs.import.phase.finalizing', defaultMessage: 'Finishing up…'});
    default:
        return formatMessage({id: 'docs.import.phase.working', defaultMessage: 'Working…'});
    }
}

export default ImportProgressStep;
