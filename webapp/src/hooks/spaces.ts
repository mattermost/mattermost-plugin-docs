// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useForm} from '@tanstack/react-form';
import {useAppDispatch, useAppSelector, useAppStore} from 'hooks/redux';
import {useTeamContext} from 'hooks/team';
import {useCallback, useMemo, useRef} from 'react';
import {DOCS_KEYWORD} from 'routing/paths';
import {createSpaceFormSchema, slugify} from 'validation/space_schema';

import {createSpace} from 'store/actions';
import {getRecentSpaceSummaries, getSpace, getSpaces, isSlugAvailable} from 'store/selectors';

import type {Space, SpaceSummary, SpaceVisibility} from 'types/docs';

export function useSpaces(): Space[] {
    return useAppSelector(getSpaces);
}

export function useSpace(id?: string): Space | undefined {
    return useAppSelector((state) => (id ? getSpace(state, id) : undefined));
}

export function useRecentSpaceSummaries(): SpaceSummary[] {
    return useAppSelector(getRecentSpaceSummaries);
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

// Owns the create-space form via TanStack Form. The existing Zod schemas drive
// validation through TanStack's validators — the whole-form schema on submit
// (its issues distribute to fields by path) and the slug's async format +
// uniqueness schema on blur. The uniqueness check reads current store state
// (a Zod refine isn't a React hook, so it reads the store snapshot rather than
// subscribing) so a space created earlier in the same session is accounted for.
export function useCreateSpace({onCreated}: CreateSpaceOptions = {}) {
    const {name: teamName} = useTeamContext();
    const dispatch = useAppDispatch();
    const store = useAppStore();

    const checkSlugAvailable = useCallback((slug: string) => isSlugAvailable(store.getState(), slug), [store]);
    const formSchema = useMemo(() => createSpaceFormSchema(checkSlugAvailable), [checkSlugAvailable]);
    const slugSchema = useMemo(() => formSchema.shape.slug, [formSchema]);

    // Stop deriving the slug from the name once the user edits the slug directly.
    const slugEdited = useRef(false);

    const form = useForm({
        defaultValues: INITIAL_VALUES,
        validators: {onSubmitAsync: formSchema},
        onSubmit: async ({value}) => {
            const space = await Promise.resolve(dispatch(createSpace({
                title: value.name.trim(),
                slug: value.slug.trim(),
                visibility: value.visibility,
                description: value.description.trim() || undefined,
            })));
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

    const baseUrl = useMemo(() => `${window.location.origin}/${teamName}/${DOCS_KEYWORD}`, [teamName]);

    return {form, slugSchema, baseUrl, changeName, changeSlug};
}
