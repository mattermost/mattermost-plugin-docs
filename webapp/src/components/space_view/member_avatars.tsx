// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberIds} from 'hooks/members';
import React from 'react';
import {Avatars, hostHasAvatars} from 'webapp_globals';

import MemberAvatarsFallback from './member_avatars_fallback';

// Overlapping avatar stack for a space's members. Core owns the stack, the "+N"
// overflow chip and the profile popovers; hosts predating MM-70358 don't publish
// Avatars, so those fall back to Docs' own stack.
const MemberAvatars = ({spaceId}: {spaceId: string}) => {
    const memberIds = useSpaceMemberIds(spaceId);

    if (!hostHasAvatars()) {
        return <MemberAvatarsFallback spaceId={spaceId}/>;
    }

    if (memberIds.length === 0) {
        return null;
    }

    return (
        <Avatars
            userIds={memberIds}
            size='sm'
            canOpenOverflow={true}
        />
    );
};

export default MemberAvatars;
