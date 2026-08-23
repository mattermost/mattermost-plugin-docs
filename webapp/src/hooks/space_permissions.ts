// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';
import {getSpaceAccess, listAllSpaceMembers, setDefaultPermissions, setMemberPermissions, setSpaceViewAccess} from 'client/space_permissions';
import {useAppSelector} from 'hooks/redux';
import {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {getSpaceMemberIds} from 'store/selectors';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';
import {Permissions} from 'types/permissions';
import type {Permission, SpaceMember, SpaceViewAccess} from 'types/permissions';
import {GUEST_NOT_ASSIGNABLE_ERROR_ID, LAST_SPACE_ADMIN_ERROR_ID, SPACE_LOCK_TIMEOUT_ERROR_ID} from 'types/server_errors';

export type SpacePermissions = {

    /** The space's default permission set — what every member holds without a grant. */
    defaults: Permission[];
    viewAccess: SpaceViewAccess;

    /** Every member's granted set and role flags, keyed by user id. */
    members: Map<string, SpaceMember>;

    /**
     * Whether the current user may change the space's own exposure policy — the default
     * permission set and view_access. Sysadmin or space admin only.
     */
    canAdminister: boolean;

    /**
     * Whether the current user may read and write the member roster. A strictly wider tier than
     * canAdminister: the roster routes also admit a team manage_space holder, who holds no
     * admin_space and would be locked out of the roster if this were derived from canAdminister.
     * Taken from the server rather than computed here.
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

/**
 * The space's permission state and the mutations that change it.
 *
 * Read straight from the permissions client rather than through the Docs data source:
 * permission state is only ever displayed by this surface, so putting it in the shared
 * store would make every other consumer carry state none of them read.
 *
 * Every mutation re-reads from the server response rather than patching local state
 * optimistically. The server derives the effective set from the space default, the
 * grant and the member's role flags, so a locally-computed guess would disagree with
 * it exactly in the cases that matter (a guest, or an admin whose grant is redundant).
 */
export function useSpacePermissions(space: Space): SpacePermissions {
    const {formatMessage} = useIntl();
    const currentUserId = useAppSelector(getCurrentUserId);

    // The roster this hook reads is its own snapshot, taken once per load. Membership changes
    // elsewhere — the tab's own add/remove field, or another administrator's change arriving over
    // the websocket — land in the store, so the store's member list is what says the snapshot is
    // stale. Without it a member added while this surface is open renders with no permission row,
    // because the grant matrix has no record of them.
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, space.id));

    const [defaults, setDefaultsState] = useState<Permission[]>([]);
    const [viewAccess, setViewAccessState] = useState<SpaceViewAccess>('open');
    const [members, setMembers] = useState<Map<string, SpaceMember>>(new Map());
    const [canAdminister, setCanAdminister] = useState(false);
    const [canManageMembers, setCanManageMembers] = useState(false);
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const [busy, setBusy] = useState(false);

    // The optimistic-concurrency baseline for a view-access change. Held in a ref
    // because a mutation reads the newest value without re-running the load effect.
    const updateAtRef = useRef(0);

    // busy is shared by three independent writes (defaults, a member's grants, view
    // access), so it is reference-counted rather than a plain flag: a bare
    // setBusy(false) in each finally would let whichever write settled first re-enable
    // the controls while another was still in flight. That matters most for view
    // access, which carries an update_at baseline a second overlapping write would
    // send stale.
    const inFlightRef = useRef(0);
    const beginWrite = useCallback(() => {
        inFlightRef.current += 1;
        setBusy(true);
    }, []);
    const endWrite = useCallback(() => {
        inFlightRef.current -= 1;
        if (inFlightRef.current === 0) {
            setBusy(false);
        }
    }, []);

    const genericError = useCallback(() => formatMessage({
        id: 'docs.spacePermissions.error.generic',
        defaultMessage: 'Something went wrong. Please try again.',
    }), [formatMessage]);

    // Re-reads the caller's own authority from the server. Used after a write that can change it —
    // editing your own row — rather than reconstructing the new tiers locally: which tier granted
    // the roster (admin_space, a team grant, or sysadmin) is not recoverable from the response, and
    // guessing is how the surface and the routes drift apart in the first place.
    const reloadTiers = useCallback(async () => {
        try {
            const access = await getSpaceAccess(space.id);
            setCanAdminister(access.permissions.includes(Permissions.ADMIN_SPACE));
            setCanManageMembers(access.permissions.includes(Permissions.MANAGE_SPACE));
        } catch {
            // The write itself succeeded, so this must not surface as a failure. Falling closed
            // locks the surface until the next load, which is the safe direction: the alternative
            // is leaving controls enabled on authority that may be gone.
            setCanAdminister(false);
            setCanManageMembers(false);
        }
    }, [space.id]);

    useEffect(() => {
        let cancelled = false;

        // A failed read must not leave the previous space's answer standing, so the resolved values
        // clear together — the optimistic-lock baseline included, which a later write would
        // otherwise send against a space it was never read from.
        const clearResolved = () => {
            setDefaultsState([]);
            setViewAccessState('open');
            setMembers(new Map());
            setCanAdminister(false);
            setCanManageMembers(false);
            updateAtRef.current = 0;
        };

        const load = async () => {
            setLoading(true);
            setLoadFailed(false);
            try {
                const access = await getSpaceAccess(space.id);
                if (cancelled) {
                    return;
                }

                // The caller's own effective set, which the server resolves the same way the
                // write gate does — so a system administrator, who holds admin_space without
                // appearing in the roster at all, resolves here as able to administer.
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

                setDefaultsState(access.default_permissions);
                setViewAccessState(access.view_access);
                updateAtRef.current = access.update_at;
                setMembers(new Map(roster.map((member) => [member.user_id, member])));

                setCanAdminister(access.permissions.includes(Permissions.ADMIN_SPACE));
                setCanManageMembers(canManage);
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
    }, [space.id, memberIds]);

    const setDefaults = useCallback(async (next: Permission[]) => {
        beginWrite();
        try {
            const updated = await setDefaultPermissions(space.id, next);
            setDefaultsState(updated.default_permissions);
            updateAtRef.current = updated.update_at;

            // A default change alters what every member effectively holds, and the server
            // is the only place that composition happens. Failures are absorbed here: this
            // is a second request, made after the write already committed, so reporting it
            // would tell the caller their saved change failed. The roster stays stale until
            // the next load instead.
            const roster = await listAllSpaceMembers(space.id).catch(() => null);
            if (roster) {
                setMembers(new Map(roster.map((member) => [member.user_id, member])));
            }
        } catch (error) {
            toast.error(genericError());
            throw error;
        } finally {
            endWrite();
        }
    }, [space.id, genericError, beginWrite, endWrite]);

    const setMemberGrants = useCallback(async (userId: string, next: Permission[]) => {
        beginWrite();
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
            const id = serverErrorId(error);
            if (id === LAST_SPACE_ADMIN_ERROR_ID) {
                toast.error(formatMessage({
                    id: 'docs.spacePermissions.error.lastAdmin',
                    defaultMessage: 'A space must keep at least one administrator.',
                }));
            } else if (id === GUEST_NOT_ASSIGNABLE_ERROR_ID) {
                toast.error(formatMessage({
                    id: 'docs.spacePermissions.error.guest',
                    defaultMessage: 'Guests are read-only and cannot be granted permissions.',
                }));
            } else {
                toast.error(genericError());
            }
            throw error;
        } finally {
            endWrite();
        }
    }, [space.id, currentUserId, reloadTiers, formatMessage, genericError, beginWrite, endWrite]);

    const setViewAccess = useCallback(async (next: SpaceViewAccess) => {
        beginWrite();
        try {
            const updated = await setSpaceViewAccess(space.id, next, updateAtRef.current);
            setViewAccessState(updated.view_access);
            updateAtRef.current = updated.update_at;
        } catch (error) {
            const id = serverErrorId(error);
            if (id === SPACE_LOCK_TIMEOUT_ERROR_ID) {
                toast.error(formatMessage({
                    id: 'docs.spacePermissions.error.busy',
                    defaultMessage: 'This space is being changed right now. Try again in a moment.',
                }));
            } else if (error instanceof RestError && error.status === 409) {
                toast.error(formatMessage({
                    id: 'docs.spacePermissions.error.conflict',
                    defaultMessage: 'Someone else changed this space. Reopen settings and try again.',
                }));
            } else {
                toast.error(genericError());
            }
            throw error;
        } finally {
            endWrite();
        }
    }, [space.id, formatMessage, genericError, beginWrite, endWrite]);

    return {defaults, viewAccess, members, canAdminister, canManageMembers, loading, loadFailed, busy, setDefaults, setMemberGrants, setViewAccess};
}
