// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useSpaceMemberProfiles} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import type {ManageSpaceMembers} from 'hooks/space_members';
import {useManageSpaceMembers} from 'hooks/space_members';
import type {SpacePermissions} from 'hooks/space_permissions';
import {useSpacePermissions} from 'hooks/space_permissions';
import {useCallback, useMemo} from 'react';
import {useIntl} from 'react-intl';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';

export type SpaceAccessEditor = {
    permissions: SpacePermissions;
    members: MemberProfile[];
    memberIds: string[];

    /** This surface may not enable default and view-access changes. */
    adminLocked: boolean;

    /** This surface may not expose member and grant controls. */
    rosterLocked: boolean;

    adminLockedReason: string | undefined;
    guestLockedReason: string;
    selfLockedReason: string;
    adminSpaceLockedReason: string;

    roleForMember: (profile: MemberProfile) => 'guest' | 'admin' | 'member' | undefined;
    memberLockedReason: (profile: MemberProfile) => string | undefined;
    isMemberLocked: (profile: MemberProfile) => boolean;

    /** Wired into MemberList; Remove is withheld from a surface that cannot manage members. */
    actions: MemberListActions;

    addMembers: ManageSpaceMembers['addMembers'];
    removeMember: ManageSpaceMembers['removeMember'];
    leave: ManageSpaceMembers['leave'];
    busy: boolean;
};

/**
 * The access-editor view model shared by the Share modal and Space Settings → Permissions:
 * lock state, per-member lock reasons, and the roster actions both surfaces wire into
 * MemberList. Each surface keeps its own widgets; only the derived state lives here.
 */
export function useSpaceAccessEditor(space: Space, {onClose}: {onClose: () => void}): SpaceAccessEditor {
    const {formatMessage} = useIntl();
    const currentUserId = useAppSelector(getCurrentUserId);
    const members = useSpaceMemberProfiles(space.id);
    const {addMembers, removeMember, leave, busy} = useManageSpaceMembers(space);
    const permissions = useSpacePermissions(space);

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    // Exposure and roster writes have different authority tiers. Disable both while the hook
    // reports a write in flight to avoid overlapping reconciliation from either surface.
    const adminLocked = !permissions.canAdminister || permissions.loading || permissions.busy;
    const rosterLocked = !permissions.canManageMembers || permissions.loading || permissions.busy || busy;

    // Only a resolved authority denial gets the admin-only explanation; a read still in flight,
    // or a save of the caller's own, has no denial to report yet.
    let adminLockedReason: string | undefined;
    if (permissions.loadFailed) {
        adminLockedReason = formatMessage({
            id: 'docs.spaceSettings.permissions.loadFailed',
            defaultMessage: "Couldn't load this space's permissions. Close and reopen to try again.",
        });
    } else if (!permissions.loading && !permissions.busy) {
        adminLockedReason = formatMessage({
            id: 'docs.spaceSettings.permissions.adminOnly',
            defaultMessage: 'Only a space administrator can change this',
        });
    }
    const guestLockedReason = formatMessage({
        id: 'docs.spaceSettings.permissions.guestLocked',
        defaultMessage: 'Guests can only view pages',
    });
    const selfLockedReason = formatMessage({
        id: 'docs.spaceSettings.permissions.selfLocked',
        defaultMessage: 'Only a space administrator can change their own permissions',
    });
    const adminSpaceLockedReason = formatMessage({
        id: 'docs.spaceSettings.permissions.adminSpaceLocked',
        defaultMessage: 'Only a space administrator can grant this',
    });

    const roleForMember = useCallback((profile: MemberProfile): 'guest' | 'admin' | 'member' | undefined => {
        const record = permissions.members.get(profile.id);
        if (!record) {
            return undefined;
        }
        if (record.is_guest) {
            return 'guest';
        }
        return record.is_admin ? 'admin' : 'member';
    }, [permissions.members]);

    const isMemberLocked = useCallback((profile: MemberProfile): boolean => {
        const record = permissions.members.get(profile.id);
        return rosterLocked || Boolean(record?.is_guest) || (profile.id === currentUserId && !permissions.canAdminister);
    }, [permissions.members, rosterLocked, currentUserId, permissions.canAdminister]);

    const memberLockedReason = useCallback((profile: MemberProfile): string | undefined => {
        const record = permissions.members.get(profile.id);
        if (record?.is_guest) {
            return guestLockedReason;
        }
        if (profile.id === currentUserId && !permissions.canAdminister) {
            return selfLockedReason;
        }
        if (rosterLocked) {
            return adminLockedReason;
        }
        return undefined;
    }, [permissions.members, currentUserId, permissions.canAdminister, rosterLocked, guestLockedReason, selfLockedReason, adminLockedReason]);

    const actions: MemberListActions = useMemo(() => ({
        ...(permissions.canManageMembers && {onRemove: removeMember}),
        onLeave: async () => {
            if (await leave()) {
                onClose();
            }
        },
        disabled: rosterLocked,
    }), [permissions.canManageMembers, removeMember, leave, onClose, rosterLocked]);

    return {
        permissions,
        members,
        memberIds,
        adminLocked,
        rosterLocked,
        adminLockedReason,
        guestLockedReason,
        selfLockedReason,
        adminSpaceLockedReason,
        roleForMember,
        memberLockedReason,
        isMemberLocked,
        actions,
        addMembers,
        removeMember,
        leave,
        busy,
    };
}
