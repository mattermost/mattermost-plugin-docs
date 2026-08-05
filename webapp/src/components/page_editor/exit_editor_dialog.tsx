// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {DestructiveButton, PrimaryButton, SecondaryButton} from 'components/form_controls/button';
import GenericModal from 'components/generic_modal/generic_modal';

import styles from './exit_editor_dialog.module.scss';

type Props = {
    onPublish: () => void;
    onSaveDraft: () => void;
    onDiscard: () => void;
    onClose: () => void;
    busy?: boolean;
    failed?: boolean;
};

const ExitEditorDialog = ({onPublish, onSaveDraft, onDiscard, onClose, busy = false, failed = false}: Props) => {
    const {formatMessage} = useIntl();

    return (
        <GenericModal
            onClose={onClose}
            ariaLabel={formatMessage({id: 'docs.editor.exit.title', defaultMessage: 'Save your changes?'})}
            title={
                <FormattedMessage
                    id='docs.editor.exit.title'
                    defaultMessage='Save your changes?'
                />
            }
            footer={
                <div className={styles.actions}>
                    <DestructiveButton
                        onClick={onDiscard}
                        disabled={busy}
                    >
                        <FormattedMessage
                            id='docs.editor.exit.discard'
                            defaultMessage='Discard changes'
                        />
                    </DestructiveButton>
                    <div className={styles.spacer}/>
                    <SecondaryButton
                        onClick={onSaveDraft}
                        disabled={busy}
                    >
                        <FormattedMessage
                            id='docs.editor.exit.saveDraft'
                            defaultMessage='Save as draft'
                        />
                    </SecondaryButton>
                    <PrimaryButton
                        onClick={onPublish}
                        disabled={busy}
                    >
                        <FormattedMessage
                            id='docs.editor.exit.publish'
                            defaultMessage='Save and publish'
                        />
                    </PrimaryButton>
                </div>
            }
        >
            <p>
                <FormattedMessage
                    id='docs.editor.exit.body'
                    defaultMessage='Publishing makes your changes visible to everyone with access to this space. A draft stays visible only to you.'
                />
            </p>
            {failed && (
                <p
                    className={styles.error}
                    role='alert'
                >
                    <FormattedMessage
                        id='docs.editor.exit.failed'
                        defaultMessage='That did not work. Your changes are still here — try again, or keep editing.'
                    />
                </p>
            )}
        </GenericModal>
    );
};

export default ExitEditorDialog;
