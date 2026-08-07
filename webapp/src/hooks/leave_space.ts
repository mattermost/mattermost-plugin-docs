// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';
import {useIntl} from 'react-intl';

import {isLastSpaceMemberError, leaveSpace} from 'store/actions';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';

import {useDocsNavigation} from './navigation';
import {useAppDispatch} from './redux';

/**
 * Leaves a space, navigating home if the space being viewed is the one left, and
 * explaining a refusal in place.
 *
 * Shared by the space header menu and the sidebar row menu so both surfaces give
 * the same answer — in particular the server's "a space must keep one member"
 * rejection, which is otherwise indistinguishable from a generic failure.
 */
export function useLeaveSpace(space: Space): () => Promise<void> {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();
    const {spaceId, goHome} = useDocsNavigation();

    return useCallback(async () => {
        try {
            await dispatch(leaveSpace(space.id));
            if (spaceId === space.id) {
                goHome();
            }
        } catch (error) {
            const description = isLastSpaceMemberError(error) ? formatMessage({
                id: 'docs.leaveSpace.error.lastMember',
                defaultMessage: 'A space must keep at least one member with access. Add another member before you leave.',
            }) : formatMessage({
                id: 'docs.leaveSpace.error.generic',
                defaultMessage: 'Something went wrong. Please try again.',
            });

            toast.error(
                formatMessage({
                    id: 'docs.leaveSpace.error.title',
                    defaultMessage: 'Unable to leave {name}',
                }, {name: space.title}),
                {description},
            );
        }
    }, [dispatch, space.id, space.title, spaceId, goHome, formatMessage]);
}
