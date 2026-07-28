// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppSelector} from 'hooks/redux';

import type {GlobalState} from '@mattermost/types/store';

// Named space permissions for the current user. Capabilities/RBAC land with
// PR #10 (a per-member capability set on the space's backing-channel
// membership); until then everything is a conservative default, so the UI never
// offers an action the server can't yet honor.
export type SpacePermissions = {

    // Add or remove space members. Needs the add-member API + capabilities from
    // PR #10.
    canManageMembers: boolean;
};

const NO_PERMISSIONS: SpacePermissions = {
    canManageMembers: false,
};

// Pure selector so a check works in thunks/other selectors too, mirroring
// core's getHaveIChannelBookmarkPermission (channel_bookmarks/utils). PR #10's
// capability set will feed this, keyed by spaceId; today it returns the safe
// defaults. Returns a stable reference so useSelector doesn't re-render.
// eslint-disable-next-line @typescript-eslint/no-unused-vars -- state/spaceId are the API the real capability lookup (PR #10) will use
export function getSpacePermissions(state: GlobalState, spaceId: string): SpacePermissions {
    return NO_PERMISSIONS;
}

// Thin hook wrapper, matching core's useChannelBookmarkPermission.
export function useSpacePermissions(spaceId: string): SpacePermissions {
    return useAppSelector((state) => getSpacePermissions(state, spaceId));
}
