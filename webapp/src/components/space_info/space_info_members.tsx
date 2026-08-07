// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';

import {MemberList} from 'components/space_members';

type Props = {
    members: MemberProfile[];
};

/**
 * The panel's members view, reached from the info menu. Mirrors core's channel
 * members RHS: the roster on its own screen rather than inline on the root.
 *
 * Read-only — it passes no actions, so the shared roster renders no row menus.
 */
const SpaceInfoMembers = ({members}: Props) => (
    <MemberList
        members={members}
        avatarSize='sm'
    />
);

export default SpaceInfoMembers;
