// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';
import {getSpaceAccess, listAllSpaceMembers, setDefaultPermissions, setMemberPermissions, setSpaceViewAccess} from 'client/space_permissions';
import {useCanAdministerSpace, useCanManageSpaceMembers} from 'hooks/permissions';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {receivedSpaceAccess} from 'store/actions';
import {getSpace, getSpaceMemberPermissionsRevision, getSpaceMemberIds} from 'store/selectors';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';
import {Permissions} from 'types/permissions';
import type {Permission, SpaceMember, SpaceViewAccess} from 'types/permissions';
import {CUSTOM_PERMISSION_SCHEME_LICENSE_ERROR_ID, GUEST_NOT_ASSIGNABLE_ERROR_ID, LAST_SPACE_ADMIN_ERROR_ID, SPACE_LOCK_TIMEOUT_ERROR_ID} from 'types/server_errors';

export type SpacePermissions = {

    /** The space's default permission set, before per-member grants. */
    defaults: Permission[];
    viewAccess: SpaceViewAccess;

    /** Every member's granted set and role flags, keyed by user id. */
    members: Map<string, SpaceMember>;

    /** Whether this surface may enable default and view-access changes. */
    canAdminister: boolean;

    /** Whether this surface may expose member and grant controls. */
    canManageMembers: boolean;

    /** A permission-matrix read is in flight. */
    loading: boolean;

    /** The permission matrix is unavailable; empty state must not be treated as server truth. */
    loadFailed: boolean;

    /** A mutation is in flight; write affordances should be disabled. */
    busy: boolean;

    setDefaults: (next: Permission[]) => Promise<void>;
    setMemberGrants: (userId: string, next: Permission[]) => Promise<void>;
    setViewAccess: (next: SpaceViewAccess) => Promise<void>;
};

const serverErrorId = (error: unknown): string | undefined =>
    (error instanceof RestError ? error.server_error_id : undefined);

// Preserve selector identity while defaults are unresolved.
const EMPTY_PERMISSIONS: Permission[] = [];

/**
 * The space's permission state and the mutations that change it.
 *
 * Shared space access lives in Redux. The manage-only member matrix stays local and is invalidated
 * by a store revision. Mutation results remain server-derived because effective permissions also
 * depend on defaults and role flags.
 */
export function useSpacePermissions(space: Space): SpacePermissions {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const currentUserId = useAppSelector(getCurrentUserId);

    // Permission-gated surfaces share the same server-resolved access record.
    const storeCanAdminister = useCanAdministerSpace(space.id);
    const storeCanManageMembers = useCanManageSpaceMembers(space.id);

    // Store membership changes invalidate the hook-local matrix.
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, space.id));

    // Grant details are not stored in Redux; the revision invalidates their local snapshot.
    const grantRevision = useAppSelector((state) => getSpaceMemberPermissionsRevision(state, space.id));

    // Shared fields stay on the canonical space record; deep links may supply the fallback.
    const stored = useAppSelector((state) => getSpace(state, space.id)) ?? space;
    const viewAccess = stored.view_access;
    const defaults = stored.default_permissions ?? EMPTY_PERMISSIONS;

    const [members, setMembers] = useState<Map<string, SpaceMember>>(new Map());
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const [busy, setBusy] = useState(false);

    // Roster invalidation needs the latest manage tier without depending on every space update.
    const canManageMembersRef = useRef(storeCanManageMembers);
    canManageMembersRef.current = storeCanManageMembers;

    // Only a space change requires another access read; other triggers reload the local matrix
    // against the latest resolved tier.
    const previousLoadTrigger = useRef<{
        spaceId: string;
        memberIds: string[];
        grantRevision: number;
    }>();

    const genericError = useCallback(() => formatMessage({
        id: 'docs.spacePermissions.error.generic',
        defaultMessage: 'Something went wrong. Please try again.',
    }), [formatMessage]);

    // Preserve retryable conflicts and rule-specific refusals instead of flattening them into the
    // generic write error. The individual endpoints do not share one concurrency contract.
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
        if (id === CUSTOM_PERMISSION_SCHEME_LICENSE_ERROR_ID) {
            return formatMessage({
                id: 'docs.spacePermissions.error.customSchemeLicense',
                defaultMessage: 'Custom permission combinations require a Professional or Enterprise license.',
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

    // Self-grant changes can alter authority through several independent tiers; re-resolve it.
    const reloadTiers = useCallback(async () => {
        try {
            dispatch(receivedSpaceAccess(await getSpaceAccess(space.id)));
        } catch {
            // The grant write succeeded; a failed follow-up must not report it as rejected.
        }
    }, [dispatch, space.id]);

    useEffect(() => {
        const previous = previousLoadTrigger.current;
        const loadAccess = previous?.spaceId !== space.id;
        previousLoadTrigger.current = {spaceId: space.id, memberIds, grantRevision};

        let cancelled = false;

        // Never retain a matrix after its read becomes untrustworthy.
        const clearResolved = () => setMembers(new Map());

        const load = async () => {
            setLoading(true);
            setLoadFailed(false);
            try {
                let canManage = canManageMembersRef.current;
                if (loadAccess) {
                    const access = await getSpaceAccess(space.id);
                    if (cancelled) {
                        return;
                    }

                    // The server-resolved record is authoritative even when the caller is absent
                    // from the roster, as a system administrator may be.
                    dispatch(receivedSpaceAccess(access));
                    canManage = access.permissions.includes(Permissions.MANAGE_SPACE);
                }

                // A non-manager receives a redacted roster; it is not a usable empty matrix.
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
                    // Disabled empty state represents an unavailable matrix without mount-time
                    // error toasts.
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

    // Defaults do not change the granted_permissions rendered by the matrix.
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

            // A successful self-edit may revoke authority even when the submitted grant alone
            // cannot reveal the resulting effective tier.
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

    // A valid tier cannot make an unavailable matrix safe to edit.
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
