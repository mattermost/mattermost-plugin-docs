// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import AlertCircleOutlineIcon from '@mattermost/compass-icons/components/alert-circle-outline';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';

import {PrimaryButton, TertiaryButton} from 'components/form_controls/button';

import styles from './save_changes_bar.module.scss';

export type SaveChangesBarState = 'editing' | 'error';

type Props = {
    state?: SaveChangesBarState;

    /** Overrides the default "You have unsaved changes" prompt. */
    message?: React.ReactNode;

    /** Shown in place of the prompt when `state` is `error`. */
    errorMessage?: React.ReactNode;
    saving?: boolean;
    saveText?: React.ReactNode;
    resetText?: React.ReactNode;
    onSave: () => void;
    onReset: () => void;
};

/**
 * Floating "unsaved changes" bar with Save/Reset actions, modeled on the host's
 * channel-settings SaveChangesPanel. The consumer positions it (e.g. absolutely
 * at the bottom of a modal) and renders it only while there are changes.
 */
const SaveChangesBar = ({state = 'editing', message, errorMessage, saving = false, saveText, resetText, onSave, onReset}: Props) => {
    const isError = state === 'error';

    return (
        <div
            className={classNames(styles.bar, {[styles.error]: isError})}
            role='status'
        >
            <span className={styles.message}>
                <span
                    className={styles.icon}
                    aria-hidden={true}
                >
                    {isError ? <AlertCircleOutlineIcon size={18}/> : <InformationOutlineIcon size={18}/>}
                </span>
                {isError ? (errorMessage ?? (
                    <FormattedMessage
                        id='docs.saveChanges.error'
                        defaultMessage='There was an error saving your changes'
                    />
                )) : (message ?? (
                    <FormattedMessage
                        id='docs.saveChanges.message'
                        defaultMessage='You have unsaved changes'
                    />
                ))}
            </span>
            <span className={styles.actions}>
                <TertiaryButton
                    size='sm'
                    disabled={saving}
                    onClick={onReset}
                >
                    {resetText ?? (
                        <FormattedMessage
                            id='docs.saveChanges.reset'
                            defaultMessage='Reset'
                        />
                    )}
                </TertiaryButton>
                <PrimaryButton
                    size='sm'
                    disabled={saving}
                    onClick={onSave}
                >
                    {saveText ?? (
                        <FormattedMessage
                            id='docs.saveChanges.save'
                            defaultMessage='Save'
                        />
                    )}
                </PrimaryButton>
            </span>
        </div>
    );
};

export default SaveChangesBar;
