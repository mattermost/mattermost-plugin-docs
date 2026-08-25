// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';
import {getSpaceAccess, listAllSpaceMembers, setDefaultPermissions, setMemberPermissions, setSpaceViewAccess} from 'client/space_permissions';
import {useCanAdministerSpace, useCanManageSpaceMembers} from 'hooks/permissions';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {receivedSpaceAccess} from 'store/actions';
import {getSpace, getSpaceMemberPermissionsRevision, getSpaceMemberIds} from 'store/selectors';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';
import {Permissions} from 'types/permissions';
import type {Permission, SpaceMember, SpaceViewAccess} from 'types/permissions';
import {GUEST_NOT_ASSIGNABLE_ERROR_ID, LAST_SPACE_ADMIN_ERROR_ID, SPACE_LOCK_TIMEOUT_ERROR_ID} from 'types/server_errors';

export type SpacePermissions = {

    /**
     * The space's default permission set — what every member holds without a grant. Read from the
     * store, which this hook's own reads and the websocket handlers both keep current.
     */
    defaults: Permission[];
    viewAccess: SpaceViewAccess;

    /** Every member's granted set and role flags, keyed by user id. */
    members: Map<string, SpaceMember>;

    /**
     * Whether the current user may change the space's own exposure policy — the default
     * permission set and view_access. Sysadmin or space admin only. Read from the store, which
     * this hook keeps current from its own reads — the same answer every other surface gates on.
     */
    canAdminister: boolean;

    /**
     * Whether the current user may read and write the member roster. A strictly wider tier than
     * canAdminister: the roster routes also admit a team manage_space holder, who holds no
     * admin_space and would be locked out of the roster if this were derived from canAdminister.
     */
    canManageMembers: boolean;

    /** The first load has not resolved; the surface has nothing truthful to show yet. */
    loading: boolean;

    /**
     * The first load failed, so the state above is empty for want of an answer rather
     * than because the space is configured that way. Distinct from canAdminister:
     * without it a failed read is indistinguishable from a resolved "you are not an
     * administrator", and the surface would state the latter on a network error.
     */
    loadFailed: boolean;

    /** A mutation is in flight; write affordances should be disabled. */
    busy: boolean;

    setDefaults: (next: Permission[]) => Promise<void>;
    setMemberGrants: (userId: string, next: Permission[]) => Promise<void>;
    setViewAccess: (next: SpaceViewAccess) => Promise<void>;
};

const serverErrorId = (error: unknown): string | undefined =>
    (error instanceof RestError ? error.server_error_id : undefined);

// Stable identity, so a space whose defaults have not resolved does not hand out a fresh array
// every render.
const EMPTY_PERMISSIONS: Permission[] = [];

/**
 * The space's permission state and the mutations that change it.
 *
 * Everything about that state lives in the store except the per-member grant matrix: only this
 * surface displays it, and only a caller holding the manage tier may read it at all, so it is
 * fetched and held here and the store carries a revision counter to say when it went stale.
 *
 * Every mutation re-reads from the server response rather than patching local state
 * optimistically. The server derives the effective set from the space default, the
 * grant and the member's role flags, so a locally-computed guess would disagree with
 * it exactly in the cases that matter (a guest, or an admin whose grant is redundant).
 */
