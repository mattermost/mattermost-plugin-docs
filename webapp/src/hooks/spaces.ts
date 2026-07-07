// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useForm} from '@tanstack/react-form';
import {docsDataSource} from 'data';
import {useTeamContext} from 'hooks/team';
import {useCallback, useMemo, useRef} from 'react';
import {slugify, spaceFormSchema} from 'validation/space_schema';

import type {Space, SpaceSummary, SpaceVisibility} from 'types/docs';

export function useSpaces(): Space[] {
    return docsDataSource.listSpaces();
}

export function useSpace(id?: string): Space | undefined {
    return id ? docsDataSource.getSpace(id) : undefined;
}

export function useRecentSpaceSummaries(): SpaceSummary[] {
    return docsDataSource.getRecentSpaceSummaries();
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
// uniqueness schema on blur. Data access stays behind DocsDataSource, so
// swapping the mock source for a real one touches nothing here.
export function useCreateSpace({onCreated}: CreateSpaceOptions = {}) {
    const {name: teamName} = useTeamContext();

    const slugSchema = spaceFormSchema.shape.slug;

    // Stop deriving the slug from the name once the user edits the slug directly.
    const slugEdited = useRef(false);

    const form = useForm({
        defaultValues: INITIAL_VALUES,
        validators: {onSubmitAsync: spaceFormSchema},
        onSubmit: async ({value}) => {
            const space = await Promise.resolve(docsDataSource.createSpace({
                ...value,
                name: value.name.trim(),
                slug: value.slug.trim(),
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

    const baseUrl = useMemo(() => `${window.location.origin}/${teamName}/docs`, [teamName]);

    return {form, slugSchema, baseUrl, changeName, changeSlug};
}
