// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {PrimaryButton, SecondaryButton} from 'components/form-controls/button';
import GenericModal from 'components/generic_modal/generic_modal';

import type {Page} from 'types/docs';

import styles from './publish_conflict_dialog.module.scss';

type Props = {
    currentPage: Page | null;

    reason: string;

    onForcePublish: () => void;
    onClose: () => void;
};

const isConcurrentAutosave = (reason: string): boolean => reason.includes('concurrent_autosave');

const PublishConflictDialog = ({currentPage, reason, onForcePublish, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const autosaveConflict = isConcurrentAutosave(reason);

    return (
        <GenericModal
            onClose={onClose}
            ariaLabel={formatMessage({id: 'docs.editor.conflict.title', defaultMessage: 'This page changed while you were editing'})}
            title={
                <FormattedMessage
                    id='docs.editor.conflict.title'
                    defaultMessage='This page changed while you were editing'
                />
            }
            footer={
                <div className={styles.actions}>
                    <SecondaryButton onClick={onClose}>
                        <FormattedMessage
                            id='docs.editor.conflict.cancel'
                            defaultMessage='Keep editing'
                        />
                    </SecondaryButton>
                    <PrimaryButton onClick={onForcePublish}>
                        <FormattedMessage
                            id='docs.editor.conflict.force'
                            defaultMessage='Publish anyway'
                        />
                    </PrimaryButton>
                </div>
            }
        >
            <p>
                {autosaveConflict ? (
                    <FormattedMessage
                        id='docs.editor.conflict.autosaveBody'
                        defaultMessage='Your draft was saved from somewhere else after this publish started. Publishing anyway uses the version currently in this editor.'
                    />
                ) : (
                    <FormattedMessage
                        id='docs.editor.conflict.editBody'
                        defaultMessage='Someone else published changes to this page after you started editing. Publishing anyway replaces their version with yours.'
                    />
                )}
            </p>
            {currentPage ? (
                <p className={styles.meta}>
                    <FormattedMessage
                        id='docs.editor.conflict.currentTitle'
                        defaultMessage='Current published title: {title}'
                        values={{title: currentPage.title}}
                    />
                </p>
            ) : null}
        </GenericModal>
    );
};

export default PublishConflictDialog;
