// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useForm} from '@tanstack/react-form';
import {getSpaceViews, recordSpaceView} from 'data/recent_spaces';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useTeamContext} from 'hooks/team';
import {useCallback, useEffect, useMemo, useRef} from 'react';
import {DOCS_KEYWORD} from 'routing/paths';
import {createSpaceFormSchema, slugify} from 'validation/space_schema';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {createSpace} from 'store/actions';
import {getAllSpaces, getSpace, getSpacesForCurrentTeam} from 'store/selectors';

import type {UrlInputHandle} from 'components/form-controls/url_input';

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
    slug: string;
    visibility: SpaceVisibility;
    description: string;
};

const INITIAL_VALUES: CreateSpaceValues = {
    name: '',
    slug: '',
    visibility: 'public',
    description: '',
};

type CreateSpaceOptions = {
    onCreated?: (space: Space) => void;
};

// Owns the create-space form via TanStack Form. The Zod schema drives validation
// through TanStack's validators (its issues distribute to fields by path). The
// slug is a client-only vanity field for now — the server assigns an opaque id
// and has no slug concept, so there's no uniqueness check; only format is
// validated (the slug's on-blur schema).
export function useCreateSpace({onCreated}: CreateSpaceOptions = {}) {
    const {name: teamName} = useTeamContext();
    const dispatch = useAppDispatch();

    const formSchema = useMemo(() => createSpaceFormSchema(), []);
    const slugSchema = useMemo(() => formSchema.shape.slug, [formSchema]);

    // Stop deriving the slug from the name once the user edits the slug directly.
    const slugEdited = useRef(false);

    const urlInputRef = useRef<UrlInputHandle>(null);

    const form = useForm({
        defaultValues: INITIAL_VALUES,
        validators: {onSubmitAsync: formSchema},
        onSubmit: async ({value}) => {
            const space = await dispatch(createSpace({
                title: value.name.trim(),
                slug: value.slug.trim(),
                visibility: value.visibility,
                description: value.description.trim() || undefined,
            }));
            onCreated?.(space);
        },
    });

    const changeName = useCallback((name: string) => {
        form.setFieldValue('name', name);
        if (!slugEdited.current) {
            form.setFieldValue('slug', slugify(name));
        }
    }, [form]);

    const changeSlug = useCallback((slug: string) => {
        slugEdited.current = true;
        form.setFieldValue('slug', slug);
    }, [form]);

    // Submits, then surfaces a rejected slug (e.g. a bad format) by focusing the
    // URL input — otherwise the error lands on the field's read-only preview.
    const submit = useCallback(async () => {
        await form.handleSubmit();
        if ((form.getFieldMeta('slug')?.errors.length ?? 0) > 0) {
            urlInputRef.current?.focus();
        }
    }, [form]);

    const baseUrl = useMemo(() => `${window.location.origin}/${teamName}/${DOCS_KEYWORD}`, [teamName]);

    return {form, slugSchema, baseUrl, changeName, changeSlug, submit, urlInputRef};
}
