// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {selectImportSource} from 'client/imports';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import type {ImportJobView} from 'types/imports';

import styles from './import_wizard.module.scss';

type Props = {
    job: ImportJobView;
    onSelected: () => void;
};

// ImportSourceStep asks which Confluence Space's history this bundle continues.
//
// The choice is always explicit and never pre-selected, even when a candidate matches perfectly. Two Confluence
// instances can share an organization id, a space key *and* a display name while being genuinely different
// sources, so an automatic pick would silently merge two unrelated page histories into one mapping set — after
// which every future import of either compares against the wrong baselines. The server scores candidates only
// to order this list.
const ImportSourceStep = ({job, onSelected}: Props) => {
    const {formatMessage} = useIntl();

    // undefined means nothing chosen yet, which is distinct from choosing "new": the Continue button stays
    // disabled until the user has actually decided.
    const [choice, setChoice] = useState<string | undefined>();
    const [displayName, setDisplayName] = useState(job.bundle?.source?.space_name ?? '');
    const [submitting, setSubmitting] = useState(false);
    const [failed, setFailed] = useState(false);

    const candidates = job.source_candidates ?? [];
    const isNew = choice === NEW_SOURCE;
    const canContinue = choice !== undefined && (!isNew || displayName.trim() !== '') && !submitting;

    const submit = async () => {
        if (!canContinue) {
            return;
        }
        setSubmitting(true);
        setFailed(false);
        try {
            await selectImportSource(job.id, isNew ? {mode: 'new', display_name: displayName.trim()} : {mode: 'existing', import_source_id: choice});
            onSelected();
        } catch {
            setFailed(true);
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <div className={styles.step}>
            <h3 className={styles.stepTitle}>
                {formatMessage({id: 'docs.import.source.title', defaultMessage: 'Which Confluence Space is this?'})}
            </h3>
            <p className={styles.hint}>
                {formatMessage({
                    id: 'docs.import.source.hint',
                    defaultMessage: 'This decides which pages are treated as updates to an earlier import rather than as new pages. Nothing is matched automatically, because two Confluence Spaces can look identical.',
                })}
            </p>

            <fieldset className={styles.choices}>
                <legend className={styles.srOnly}>
                    {formatMessage({id: 'docs.import.source.legend', defaultMessage: 'Import source'})}
                </legend>

                {candidates.map((candidate) => (
                    <label
                        key={candidate.import_source_id}
                        className={styles.choice}
                    >
                        <input
                            type='radio'
                            name='import-source'
                            value={candidate.import_source_id}
                            checked={choice === candidate.import_source_id}
                            disabled={submitting}
                            onChange={() => setChoice(candidate.import_source_id)}
                        />
                        <span className={styles.choiceBody}>
                            <span className={styles.choiceTitle}>{candidate.display_name}</span>
                            <span className={styles.choiceMeta}>
                                {formatMessage(
                                    {
                                        id: 'docs.import.source.candidateMeta',
                                        defaultMessage: '{key} · {count, plural, one {# page already imported} other {# pages already imported}}',
                                    },
                                    {key: candidate.external_space_key, count: candidate.mapped_page_count},
                                )}
                            </span>
                        </span>
                    </label>
                ))}

                <label className={styles.choice}>
                    <input
                        type='radio'
                        name='import-source'
                        value={NEW_SOURCE}
                        checked={isNew}
                        disabled={submitting}
                        onChange={() => setChoice(NEW_SOURCE)}
                    />
                    <span className={styles.choiceBody}>
                        <span className={styles.choiceTitle}>
                            {formatMessage({id: 'docs.import.source.newTitle', defaultMessage: 'This is a new source'})}
                        </span>
                        <span className={styles.choiceMeta}>
                            {formatMessage({
                                id: 'docs.import.source.newMeta',
                                defaultMessage: 'Every page in the bundle is imported as a new page.',
                            })}
                        </span>
                    </span>
                </label>
            </fieldset>

            {isNew ? (
                <label className={styles.field}>
                    <span className={styles.fieldLabel}>
                        {formatMessage({id: 'docs.import.source.nameLabel', defaultMessage: 'Name for this source'})}
                    </span>
                    <input
                        type='text'
                        className={styles.textInput}
                        value={displayName}
                        disabled={submitting}
                        onChange={(event) => setDisplayName(event.target.value)}
                    />
                </label>
            ) : null}

            {failed ? (
                <p
                    role='alert'
                    className={styles.error}
                >
                    {formatMessage({id: 'docs.import.source.error', defaultMessage: 'That source could not be selected. Try again.'})}
                </p>
            ) : null}

            <div className={styles.actions}>
                <button
                    type='button'
                    className={styles.primary}
                    disabled={!canContinue}
                    onClick={submit}
                >
                    {formatMessage({id: 'docs.import.source.submit', defaultMessage: 'Continue'})}
                </button>
            </div>
        </div>
    );
};

// NEW_SOURCE is the radio value standing for "not one of these", kept distinct from any real source id.
const NEW_SOURCE = '__new__';

export default ImportSourceStep;
