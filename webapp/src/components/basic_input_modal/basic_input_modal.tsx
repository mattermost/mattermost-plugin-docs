// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId, useState} from 'react';
import {FormattedMessage} from 'react-intl';

import {PrimaryButton, TertiaryButton} from 'components/form_controls/button';
import TextArea from 'components/form_controls/text_area';
import TextInput from 'components/form_controls/text_input';
import GenericModal from 'components/generic_modal/generic_modal';

import styles from './basic_input_modal.module.scss';

// Rows for the multiline field; matches the description box in Space Settings.
const MULTILINE_ROWS = 4;

type Props = {
    title: React.ReactNode;
    label: string;
    initialValue?: string;
    confirmButtonText?: React.ReactNode;
    maxLength?: number;

    /** Renders a textarea instead of a single-line input. */
    multiline?: boolean;

    /**
     * Allows confirming with an empty value, for fields that can be cleared
     * (e.g. removing a description). Off by default so renames stay required.
     */
    allowEmpty?: boolean;

    /** Rejecting keeps the modal open and surfaces the reason inline. */
    onConfirm: (value: string) => void | Promise<void>;
    onClose: () => void;
};

/**
 * A one-field modal for short renames and single prompts, single- or multiline.
 * The caller supplies the copy and the confirm handler; this owns the value, the
 * saving state and the inline error.
 */
const BasicInputModal = ({title, label, initialValue = '', confirmButtonText, maxLength, multiline = false, allowEmpty = false, onConfirm, onClose}: Props) => {
    const inputId = useId();

    const [value, setValue] = useState(initialValue);
    const [error, setError] = useState<string>();
    const [saving, setSaving] = useState(false);

    const trimmed = value.trim();
    const canConfirm = (allowEmpty || Boolean(trimmed)) && !saving;

    const confirm = async () => {
        if (!canConfirm) {
            return;
        }
        setSaving(true);
        setError(undefined);
        try {
            await onConfirm(trimmed);
            onClose();
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
            setSaving(false);
        }
    };

    const footer = (
        <>
            <TertiaryButton
                type='button'
                disabled={saving}
                onClick={onClose}
            >
                <FormattedMessage
                    id='docs.basicInputModal.cancel'
                    defaultMessage='Cancel'
                />
            </TertiaryButton>
            <PrimaryButton
                type='button'
                disabled={!canConfirm}
                onClick={confirm}
            >
                {confirmButtonText ?? (
                    <FormattedMessage
                        id='docs.basicInputModal.confirm'
                        defaultMessage='Save'
                    />
                )}
            </PrimaryButton>
        </>
    );

    return (
        <GenericModal
            className={styles.modal}
            title={title}
            ariaLabel={typeof title === 'string' ? title : undefined}
            onClose={onClose}
            showCloseButton={!saving}
            closeDisabled={saving}
            footer={footer}
            headerDivider={false}
        >
            <div className={styles.body}>
                {multiline ? (

                    // No Enter-to-submit: newlines are part of the value.
                    <TextArea
                        id={inputId}
                        label={label}
                        value={value}
                        onChange={setValue}
                        maxLength={maxLength}
                        rows={MULTILINE_ROWS}
                        autoFocus={true}
                    />
                ) : (
                    <TextInput
                        id={inputId}
                        label={label}
                        value={value}
                        onChange={setValue}
                        maxLength={maxLength}
                        autoFocus={true}
                        onEnter={confirm}
                    />
                )}
                {error && <div className={styles.error}>{error}</div>}
            </div>
        </GenericModal>
    );
};

export default BasicInputModal;
