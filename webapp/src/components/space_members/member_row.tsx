// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {Avatar} from 'webapp_globals';

import styles from './space_members.module.scss';

type Props = {
    member: MemberProfile;
    avatarSize: 'sm' | 'md';
    isCurrentUser: boolean;
    showYouBadge: boolean;

    /** Row-end slot. Absent on a read-only roster. */
    trailing?: React.ReactNode;
};

const MemberRow = ({member, avatarSize, isCurrentUser, showYouBadge, trailing}: Props) => (
    <div className={styles.memberRow}>
        <Avatar
            url={member.avatarUrl}
            username={member.username}
            size={avatarSize}
            name=''
        />
        <span className={styles.memberInfo}>
            <span className={styles.memberName}>{member.displayName}</span>
            {member.username && (
                <span className={styles.memberUsername}>
                    <FormattedMessage
                        id='docs.spaceMembers.handle'
                        defaultMessage='@{username}'
                        values={{username: member.username}}
                    />
                </span>
            )}
            {showYouBadge && isCurrentUser && (
                <span className={styles.you}>
                    <FormattedMessage
                        id='docs.spaceMembers.you'
                        defaultMessage='(You)'
                    />
                </span>
            )}
        </span>
        {trailing}
    </div>
);

export default MemberRow;
