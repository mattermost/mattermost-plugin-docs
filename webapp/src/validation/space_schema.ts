// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {z} from 'zod';

// Validation for the create-space form, consumed as TanStack Form validators.
// The schema is UI-agnostic: each failing check is tagged with a stable
// SpaceValidationError id, which the UI layer maps to a localized message
// (see create_space_modal/validation_messages). Field keys match the form
// state so TanStack distributes each issue to its field by path.

export const SPACE_NAME_MAX_LENGTH = 64;
export const SPACE_DESCRIPTION_MAX_LENGTH = 1024;

// Stable ids for each validation failure; the UI maps these to messages.
export const SpaceValidationError = {
    NameRequired: 'name.required',
    NameTooLong: 'name.tooLong',
    DescriptionTooLong: 'description.tooLong',
} as const;

export type SpaceValidationError = typeof SpaceValidationError[keyof typeof SpaceValidationError];

// One schema for the whole form. Field keys match the form state so TanStack
// distributes each issue to its field by path.
export function createSpaceFormSchema() {
    return z.object({
        name: z.
            string().
            trim().
            min(1, {error: SpaceValidationError.NameRequired}).
            max(SPACE_NAME_MAX_LENGTH, {error: SpaceValidationError.NameTooLong}),
        visibility: z.enum(['public', 'private']),

        // Required string (may be empty) to match the always-present form field;
        // the submit handler converts an empty description to undefined.
        description: z.
            string().
            trim().
            max(SPACE_DESCRIPTION_MAX_LENGTH, {error: SpaceValidationError.DescriptionTooLong}),
    });
}

export type SpaceFormValues = z.infer<ReturnType<typeof createSpaceFormSchema>>;
