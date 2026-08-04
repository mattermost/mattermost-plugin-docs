// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {FormattedMessage} from 'react-intl';

import {DestructiveButton, PrimaryButton, TertiaryButton} from 'components/form_controls/button';
import GenericModal, {useModalClose} from 'components/generic_modal/generic_modal';

import styles from './confirm_modal.module.scss';

type Props = {
    title: React.ReactNode;
    children: React.ReactNode;
    confirmButtonText?: React.ReactNode;
    cancelButtonText?: React.ReactNode;
    isConfirmDestructive?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
};

const ConfirmModal = ({title, children, confirmButtonText, cancelButtonText, isConfirmDestructive = false, onConfirm, onCancel}: Props) => {
    // Focus the non-destructive Cancel button by default so pressing Enter does
    // not immediately trigger a destructive confirm.
    const cancelRef = useRef<HTMLButtonElement>(null);

    const ConfirmButton = isConfirmDestructive ? DestructiveButton : PrimaryButton;

    const cancelLabel = cancelButtonText ?? (
        <FormattedMessage
            id='docs.confirmModal.cancel'
            defaultMessage='Cancel'
        />
    );

    const confirmLabel = confirmButtonText ?? (
        <FormattedMessage
            id='docs.confirmModal.confirm'
            defaultMessage='Confirm'
        />
    );

    // Rendered inside the modal rather than built here, so it can reach
    // `useModalClose`: both buttons dismiss the modal as well as acting, and going
    // through the modal's own close lets it animate out before the handler runs and
    // unmounts it.
    const Footer = () => {
        const close = useModalClose();

        // Not `close?.(action) ?? action()` — close returns void, so that would run
        // the action a second time, immediately.
        const dismiss = (action: () => void) => () => {
            if (close) {
                close(action);
            } else {
                action();
            }
        };

        return (
            <>
                <TertiaryButton
                    ref={cancelRef}
                    type='button'
                    onClick={dismiss(onCancel)}
                >
                    {cancelLabel}
                </TertiaryButton>
                <ConfirmButton
                    type='button'
                    onClick={dismiss(onConfirm)}
                >
                    {confirmLabel}
                </ConfirmButton>
            </>
        );
    };

    return (
        <GenericModal
            title={title}
            ariaLabel={typeof title === 'string' ? title : undefined}
            onClose={onCancel}
            initialFocus={cancelRef}
            headerClassName={styles.header}
            footer={<Footer/>}
        >
            <div className={styles.body}>
                {children}
            </div>
        </GenericModal>
    );
};

export default ConfirmModal;
