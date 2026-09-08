// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';
import {useIntl} from 'react-intl';

import {isLastSpaceAdminError, isLastSpaceMemberError, isSpaceLockTimeoutError, leaveSpace} from 'store/actions';

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
export function useLeaveSpace(space: Space): () => Promise<boolean> {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();
    const {spaceId, goHome} = useDocsNavigation();

    return useCallback(async () => {
        try {
            await dispatch(leaveSpace(space.id));
            if (spaceId === space.id) {
                goHome();
            }
            return true;
        } catch (error) {
            let description;
            if (isLastSpaceMemberError(error)) {
                description = formatMessage({
                    id: 'docs.leaveSpace.error.lastMember',
                    defaultMessage: 'A space must keep at least one member with access. Add another member before you leave.',
                });
            } else if (isLastSpaceAdminError(error)) {
                // Naming the administrator requirement matters: the last-member wording sent a sole
                // admin off to add an ordinary member, which does not lift the refusal.
                description = formatMessage({
                    id: 'docs.leaveSpace.error.lastAdmin',
                    defaultMessage: 'A space must keep at least one administrator. Make another member an administrator before you leave.',
                });
            } else if (isSpaceLockTimeoutError(error)) {
                description = formatMessage({
                    id: 'docs.leaveSpace.error.busy',
                    defaultMessage: 'This space is being changed right now. Try again in a moment.',
                });
            } else {
                description = formatMessage({
                    id: 'docs.leaveSpace.error.generic',
                    defaultMessage: 'Something went wrong. Please try again.',
                });
            }

            toast.error(
                formatMessage({
                    id: 'docs.leaveSpace.error.title',
                    defaultMessage: 'Unable to leave {name}',
                }, {name: space.title}),
                {description},
            );
            return false;
        }
    }, [dispatch, space.id, space.title, spaceId, goHome, formatMessage]);
}
