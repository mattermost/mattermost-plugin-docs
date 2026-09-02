// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {type MemberProfile, useSpaceMemberProfiles} from 'hooks/members';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {Avatar} from 'webapp_globals';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import styles from './member_avatars.module.scss';

const MAX_SHOWN = 3;

const MemberLabel = ({member}: {member: MemberProfile}) => (
    <span className={styles.label}>
        <span className={styles.labelName}>{member.displayName}</span>
        {member.username && member.username !== member.displayName && (
            <span className={styles.labelUsername}>
                <FormattedMessage
                    id='docs.space.memberHandle'
                    defaultMessage='@{username}'
                    values={{username: member.username}}
                />
            </span>
        )}
    </span>
);

// Docs' own avatar stack, used only on hosts that don't publish core's Avatars.
// Core owns the canonical implementation — see member_avatars.tsx.
const MemberAvatarsFallback = ({spaceId}: {spaceId: string}) => {
    const members = useSpaceMemberProfiles(spaceId);

    if (members.length === 0) {
        return null;
    }

    const shown = members.slice(0, MAX_SHOWN);
    const hidden = members.slice(shown.length);

    return (
        <div className={styles.stack}>
            {shown.map((member) => (
                <WithTooltip
                    key={member.id}
                    title={<MemberLabel member={member}/>}
                >
                    <span
                        className={styles.avatar}
                        role='img'
                        aria-label={member.displayName}
                    >
                        <Avatar
                            url={member.avatarUrl}
                            username={member.username}
                            size='sm'
                            name=''
                        />
                    </span>
                </WithTooltip>
            ))}
            {hidden.length > 0 && (
                <WithTooltip
                    title={
                        <span className={styles.labelList}>
                            {hidden.map((member) => (
                                <MemberLabel
                                    key={member.id}
                                    member={member}
                                />
                            ))}
                        </span>
                    }
                >
                    <span className={styles.overflow}>
                        <FormattedMessage
                            id='docs.space.membersOverflow'
                            defaultMessage='+{count}'
                            values={{count: hidden.length}}
                        />
                    </span>
                </WithTooltip>
            )}
        </div>
    );
};

export default MemberAvatarsFallback;
