// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
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
    comfortable?: boolean;

    /**
     * Slot beneath the identity line, for per-member controls that need the row's full
     * width. Rendered inside the row so the two stay associated when rows wrap.
     */
    below?: React.ReactNode;
};

const MemberRow = ({member, avatarSize, isCurrentUser, showYouBadge, trailing, comfortable = false, below}: Props) => {
    const identity = (
        <div
            className={classNames(
                below ? styles.memberRowIdentity : styles.memberRow,
                {[styles.memberRowComfortable]: comfortable},
            )}
        >
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

    if (!below) {
        return identity;
    }

    return (
        <div className={styles.memberRowStacked}>
            {identity}
            {below}
        </div>
    );
};

export default MemberRow;
