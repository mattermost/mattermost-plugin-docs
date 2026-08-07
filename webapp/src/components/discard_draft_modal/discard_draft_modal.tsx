// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import {discardDraft} from 'store/actions';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {toast} from 'components/toast';

type Props = {
    spaceId: string;
    pageId: string;
    pageTitle: string;
    onClose: () => void;
};

/**
 * Confirms discarding an unpublished page.
 *
 * Worth a confirm even though nothing published is at risk: the draft *is* the
 * page, so discarding it destroys the only copy. Viewing it at the time leaves
 * nothing to show, so the viewer lands back in the space.
 */
const DiscardDraftModal = ({spaceId, pageId, pageTitle, onClose}: Props) => {
    const dispatch = useAppDispatch();
    const {pageId: routedPageId, isDraft, goToSpace} = useDocsNavigation();

    const viewingThisDraft = isDraft && routedPageId === pageId;

    const confirm = async () => {
        try {
            await dispatch(discardDraft(spaceId, pageId));
            if (viewingThisDraft) {
                // The draft URL names something that no longer exists, so it is not
                // somewhere Back should return to.
                goToSpace(spaceId, {replace: true});
            }
        } catch (error) {
            toast.error(
                <FormattedMessage
                    id='docs.discardDraft.error'
                    defaultMessage='Could not discard “{title}”.'
                    values={{title: pageTitle}}
                />,
                {description: error instanceof Error ? error.message : String(error)},
            );
        }
        onClose();
    };

    return (
        <ConfirmModal
            title={(
                <FormattedMessage
                    id='docs.discardDraft.title'
                    defaultMessage='Discard draft'
                />
            )}
            confirmButtonText={(
                <FormattedMessage
                    id='docs.discardDraft.confirm'
                    defaultMessage='Discard'
                />
            )}
            isConfirmDestructive={true}
            onConfirm={confirm}
            onCancel={onClose}
        >
            <FormattedMessage
                id='docs.discardDraft.message'
                defaultMessage='Are you sure you want to discard <b>{title}</b>? It has never been published, so this cannot be undone.'
                values={{
                    title: pageTitle,
                    b: (chunks) => <b>{chunks}</b>,
                }}
            />
        </ConfirmModal>
    );
};

export default DiscardDraftModal;
