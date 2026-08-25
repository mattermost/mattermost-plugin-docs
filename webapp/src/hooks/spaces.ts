// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useForm} from '@tanstack/react-form';
import {getSpaceViews, recordSpaceView} from 'data/recent_spaces';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {createSpaceFormSchema} from 'validation/space_schema';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {createSpace, fetchDrafts, fetchPages, fetchSpace, fetchSpaceMembers} from 'store/actions';
import {areMembersLoadedForSpace, getAllSpaces, getPagesForSpace, getSpace, getSpaceMemberIds, getSpacesForCurrentTeam} from 'store/selectors';

import {toast} from 'components/toast';

import type {Space, SpaceSummary} from 'types/docs';
import type {SpaceViewAccess} from 'types/permissions';

export function useSpaces(): Space[] {
    return useAppSelector(getSpacesForCurrentTeam);
}

// All of the user's spaces across teams — for the cross-team docs switcher.
export function useAllSpaces(): Space[] {
    return useAppSelector(getAllSpaces);
}

export function useSpace(id?: string): Space | undefined {
    return useAppSelector((state) => (id ? getSpace(state, id) : undefined));
}

export type RoutedSpace = {
    space?: Space;

    // False until the id has an answer. A routed id that isn't in the store yet is
    // not yet known to be bad, and correcting the URL before the answer is in turns
    // a slow response into a bounce out of a space that is really there.
    resolved: boolean;
};

/**
 * Resolves a space id that came from the URL, fetching it by id when the store
 * doesn't hold it.
 *
 * The team listing is not enough on its own: it can predate the space, or belong to
 * another team, so an id missing from it is not an id that doesn't exist. Asking the
 * server for the id itself is the only answer a deep link can trust.
 *
 * The fetch runs only while the space is absent, so an id already in the store costs
 * nothing and a failed lookup isn't retried in a loop (its absence is the state that
 * gated the effect, and it doesn't change).
 */
export function useRoutedSpace(spaceId?: string): RoutedSpace {
    const dispatch = useAppDispatch();
    const space = useSpace(spaceId);
    const [checkedId, setCheckedId] = useState<string>();
    const missing = Boolean(spaceId) && !space;

    useEffect(() => {
        if (!spaceId || !missing) {
            return undefined;
        }
        let active = true;
        dispatch(fetchSpace(spaceId)).then(() => {
            if (active) {
                setCheckedId(spaceId);
            }
        });
        return () => {
            active = false;
        };
    }, [dispatch, spaceId, missing]);

    // No routed id is nothing to resolve, so it must not read as answered — the
    // caller redirects on an answered id with no space.
    return {space, resolved: Boolean(spaceId) && (Boolean(space) || checkedId === spaceId)};
}

const resolveMaxAttempts = 3;
const resolveRetryDelayMs = 2000;

/**
 * Resolves the caller's own permissions for the space being viewed.
 *
 * Separate from useRoutedSpace, which fetches only while the space is absent: the team
 * listing puts every space in the store without permissions (it answers with bare spaces),
 * so by the time a space is opened from the sidebar it is present-but-unresolved and that
 * effect correctly does nothing. Permission-gated affordances would then never see a
 * resolved set.
 *
 * Runs once per SUCCESSFUL resolution rather than once per attempt, and re-reads on a change of id
 * so switching spaces cannot carry the previous space's permissions.
 *
 * The success condition matters: fetchSpace answers any failure — a network blip, or the 403 a
 * private space gives a non-member — by resolving to undefined rather than rejecting. Marking the
 * id resolved before knowing the outcome therefore made one transient failure permanent for the
 * mounted view, and the two selectors reading that field fail in opposite directions: page creation
 * is offered on an unresolved set and member management is withheld from one.
 *
 * A failed attempt is retried up to resolveMaxAttempts times; past that the space is left
 * unresolved until the id changes or the view remounts.
 */
