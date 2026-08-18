// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import ConfirmModal from 'components/confirm_modal/confirm_modal';

import type {Page} from 'types/docs';

type Props = {

    // The page as the server currently has it, or null when the re-read failed.
    currentPage: Page | null;
    onConfirm: () => void;
    onCancel: () => void;
};

/**
 * Offered when publishing is refused because the page moved underneath the draft.
 *
 * Publishing anyway overwrites whatever landed in the meantime, so the other
 * version is named before the choice is offered — a generic retry would reissue
 * the same request and be refused the same way, forever.
 */
const PublishConflictModal = ({currentPage, onConfirm, onCancel}: Props) => (
    <ConfirmModal
        title={(
            <FormattedMessage
                id='docs.publish.conflict.title'
                defaultMessage='This page changed while you were editing'
            />
        )}
        confirmButtonText={(
            <FormattedMessage
                id='docs.publish.conflict.confirm'
                defaultMessage='Publish anyway'
            />
        )}
        isConfirmDestructive={true}
        onConfirm={onConfirm}
        onCancel={onCancel}
    >
        {currentPage ? (
            <FormattedMessage
                id='docs.publish.conflict.message'
                defaultMessage='Someone else published changes to <b>{title}</b> since you started this draft. Publishing anyway replaces their version with yours.'
                values={{
                    title: currentPage.title,
                    b: (chunks) => <b>{chunks}</b>,
                }}
            />
        ) : (
            <FormattedMessage
                id='docs.publish.conflict.messageUnknown'
                defaultMessage='Someone else published changes to this page since you started this draft. Publishing anyway replaces their version with yours.'
            />
        )}
    </ConfirmModal>
);

export default PublishConflictModal;
