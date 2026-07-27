// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineMessages} from 'react-intl';
import type {IntlShape} from 'react-intl';
import {SPACE_DESCRIPTION_MAX_LENGTH, SPACE_NAME_MAX_LENGTH, SPACE_SLUG_MAX_LENGTH, SpaceValidationError} from 'validation/space_schema';

type FormatMessage = IntlShape['formatMessage'];

const messages = defineMessages({
    nameRequired: {id: 'docs.createSpace.error.name.required', defaultMessage: 'Please enter a name for the space'},
    nameTooLong: {id: 'docs.createSpace.error.name.tooLong', defaultMessage: 'Name must be {max} characters or fewer'},
    urlRequired: {id: 'docs.createSpace.error.url.required', defaultMessage: 'Please enter a URL for the space'},
    urlTooLong: {id: 'docs.createSpace.error.url.tooLong', defaultMessage: 'URL must be {max} characters or fewer'},
    urlInvalid: {id: 'docs.createSpace.error.url.invalid', defaultMessage: 'Use lowercase letters, numbers, and dashes, with no spaces'},
    descriptionTooLong: {id: 'docs.createSpace.error.description.tooLong', defaultMessage: 'Description must be {max} characters or fewer'},
});

// Maps a schema validation id (surfaced through TanStack meta.errors) to a
// localized message. The schema stays UI-agnostic; wording and interpolation
// live here in the UI layer.
export function resolveSpaceValidationError(id: string, formatMessage: FormatMessage): string {
    switch (id) {
    case SpaceValidationError.NameRequired:
        return formatMessage(messages.nameRequired);
    case SpaceValidationError.NameTooLong:
        return formatMessage(messages.nameTooLong, {max: SPACE_NAME_MAX_LENGTH});
    case SpaceValidationError.UrlRequired:
        return formatMessage(messages.urlRequired);
    case SpaceValidationError.UrlTooLong:
        return formatMessage(messages.urlTooLong, {max: SPACE_SLUG_MAX_LENGTH});
    case SpaceValidationError.UrlInvalid:
        return formatMessage(messages.urlInvalid);
    case SpaceValidationError.DescriptionTooLong:
        return formatMessage(messages.descriptionTooLong, {max: SPACE_DESCRIPTION_MAX_LENGTH});
    default:
        return id;
    }
}

// First error from a TanStack meta.errors array, resolved to a message. Standard
// Schema (Zod) issues are objects carrying our validation id as their message.
export function firstSpaceValidationError(errors: readonly unknown[], formatMessage: FormatMessage): string | undefined {
    const first = errors[0];
    if (typeof first === 'string') {
        return resolveSpaceValidationError(first, formatMessage);
    }
    if (first && typeof first === 'object' && 'message' in first) {
        return resolveSpaceValidationError(String((first as {message: unknown}).message), formatMessage);
    }
    return undefined;
}
