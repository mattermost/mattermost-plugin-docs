// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useForm} from '@tanstack/react-form';
import {getSpaceViews, recordSpaceView} from 'data/recent_spaces';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback, useEffect, useMemo} from 'react';
import {createSpaceFormSchema} from 'validation/space_schema';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {createSpace, fetchPages, fetchSpaceMembers} from 'store/actions';
import {getAllSpaces, getPagesForSpace, getSpace, getSpaceMemberIds, getSpacesForCurrentTeam} from 'store/selectors';

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
    memberCount: number;
};

// Loads and returns a space's page and member counts. Fetches on mount into the
// store, so the page tree and member avatars can reuse the same data later. The
// view count has no server source yet, so it isn't included.
export function useSpaceStats(spaceId: string): SpaceStats {
    const dispatch = useAppDispatch();

    useEffect(() => {
        dispatch(fetchPages(spaceId));
        dispatch(fetchSpaceMembers(spaceId));
    }, [dispatch, spaceId]);

    const pages = useAppSelector((state) => getPagesForSpace(state, spaceId));
    const memberIds = useAppSelector((state) => getSpaceMemberIds(state, spaceId));

    return {pageCount: pages.length, memberCount: memberIds.length};
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
    visibility: 'public',
    description: '',
};

type CreateSpaceOptions = {
    onCreated?: (space: Space) => void;
};

// Owns the create-space form via TanStack Form. The Zod schema drives validation
// through TanStack's validators (its issues distribute to fields by path).
export function useCreateSpace({onCreated}: CreateSpaceOptions = {}) {
    const dispatch = useAppDispatch();

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

    const submit = useCallback(() => form.handleSubmit(), [form]);

    return {form, changeName, submit};
}