export function useSpacePermissions(space: Space): SpacePermissions {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const currentUserId = useAppSelector(getCurrentUserId);

    // The caller's own tiers come from the store, not from a second copy kept here: every other
    // permission-gated surface already gates on these selectors, and two resolutions of the same
    // answer drift the moment one of them refreshes and the other does not. This hook's own reads
    // feed the store, so reading back from it is not a staleness trade.
    const storeCanAdminister = useCanAdministerSpace(space.id);
    const storeCanManageMembers = useCanManageSpaceMembers(space.id);

    // The roster this hook reads is its own snapshot, taken once per load. Membership changes
    // elsewhere — the tab's own add/remove field, or another administrator's change arriving over
    // the websocket — land in the store, so the store's member list is what says the snapshot is
    // stale. Without it a member added while this surface is open renders with no permission row,
    // because the grant matrix has no record of them.
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, space.id));

    // The staleness signal for the one piece of permission state this surface still resolves
    // itself. A per-member grant change moves no slice the store holds, so without this the matrix
    // below would keep showing what was true when the surface mounted.
    const grantRevision = useAppSelector((state) => getSpaceMemberPermissionsRevision(state, space.id));

    // view_access, update_at and the default permission set come off the space record rather than
    // copies taken at load: all three are already store state that the websocket handlers refresh,
    // and the writes below feed their responses straight back into it. Falls back to the caller's
    // own record, which is what a space reached without a single-space read carries.
    const stored = useAppSelector((state) => getSpace(state, space.id)) ?? space;
    const viewAccess = stored.view_access;
    const defaults = stored.default_permissions ?? EMPTY_PERMISSIONS;

    const [members, setMembers] = useState<Map<string, SpaceMember>>(new Map());
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const [busy, setBusy] = useState(false);

    const genericError = useCallback(() => formatMessage({
        id: 'docs.spacePermissions.error.generic',
        defaultMessage: 'Something went wrong. Please try again.',
    }), [formatMessage]);

    // All three permission writes share the same concurrency contract. Keeping the mapping here
    // prevents one surface from turning a retryable lock timeout or an optimistic-lock conflict
    // into the generic message while preserving the member-only refusals.
    const permissionWriteError = useCallback((error: unknown) => {
        const id = serverErrorId(error);
        if (id === SPACE_LOCK_TIMEOUT_ERROR_ID) {
            return formatMessage({
                id: 'docs.spacePermissions.error.busy',
                defaultMessage: 'This space is being changed right now. Try again in a moment.',
            });
        }
        if (id === LAST_SPACE_ADMIN_ERROR_ID) {
            return formatMessage({
                id: 'docs.spacePermissions.error.lastAdmin',
                defaultMessage: 'A space must keep at least one administrator.',
            });
        }
        if (id === GUEST_NOT_ASSIGNABLE_ERROR_ID) {
            return formatMessage({
                id: 'docs.spacePermissions.error.guest',
                defaultMessage: 'Guests are read-only and cannot be granted permissions.',
            });
        }
        if (error instanceof RestError && error.status === 409) {
            return formatMessage({
                id: 'docs.spacePermissions.error.conflict',
                defaultMessage: 'Someone else changed this space. Reopen settings and try again.',
            });
        }
        return genericError();
    }, [formatMessage, genericError]);

    // Re-reads the caller's own authority from the server and puts it in the store. Used after a
    // write that can change it — editing your own row — rather than reconstructing the new tiers
    // locally: which tier granted the roster (admin_space, a team grant, or sysadmin) is not
    // recoverable from the response, and guessing is how the surface and the routes drift apart in
    // the first place.
    const reloadTiers = useCallback(async () => {
        try {
            dispatch(receivedSpaceAccess(await getSpaceAccess(space.id)));
        } catch {
            // The write itself succeeded, so this must not surface as a failure. The store keeps
            // the tiers it last resolved; the next load or websocket refresh corrects them.
        }
    }, [dispatch, space.id]);

    useEffect(() => {
        let cancelled = false;

        // A failed read must not leave the previous space's matrix standing.
        const clearResolved = () => setMembers(new Map());

        const load = async () => {
            setLoading(true);
            setLoadFailed(false);
            try {
                const access = await getSpaceAccess(space.id);
                if (cancelled) {
                    return;
                }

                // Into the store, where every permission-gated surface reads the caller's own
                // tiers from — including canAdminister/canManageMembers above. The server resolves
                // that set the same way the write gate does, so a system administrator, who holds
                // admin_space without appearing in the roster at all, resolves as able to
                // administer.
                dispatch(receivedSpaceAccess(access));
                const canManage = access.permissions.includes(Permissions.MANAGE_SPACE);

                // Read after the tier, not alongside it: the roster route serves every reader so a
                // space view can render its member count, but it redacts the per-member permission
                // matrix for a caller without the manage tier. This surface edits that matrix, so a
                // redacted roster is not an answer it can show — an all-empty grid would state that
                // every member holds nothing. Treated as the read failure a 403 used to be.
                const roster = canManage ? await listAllSpaceMembers(space.id) : null;
                if (cancelled) {
                    return;
                }
                if (!roster) {
                    clearResolved();
                    setLoadFailed(true);
                    return;
                }

                setMembers(new Map(roster.map((member) => [member.user_id, member])));
            } catch {
                if (!cancelled) {
                    // No toast: a failed read leaves the controls disabled and empty, which is
                    // already visible. A toast here would fire on every mount behind a 403.
                    // loadFailed is what keeps that emptiness from being read as an answer.
                    clearResolved();
                    setLoadFailed(true);
                }
            } finally {
                if (!cancelled) {
                    setLoading(false);
                }
            }
        };

        load();
        return () => {
            cancelled = true;
        };
    }, [dispatch, space.id, memberIds, grantRevision]);

    // No roster re-read afterwards: a default change moves every member's effective set, but the
    // matrix this surface renders is bound to granted_permissions, which a default change leaves
    // untouched.
    const setDefaults = useCallback(async (next: Permission[]) => {
        setBusy(true);
        try {
            dispatch(receivedSpaceAccess(await setDefaultPermissions(space.id, next)));
        } catch (error) {
            toast.error(permissionWriteError(error));
            throw error;
        } finally {
            setBusy(false);
        }
    }, [dispatch, space.id, permissionWriteError]);

    const setMemberGrants = useCallback(async (userId: string, next: Permission[]) => {
        setBusy(true);
        try {
            const updated = await setMemberPermissions(space.id, userId, next);
            setMembers((current) => new Map(current).set(updated.user_id, updated));

            // Editing your own row can revoke your own authority — dropping admin_space is a legal
            // write whenever you are not the last administrator. The tier flags were resolved at
            // load, so without this the surface keeps offering controls the server will now refuse
            // and the failure arrives as a bare error. Re-derived from the response's effective set
            // rather than from `next`, which is the granted set and says nothing about what the
            // space default already confers.
            if (updated.user_id === currentUserId) {
                await reloadTiers();
            }
        } catch (error) {
            toast.error(permissionWriteError(error));
            throw error;
        } finally {
            setBusy(false);
        }
    }, [space.id, currentUserId, reloadTiers, permissionWriteError]);

    const setViewAccess = useCallback(async (next: SpaceViewAccess) => {
        setBusy(true);
        try {
            dispatch(receivedSpaceAccess(await setSpaceViewAccess(space.id, next, stored.update_at)));
        } catch (error) {
            toast.error(permissionWriteError(error));
            throw error;
        } finally {
            setBusy(false);
        }
    }, [dispatch, space.id, stored.update_at, permissionWriteError]);

    // Both tiers are additionally gated on this surface's own read having landed. The tier itself is
    // the store's answer, but a surface whose read failed holds nothing to administer — leaving the
    // controls live on a stale tier would offer edits against state it never loaded.
    return {
        defaults,
        viewAccess,
        members,
        canAdminister: storeCanAdminister && !loadFailed,
        canManageMembers: storeCanManageMembers && !loadFailed,
        loading,
        loadFailed,
        busy,
        setDefaults,
        setMemberGrants,
        setViewAccess,
    };
}
