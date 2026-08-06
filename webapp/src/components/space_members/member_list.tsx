// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import React from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import MemberRow from './member_row';
import styles from './space_members.module.scss';

type Props = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;
};

/**
 * A space's member roster. Shared by the Share modal, Space Settings → Permissions
 * and the space info panel, which differ only in chrome and affordances.
 */
const MemberList = ({members, avatarSize, showYouBadge = false}: Props) => {
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
                />
            ))}
        </div>
    );
};

export default MemberList;
