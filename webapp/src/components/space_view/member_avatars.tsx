// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberProfiles} from 'hooks/members';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {Avatar} from 'webapp_globals';

import styles from './member_avatars.module.scss';

const MAX_SHOWN = 3;

// Overlapping avatar stack for a space's members, with a "+N" overflow. Renders
// nothing when there are no members (Avatar itself no-ops on hosts without it).
const MemberAvatars = ({spaceId}: {spaceId: string}) => {
    const members = useSpaceMemberProfiles(spaceId);

    if (members.length === 0) {
        return null;
    }

    const shown = members.slice(0, MAX_SHOWN);
    const overflow = members.length - shown.length;

    return (
        <div className={styles.stack}>
            {shown.map((member) => (
                <span
                    key={member.id}
                    className={styles.avatar}
                    title={member.displayName}
                >
                    <Avatar
                        url={member.avatarUrl}
                        username={member.username}
                        size='sm'
                        name=''
                    />
                </span>
            ))}
            {overflow > 0 && (
                <span className={styles.overflow}>
                    <FormattedMessage
                        id='docs.space.membersOverflow'
                        defaultMessage='+{count}'
                        values={{count: overflow}}
                    />
                </span>
            )}
        </div>
    );
};

export default MemberAvatars;
