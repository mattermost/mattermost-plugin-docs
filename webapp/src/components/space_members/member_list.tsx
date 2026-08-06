// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import React from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import MemberRow from './member_row';
import MemberRowMenu from './member_row_menu';
import styles from './space_members.module.scss';

export type MemberListActions = {
    onRemove: (userId: string) => void;
    onLeave: () => void;

    /** A mutation is in flight; row actions are unavailable. */
    disabled: boolean;
};

type Props = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;

    // Absent means a read-only roster. Expressed as the absence of actions rather
    // than a flag, so a row can never render a menu with nothing behind it.
    actions?: MemberListActions;
};

/**
 * A space's member roster. Shared by the Share modal, Space Settings → Permissions
 * and the space info panel, which differ only in chrome and affordances.
 */
const MemberList = ({members, avatarSize, showYouBadge = false, actions}: Props) => {
    const currentUserId = useAppSelector(getCurrentUserId);

    return (
        <div className={styles.memberList}>
            {members.map((member) => (
                <MemberRow
                    key={member.id}
                    member={member}
                    avatarSize={avatarSize}
                    isCurrentUser={member.id === currentUserId}
                    showYouBadge={showYouBadge}
                    trailing={actions && (
                        <MemberRowMenu
                            member={member}
                            isCurrentUser={member.id === currentUserId}
                            disabled={actions.disabled}
                            onRemove={() => actions.onRemove(member.id)}
                            onLeave={actions.onLeave}
                        />
                    )}
                />
            ))}
        </div>
    );
};

export default MemberList;
