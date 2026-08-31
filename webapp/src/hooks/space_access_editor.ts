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
import type {Permission} from 'types/permissions';
import {DEFAULT_PERMISSION_ORDER, MEMBER_PERMISSION_ORDER} from 'types/permissions';

export type SpaceAccessEditor = {
    permissions: SpacePermissions;
    members: MemberProfile[];
    memberIds: string[];

    // Authority, not availability. A caller without it would be refused by the server, so the
    // surface renders no control at all rather than a disabled one; the busy flags below are
    // the separate, temporary condition that disables a control the caller does own.
    canEditAccess: boolean;

    /** The permission ids this caller may grant a member, empty when they may grant none. */
    grantOptionsFor: (profile: MemberProfile) => readonly Permission[];

    /** A view-access or default-set write is in flight, or the record is still loading. */
    accessBusy: boolean;

    /** A roster write is in flight, or the record is still loading. */
    rosterBusy: boolean;

    /** Set only when the permission record failed to load, so the surface can say so. */
    loadFailedReason: string | undefined;

    busyReason: string | undefined;

    roleForMember: (profile: MemberProfile) => 'guest' | 'admin' | 'member' | undefined;

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

    // Exposure and roster writes have different authority tiers, and each is separately
    // unavailable while a write of its own is in flight.
    const canEditAccess = permissions.canAdminister;
    const accessBusy = permissions.loading || permissions.busy;
    const rosterBusy = permissions.loading || permissions.busy || busy;

    const loadFailedReason = permissions.loadFailed ? formatMessage({
        id: 'docs.spaceSettings.permissions.loadFailed',
        defaultMessage: "Couldn't load this space's permissions. Close and reopen to try again.",
    }) : undefined;

    // The only reason a control the caller owns is unusable: an authority denial withholds the
    // control instead, so it has no reason left to report.
    const busyReason = accessBusy ? formatMessage({
        id: 'docs.spaceSettings.permissions.saving',
        defaultMessage: 'Saving…',
    }) : undefined;

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

    // Every grant this caller could not make on this member is dropped from the vocabulary
    // rather than offered and refused: a guest may hold nothing beyond read_page, a member
    // cannot raise themselves, and admin_space is a space administrator's grant to give.
    const grantOptionsFor = useCallback((profile: MemberProfile): readonly Permission[] => {
        const record = permissions.members.get(profile.id);
        if (!record || !permissions.canManageMembers || record.is_guest) {
            return [];
        }
        if (profile.id === currentUserId && !permissions.canAdminister) {
            return [];
        }
        return permissions.canAdminister ? MEMBER_PERMISSION_ORDER : DEFAULT_PERMISSION_ORDER;
    }, [permissions.members, permissions.canManageMembers, permissions.canAdminister, currentUserId]);

    const actions: MemberListActions = useMemo(() => ({
        ...(permissions.canManageMembers && {onRemove: removeMember}),
        onLeave: async () => {
            if (await leave()) {
                onClose();
            }
        },
        disabled: rosterBusy,
    }), [permissions.canManageMembers, removeMember, leave, onClose, rosterBusy]);

    return {
        permissions,
        members,
        memberIds,
        canEditAccess,
        grantOptionsFor,
        accessBusy,
        rosterBusy,
        loadFailedReason,
        busyReason,
        roleForMember,
        actions,
        addMembers,
        removeMember,
        leave,
        busy,
    };
}
