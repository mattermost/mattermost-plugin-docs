// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useCallback, useState} from 'react';
import {useIntl} from 'react-intl';

import {addSpaceMembers, isLastSpaceMemberError, isNotTeamMemberError, removeSpaceMember} from 'store/actions';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';

import {useLeaveSpace} from './leave_space';
import {useAppDispatch} from './redux';

export type ManageSpaceMembers = {

    /** Resolves to the users that FAILED, so a caller can restore exactly those chips. */
    addMembers: (users: MemberProfile[]) => Promise<MemberProfile[]>;
    removeMember: (userId: string) => Promise<void>;
    leave: () => Promise<boolean>;

    /** A mutation is in flight; write affordances should be disabled. */
    busy: boolean;
};

/**
 * Membership mutations for a space, with their user-facing messages.
 *
 * The only layer that knows about both profiles and copy: the thunks below it deal
 * in ids and errors, the components above it deal in chips and rows. Each writing
 * surface calls this once and threads the functions into the shared
 * `components/space_members` core, which never calls it itself.
 */
export function useManageSpaceMembers(space: Space): ManageSpaceMembers {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();
    const leaveSpace = useLeaveSpace(space);
    const [busy, setBusy] = useState(false);

    const addMembers = useCallback(async (users: MemberProfile[]): Promise<MemberProfile[]> => {
        setBusy(true);
        try {
            const failed = await dispatch(addSpaceMembers(space.id, users.map((user) => user.id)));
            if (failed.length === 0) {
                return [];
            }

            const byId = new Map(users.map((user) => [user.id, user]));
            const failedUsers = failed.flatMap(({userId}) => {
                const user = byId.get(userId);
                return user ? [user] : [];
            });

            if (failed.length === 1) {
                const name = failedUsers[0]?.displayName ?? '';
                toast.error(isNotTeamMemberError(failed[0].error) ? formatMessage({
                    id: 'docs.spaceMembers.add.error.notTeamMember',
                    defaultMessage: "{name} isn't a member of this team.",
                }, {name}) : formatMessage({
                    id: 'docs.spaceMembers.add.error.single',
                    defaultMessage: "Couldn't add {name}. Please try again.",
                }, {name}));
            } else {
                // One toast per user would stack N of them for a single click. The chips
                // that stay in the picker are what identify which ones failed.
                toast.error(formatMessage({
                    id: 'docs.spaceMembers.add.error.several',
                    defaultMessage: "Couldn't add {count} people. Please try again.",
                }, {count: failed.length}));
            }
            return failedUsers;
        } finally {
            setBusy(false);
        }
    }, [dispatch, space.id, formatMessage]);

    const removeMember = useCallback(async (userId: string) => {
        setBusy(true);
        try {
            await dispatch(removeSpaceMember(space.id, userId));
        } catch (error) {
            // Its own string rather than useLeaveSpace's: that one ends "before you
            // leave", which is wrong when you are removing somebody else.
            toast.error(isLastSpaceMemberError(error) ? formatMessage({
                id: 'docs.spaceMembers.remove.error.lastMember',
                defaultMessage: 'A space must keep at least one member with access.',
            }) : formatMessage({
                id: 'docs.spaceMembers.remove.error.generic',
                defaultMessage: 'Something went wrong. Please try again.',
            }));
        } finally {
            setBusy(false);
        }
    }, [dispatch, space.id, formatMessage]);

    const leave = useCallback(async () => {
        setBusy(true);
        try {
            return await leaveSpace();
        } finally {
            setBusy(false);
        }
    }, [leaveSpace]);

    return {addMembers, removeMember, leave, busy};
}
