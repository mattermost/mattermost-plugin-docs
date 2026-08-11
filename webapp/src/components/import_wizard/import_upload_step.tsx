// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {importErrorCode, ImportAdmissionError, uploadImportBundle} from 'client/imports';
import {RestError} from 'client/rest';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import type {ImportJobView, ImportTargetRequest} from 'types/imports';

import styles from './import_wizard.module.scss';

type Props = {
    target: ImportTargetRequest;
    onUploaded: (job: ImportJobView) => void;
};

// ImportUploadStep collects the bundle and starts the job.
//
// This is the only step that transfers a large file, and the only one whose failures are about the bundle
// itself rather than about the import, so it owns its own error presentation.
const ImportUploadStep = ({target, onUploaded}: Props) => {
    const {formatMessage} = useIntl();
    const [bundle, setBundle] = useState<File | undefined>();
    const [uploading, setUploading] = useState(false);
    const [failure, setFailure] = useState<string | undefined>();
    const [retryAfter, setRetryAfter] = useState<number | undefined>();

    const submit = async () => {
        if (!bundle || uploading) {
            return;
        }
        setUploading(true);
        setFailure(undefined);
        setRetryAfter(undefined);
        try {
            onUploaded(await uploadImportBundle(target, bundle));
        } catch (err) {
            setFailure(uploadFailureMessage(err, formatMessage));

            // An admission rejection is the one failure that comes with a "try again later", and saying when
            // is the difference between a useful refusal and one that invites an immediate, identical failure.
            if (err instanceof ImportAdmissionError) {
                setRetryAfter(err.retryAfterSeconds);
            }
        } finally {
            setUploading(false);
        }
    };

    return (
        <div className={styles.step}>
            <h3 className={styles.stepTitle}>
                {formatMessage({id: 'docs.import.upload.title', defaultMessage: 'Choose a Confluence export'})}
            </h3>
            <p className={styles.hint}>
                {formatMessage({
                    id: 'docs.import.upload.hint',
                    defaultMessage: 'Upload a bundle produced by mmetl. Nothing is written until you review and confirm what it will do.',
                })}
            </p>

            {/* The native file input is visually hidden rather than styled: its own "No file chosen" text takes a
                colour the page cannot set, which on a dark theme is all but invisible, and it offers nothing that
                reads as a control. The input itself still does the work, so the label, keyboard and screen
                readers behave exactly as before. */}
            <label className={styles.fileField}>
                <span className={styles.fileButton}>
                    {formatMessage({id: 'docs.import.upload.choose', defaultMessage: 'Choose a file'})}
                </span>
                <span className={bundle ? styles.fileName : styles.fileNameEmpty}>
                    {bundle ? bundle.name : formatMessage({
                        id: 'docs.import.upload.noFile',
                        defaultMessage: 'No bundle chosen yet',
                    })}
                </span>
                <input
                    type='file'
                    accept='.zip,application/zip'
                    className={styles.srOnly}
                    aria-label={formatMessage({id: 'docs.import.upload.field', defaultMessage: 'Confluence export bundle'})}
                    disabled={uploading}
                    onChange={(event) => {
                        setBundle(event.target.files?.[0]);
                        setFailure(undefined);
                    }}
                />
            </label>

            {failure ? (
                <div
                    role='alert'
                    className={styles.error}
                >
                    <p className={styles.errorLine}>{failure}</p>
                    {retryAfter === undefined ? null : (
                        <p className={styles.errorLine}>
                            {formatMessage(
                                {id: 'docs.import.upload.retryAfter', defaultMessage: 'Try again in {seconds} seconds.'},
                                {seconds: retryAfter},
                            )}
                        </p>
                    )}
                </div>
            ) : null}

            <div className={styles.actions}>
                <button
                    type='button'
                    className={styles.primary}
                    disabled={!bundle || uploading}
                    onClick={submit}
                >
                    {uploading ? formatMessage({id: 'docs.import.upload.uploading', defaultMessage: 'Uploading…'}) : formatMessage({id: 'docs.import.upload.submit', defaultMessage: 'Upload and inspect'})}
                </button>
            </div>
        </div>
    );
};

// uploadFailureMessage turns an upload failure into something actionable.
//
// The status carries the shape of the problem and the importer code carries the specifics, so both are used:
// a 413 and a 422 are both "this bundle will not do" but mean different things to fix. The code is appended
// rather than translated per value — there are dozens, they are stable, and an operator or a bug report needs
// the exact one far more than a smooth sentence.
function uploadFailureMessage(err: unknown, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    const code = importErrorCode(err);
    const suffix = code ? ` (${code})` : '';

    if (!(err instanceof RestError)) {
        return formatMessage({
            id: 'docs.import.upload.error.network',
            defaultMessage: 'The upload could not be sent. Check your connection and try again.',
        });
    }
    switch (err.status) {
    case 400:
        return formatMessage({id: 'docs.import.upload.error.invalid', defaultMessage: 'That file is not a valid Confluence export bundle.'}) + suffix;
    case 403:
        return formatMessage({id: 'docs.import.upload.error.forbidden', defaultMessage: 'You do not have permission to import into that destination.'});
    case 413:
        return formatMessage({id: 'docs.import.upload.error.tooLarge', defaultMessage: 'That bundle is too large to import.'}) + suffix;
    case 422:
        return formatMessage({id: 'docs.import.upload.error.overLimit', defaultMessage: 'That bundle is valid but exceeds a Docs limit, so it cannot be imported as-is.'}) + suffix;
    case 429:
        return formatMessage({id: 'docs.import.upload.error.busy', defaultMessage: 'Too many imports are in progress.'});
    default:
        return formatMessage({id: 'docs.import.upload.error.generic', defaultMessage: 'The upload failed.'}) + suffix;
    }
}

export default ImportUploadStep;
