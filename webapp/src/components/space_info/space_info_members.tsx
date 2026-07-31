// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {Avatar} from 'webapp_globals';

import styles from './space_info_panel.module.scss';

type Props = {
    members: MemberProfile[];
};

/**
 * The panel's members view, reached from the info menu. Mirrors core's channel
 * members RHS: the roster on its own screen rather than inline on the root.
 */
const SpaceInfoMembers = ({members}: Props) => (
    <div className={styles.memberList}>
        {members.map((member) => (
            <div
                key={member.id}
                className={styles.memberRow}
            >
                <Avatar
                    url={member.avatarUrl}
                    username={member.username}
                    size='sm'
                    name=''
                />
                <span className={styles.memberInfo}>
                    <span className={styles.memberName}>{member.displayName}</span>
                    {member.username && (
                        <span className={styles.memberUsername}>
                            <FormattedMessage
                                id='docs.spaceInfo.handle'
                                defaultMessage='@{username}'
                                values={{username: member.username}}
                            />
                        </span>
                    )}
                </span>
            </div>
        ))}
    </div>
);

export default SpaceInfoMembers;
