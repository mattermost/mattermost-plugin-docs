// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';
import {z} from 'zod';

// Validation for the create-space form, consumed as TanStack Form validators.
// The schema is UI-agnostic: each failing check is tagged with a stable
// SpaceValidationError id, which the UI layer maps to a localized message
// (see create_space_modal/validation_messages). Field keys match the form
// state so TanStack distributes each issue to its field by path.

export const SPACE_NAME_MAX_LENGTH = 64;
export const SPACE_SLUG_MAX_LENGTH = 64;
export const SPACE_DESCRIPTION_MAX_LENGTH = 1024;

// Lowercase alphanumerics and single dashes, not leading/trailing — the same
// shape Mattermost uses for channel URL names.
const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

// Stable ids for each validation failure; the UI maps these to messages.
export const SpaceValidationError = {
    NameRequired: 'name.required',
    NameTooLong: 'name.tooLong',
    UrlRequired: 'url.required',
    UrlTooLong: 'url.tooLong',
    UrlInvalid: 'url.invalid',
    UrlTaken: 'url.taken',
    DescriptionTooLong: 'description.tooLong',
} as const;

export type SpaceValidationError = typeof SpaceValidationError[keyof typeof SpaceValidationError];

// One statically-defined schema for the whole form. The slug field carries the
// async uniqueness refine, which calls the data source directly (the same client
// seam the hooks use); its format checks abort on failure so a malformed slug
// never reaches the server. Consumers that validate the slug on its own (the URL
// field, on blur) derive it with `spaceFormSchema.shape.slug`.
export const spaceFormSchema = z.object({
    name: z.
        string().
        trim().
        min(1, {error: SpaceValidationError.NameRequired}).
        max(SPACE_NAME_MAX_LENGTH, {error: SpaceValidationError.NameTooLong}),
    slug: z.
        string().
        trim().
        min(1, {error: SpaceValidationError.UrlRequired, abort: true}).
        max(SPACE_SLUG_MAX_LENGTH, {error: SpaceValidationError.UrlTooLong, abort: true}).
        regex(SLUG_PATTERN, {error: SpaceValidationError.UrlInvalid, abort: true}).
        refine(async (slug) => docsDataSource.isSlugAvailable(slug), {error: SpaceValidationError.UrlTaken}),
    visibility: z.enum(['public', 'private']),

    // Required string (may be empty) to match the always-present form field;
    // the submit handler converts an empty description to undefined.
    description: z.
        string().
        trim().
        max(SPACE_DESCRIPTION_MAX_LENGTH, {error: SpaceValidationError.DescriptionTooLong}),
});

export type SpaceFormValues = z.infer<typeof spaceFormSchema>;

export function slugify(value: string): string {
    return value.
        toLowerCase().
        trim().
        replace(/[^a-z0-9]+/g, '-').
        replace(/^-+|-+$/g, '');
}
