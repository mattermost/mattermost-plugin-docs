// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useForm} from '@tanstack/react-form';
import {getSpaceViews, recordSpaceView} from 'data/recent_spaces';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback, useEffect, useMemo, useState} from 'react';
import {useIntl} from 'react-intl';
import {createSpaceFormSchema} from 'validation/space_schema';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {createSpace, fetchDrafts, fetchPages, fetchSpace, fetchSpaceMembers} from 'store/actions';
import {areMembersLoadedForSpace, getAllSpaces, getPagesForSpace, getSpace, getSpaceMemberIds, getSpacesForCurrentTeam} from 'store/selectors';

import {toast} from 'components/toast';

import type {Space, SpaceSummary, SpaceVisibility} from 'types/docs';

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
    visibility: SpaceVisibility;
    description: string;
};

const INITIAL_VALUES: CreateSpaceValues = {
    name: '',
    visibility: 'private',
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
                visibility: value.visibility,
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
