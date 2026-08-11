// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {confirmImportJob, getImportPreflightResults, isPreflightStale} from 'client/imports';
import {requiredAcknowledgementsSatisfied} from 'hooks/imports';
import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {useIntl} from 'react-intl';

import type {ImportJobView, ImportPreflightResultView} from 'types/imports';

import styles from './import_wizard.module.scss';

type Props = {
    job: ImportJobView;
    onConfirmed: () => void;
};

// How many review rows to load. The server pages these; the wizard shows the first page and the counts, because
// a five-thousand-row table is not how anyone reviews an import — the per-action totals are.
const REVIEW_PAGE_SIZE = 100;

// ImportReviewStep is the last point at which nothing has been written.
//
// Two things here are load-bearing. The revision is echoed back exactly as received, so a confirmation can only
// ever apply to the plan that was actually displayed. And every conflict is approved individually — there is no
// approve-all — because each approval discards a specific person's edits.
const ImportReviewStep = ({job, onConfirmed}: Props) => {
    const {formatMessage} = useIntl();
    const [results, setResults] = useState<ImportPreflightResultView[]>([]);
    const [hasMore, setHasMore] = useState(false);
    const [acknowledged, setAcknowledged] = useState<Record<string, boolean>>({});
    const [approved, setApproved] = useState<Record<string, boolean>>({});
    const [newSpaceTitle, setNewSpaceTitle] = useState(job.bundle?.space_defaults?.title ?? '');
    const [newSpaceDescription, setNewSpaceDescription] = useState(job.bundle?.space_defaults?.description ?? '');
    const [submitting, setSubmitting] = useState(false);
    const [failure, setFailure] = useState<string | undefined>();

    const needsNewSpace = job.target.kind === 'new';
    const revision = job.preflight?.revision ?? '';
    const counts = job.preflight?.counts;

    useEffect(() => {
        let cancelled = false;
        getImportPreflightResults(job.id, {perPage: REVIEW_PAGE_SIZE}).then((page) => {
            if (!cancelled) {
                setResults(page.items ?? []);
                setHasMore(Boolean(page.has_more));
            }
        }).catch(() => {
            // The summary counts below come from the job itself, so a failed row read degrades the table
            // rather than the step: the user can still see what the import will do in aggregate.
            if (!cancelled) {
                setResults([]);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [job.id, revision]);

    const conflicts = useMemo(() => results.filter((row) => row.overwrite_eligible), [results]);

    const acksSatisfied = requiredAcknowledgementsSatisfied(job, acknowledged);
    const newSpaceReady = !needsNewSpace || newSpaceTitle.trim() !== '';
    const canConfirm = revision !== '' && acksSatisfied && newSpaceReady && !submitting;

    const toggle = useCallback((setter: React.Dispatch<React.SetStateAction<Record<string, boolean>>>, key: string) => {
        setter((current) => ({...current, [key]: !current[key]}));
    }, []);

    const submit = async () => {
        if (!canConfirm) {
            return;
        }
        setSubmitting(true);
        setFailure(undefined);
        try {
            await confirmImportJob(job.id, {
                preflight_revision: revision,
                acknowledgements: acknowledged,
                ...(needsNewSpace ? {new_space: {title: newSpaceTitle.trim(), description: newSpaceDescription.trim()}} : {}),

                // Only ids the user actually ticked, and only ones this plan offered: the server refuses an id
                // that is not an approvable conflict of this job rather than ignoring it.
                overwrite_conflicts: Object.keys(approved).filter((id) => approved[id]),
            });
            onConfirmed();
        } catch (err) {
            if (isPreflightStale(err)) {
                // The plan changed under us. The server has already sent the job back to be recomputed, so
                // there is nothing to retry — the wizard will move itself once the new plan arrives.
                setFailure(formatMessage({
                    id: 'docs.import.review.stale',
                    defaultMessage: 'Something else changed this Space while you were reviewing, so the plan is being recalculated. It will reappear here in a moment.',
                }));
            } else {
                setFailure(formatMessage({
                    id: 'docs.import.review.error',
                    defaultMessage: 'The import could not be confirmed. Nothing has been changed.',
                }));
            }
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <div className={styles.step}>
            <h3 className={styles.stepTitle}>
                {formatMessage({id: 'docs.import.review.title', defaultMessage: 'Review what this will do'})}
            </h3>

            {counts ? (
                <ul className={styles.counts}>
                    {ACTION_ORDER.map((action) => {
                        const value = counts.actions?.[action] ?? 0;
                        return value === 0 ? null : (
                            <li
                                key={action}
                                className={styles.count}
                            >
                                <span className={styles.countValue}>{value}</span>
                                <span className={styles.countLabel}>{actionLabel(action, formatMessage)}</span>
                            </li>
                        );
                    })}
                </ul>
            ) : null}

            {/* Fidelity is a fixed disclosure, not a per-job outcome: no import in this release is full
                fidelity, and saying so once here is more honest than implying it varies. */}
            <p className={styles.hint}>
                {formatMessage({
                    id: 'docs.import.review.fidelity',
                    defaultMessage: 'Pages are imported. Comments and attachments are counted and reported, but not imported.',
                })}
            </p>

            {needsNewSpace ? (
                <>
                    <label className={styles.field}>
                        <span className={styles.fieldLabel}>
                            {formatMessage({id: 'docs.import.review.spaceTitle', defaultMessage: 'Name for the new Space'})}
                        </span>
                        <input
                            type='text'
                            className={styles.textInput}
                            value={newSpaceTitle}
                            disabled={submitting}
                            onChange={(event) => setNewSpaceTitle(event.target.value)}
                        />
                    </label>
                    <label className={styles.field}>
                        <span className={styles.fieldLabel}>
                            {formatMessage({id: 'docs.import.review.spaceDescription', defaultMessage: 'Description'})}
                        </span>
                        <input
                            type='text'
                            className={styles.textInput}
                            value={newSpaceDescription}
                            disabled={submitting}
                            onChange={(event) => setNewSpaceDescription(event.target.value)}
                        />
                    </label>
                </>
            ) : null}

            {conflicts.length > 0 ? (
                <fieldset className={styles.choices}>
                    <legend className={styles.fieldLabel}>
                        {formatMessage({id: 'docs.import.review.conflictsTitle', defaultMessage: 'Pages edited in both places'})}
                    </legend>
                    <p className={styles.hint}>
                        {formatMessage({
                            id: 'docs.import.review.conflictsHint',
                            defaultMessage: 'These changed in Confluence and in Mattermost. They are left alone unless you approve replacing the Mattermost version.',
                        })}
                    </p>
                    {conflicts.map((row) => (
                        <label
                            key={row.external_id}
                            className={styles.choice}
                        >
                            <input
                                type='checkbox'
                                checked={Boolean(approved[row.external_id])}
                                disabled={submitting}
                                onChange={() => toggle(setApproved, row.external_id)}
                            />
                            <span className={styles.choiceBody}>
                                <span className={styles.choiceTitle}>{row.title || row.external_id}</span>
                                <span className={styles.choiceMeta}>
                                    {formatMessage({
                                        id: 'docs.import.review.conflictApprove',
                                        defaultMessage: 'Replace the Mattermost version with the Confluence one',
                                    })}
                                </span>
                            </span>
                        </label>
                    ))}
                </fieldset>
            ) : null}

            {/* The required set comes from the job. Rendering a fixed list would either miss a key the server
                demands or offer one it refuses, and either makes the import unconfirmable. */}
            {job.required_acknowledgements.length > 0 ? (
                <fieldset className={styles.choices}>
                    <legend className={styles.fieldLabel}>
                        {formatMessage({id: 'docs.import.review.acksTitle', defaultMessage: 'Before you continue'})}
                    </legend>
                    {job.required_acknowledgements.map((key) => (
                        <label
                            key={key}
                            className={styles.choice}
                        >
                            <input
                                type='checkbox'
                                checked={Boolean(acknowledged[key])}
                                disabled={submitting}
                                onChange={() => toggle(setAcknowledged, key)}
                            />
                            <span className={styles.choiceBody}>
                                <span className={styles.choiceTitle}>{acknowledgementLabel(key, formatMessage)}</span>
                            </span>
                        </label>
                    ))}
                </fieldset>
            ) : null}

            {hasMore ? (
                <p className={styles.hint}>
                    {formatMessage({
                        id: 'docs.import.review.truncated',
                        defaultMessage: 'Showing the first {shown} pages. The counts above cover every page.',
                    }, {shown: results.length})}
                </p>
            ) : null}

            {failure ? (
                <p
                    role='alert'
                    className={styles.error}
                >
                    {failure}
                </p>
            ) : null}

            <div className={styles.actions}>
                <button
                    type='button'
                    className={styles.primary}
                    disabled={!canConfirm}
                    onClick={submit}
                >
                    {formatMessage({id: 'docs.import.review.submit', defaultMessage: 'Start import'})}
                </button>
            </div>
        </div>
    );
};

// ACTION_ORDER shows the summary in decreasing order of consequence, so what the import *changes* reads before
// what it leaves alone.
const ACTION_ORDER = ['create', 'update', 'conflict', 'blocked', 'preserve_local', 'noop', 'stale'] as const;

// actionLabel names an action in a reader's terms rather than the server's.
function actionLabel(action: string, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (action) {
    case 'create':
        return formatMessage({id: 'docs.import.action.create', defaultMessage: 'new pages'});
    case 'update':
        return formatMessage({id: 'docs.import.action.update', defaultMessage: 'pages updated'});
    case 'conflict':
        return formatMessage({id: 'docs.import.action.conflict', defaultMessage: 'edited in both places'});
    case 'blocked':
        return formatMessage({id: 'docs.import.action.blocked', defaultMessage: 'skipped'});
    case 'preserve_local':
        return formatMessage({id: 'docs.import.action.preserveLocal', defaultMessage: 'your version kept'});
    case 'noop':
        return formatMessage({id: 'docs.import.action.noop', defaultMessage: 'unchanged'});
    case 'stale':
        return formatMessage({id: 'docs.import.action.stale', defaultMessage: 'no longer in Confluence'});
    default:
        return action;
    }
}

// acknowledgementLabel states plainly what each acknowledgement commits the user to. These are the only
// confirmations that gate the import, so vagueness here would be the wrong kind of kindness.
function acknowledgementLabel(key: string, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (key) {
    case 'confirm_new_space_metadata':
        return formatMessage({
            id: 'docs.import.ack.newSpace',
            defaultMessage: 'A new Space will be created with the name and description above.',
        });
    case 'page_only_partial_import':
        return formatMessage({
            id: 'docs.import.ack.pageOnly',
            defaultMessage: 'Comments and attachments in this bundle will not be imported.',
        });
    case 'widen_restricted_pages':
        return formatMessage({
            id: 'docs.import.ack.widenRestricted',
            defaultMessage: 'Pages restricted in Confluence will be readable by everyone in the target Space.',
        });
    case 'reimport_existing_pages':
        return formatMessage({
            id: 'docs.import.ack.reimport',
            defaultMessage: 'This bundle contains pages that were imported before, so existing pages may change.',
        });
    default:
        return key;
    }
}

export default ImportReviewStep;