export function useResolveSpacePermissions(spaceId?: string): void {
    const dispatch = useAppDispatch();
    const resolvedFor = useRef<string>();
    const attempts = useRef<{spaceId?: string; count: number}>({count: 0});
    const [retry, setRetry] = useState(0);

    useEffect(() => {
        let cancelled = false;
        let timer: ReturnType<typeof setTimeout> | undefined;

        if (spaceId && resolvedFor.current !== spaceId) {
            // The count belongs to the id being resolved, so a space switch starts a fresh budget
            // rather than inheriting one the previous space spent.
            if (attempts.current.spaceId !== spaceId) {
                attempts.current = {spaceId, count: 0};
            }
            resolvedFor.current = spaceId;
            dispatch(fetchSpace(spaceId)).then((space) => {
                // Guarded on the id so a resolution that lost a space switch cannot reopen the
                // current space's.
                if (cancelled || space || resolvedFor.current !== spaceId) {
                    return;
                }
                resolvedFor.current = undefined;
                attempts.current.count += 1;

                // Clearing the marker cannot by itself produce another attempt: this effect re-runs
                // only when one of its dependencies changes, and a failure changes neither dispatch
                // nor spaceId. The retry counter is state for that reason.
                if (attempts.current.count < resolveMaxAttempts) {
                    timer = setTimeout(() => setRetry((n) => n + 1), resolveRetryDelayMs);
                }
            });
        }

        return () => {
            cancelled = true;
            clearTimeout(timer);
            if (resolvedFor.current === spaceId) {
                resolvedFor.current = undefined;
            }
        };
    }, [dispatch, spaceId, retry]);
}

// Recently-viewed spaces in the current team (Home). Recency is client-side
// today (see data/recent_spaces); resolved against the loaded team spaces so a
// left/deleted space drops out. pageCount is omitted until the server provides
// one.
export function useRecentSpaceSummaries(): SpaceSummary[] {
    const userId = useAppSelector(getCurrentUserId);
    const teamSpaces = useAppSelector(getSpacesForCurrentTeam);
    return useMemo(() => {
        const byId = new Map(teamSpaces.map((space) => [space.id, space]));
        return getSpaceViews(userId).flatMap(({spaceId, lastViewedAt}) => {
            const space = byId.get(spaceId);
            return space ? [{space, lastViewedAt}] : [];
        });
    }, [userId, teamSpaces]);
}

export type SpaceStats = {
    pageCount: number;

    // Undefined until the member list arrives (or if it failed): a real space is
    // never memberless, so rendering 0 would state something untrue.
    memberCount?: number;
};

// Loads and returns a space's page and member counts. Fetches the space's pages,
// members, and the caller's drafts on mount, so the page tree and member avatars
// can reuse the same data later. The view count has no server source yet, so it
// isn't included.
export function useSpaceStats(spaceId: string): SpaceStats {
    const dispatch = useAppDispatch();

    useEffect(() => {
        dispatch(fetchPages(spaceId));
        dispatch(fetchSpaceMembers(spaceId));
        dispatch(fetchDrafts(spaceId));
    }, [dispatch, spaceId]);

    const pages = useAppSelector((state) => getPagesForSpace(state, spaceId));
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, spaceId));
    const membersLoaded = useAppSelector((state) => areMembersLoadedForSpace(state, spaceId));

    return {pageCount: pages.length, memberCount: membersLoaded ? memberIds.length : undefined};
}

// Records that the current user viewed a space, feeding the recently-viewed
// list. No-op until both ids are known.
export function useRecordSpaceView(spaceId?: string): void {
    const userId = useAppSelector(getCurrentUserId);
    useEffect(() => {
        if (userId && spaceId) {
            recordSpaceView(userId, spaceId, Date.now());
        }
    }, [userId, spaceId]);
}

type CreateSpaceValues = {
    name: string;
    view_access: SpaceViewAccess;
    description: string;
};

const INITIAL_VALUES: CreateSpaceValues = {
    name: '',
    view_access: 'private',
    description: '',
};

type CreateSpaceOptions = {
    onCreated?: (space: Space) => void;
};

// Owns the create-space form via TanStack Form. The Zod schema drives validation
// through TanStack's validators (its issues distribute to fields by path).
export function useCreateSpace({onCreated}: CreateSpaceOptions = {}) {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();

    const formSchema = useMemo(() => createSpaceFormSchema(), []);

    const form = useForm({
        defaultValues: INITIAL_VALUES,
        validators: {onSubmitAsync: formSchema},
        onSubmit: async ({value}) => {
            const space = await dispatch(createSpace({
                title: value.name.trim(),
                view_access: value.view_access,
                description: value.description.trim() || undefined,
            }));
            onCreated?.(space);
        },
    });

    const changeName = useCallback((name: string) => {
        form.setFieldValue('name', name);
    }, [form]);

    const submit = useCallback(() => form.handleSubmit().catch((error) => {
        toast.error(formatMessage({
            id: 'docs.createSpace.error.submit',
            defaultMessage: 'Could not create the space. Please try again.',
        }));
        // eslint-disable-next-line no-console
        console.error('Docs: failed to create space', error);
    }), [form, formatMessage]);

    return {form, changeName, submit};
}
