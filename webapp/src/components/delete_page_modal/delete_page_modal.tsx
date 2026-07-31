// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import {deletePage} from 'store/actions';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {toast} from 'components/toast';

type Props = {
    spaceId: string;
    pageId: string;
    pageTitle: string;
    onClose: () => void;
};

/**
 * Confirms deleting a page and its subpages. Deleting the page currently routed
 * to leaves nothing to show, so the viewer is sent home.
 */
const DeletePageModal = ({spaceId, pageId, pageTitle, onClose}: Props) => {
    const dispatch = useAppDispatch();
    const {pageId: routedPageId, goHome} = useDocsNavigation();

    const confirm = async () => {
        try {
            await dispatch(deletePage(spaceId, pageId));
            if (routedPageId === pageId) {
                goHome();
            }
        } catch (error) {
            toast.error(
                <FormattedMessage
                    id='docs.deletePage.error'
                    defaultMessage='Could not delete “{title}”.'
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
                    id='docs.deletePage.title'
                    defaultMessage='Delete page'
                />
            )}
            confirmButtonText={(
                <FormattedMessage
                    id='docs.deletePage.confirm'
                    defaultMessage='Delete'
                />
            )}
            isConfirmDestructive={true}
            onConfirm={confirm}
            onCancel={onClose}
        >
            <FormattedMessage
                id='docs.deletePage.message'
                defaultMessage='Are you sure you want to delete <b>{title}</b>? Its subpages will be deleted too.'
                values={{
                    title: pageTitle,
                    b: (chunks) => <b>{chunks}</b>,
                }}
            />
        </ConfirmModal>
    );
};

export default DeletePageModal;
