// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';

import type {Page} from 'types/docs';
import {ConflictReason} from 'types/drafts';
import type {ConflictReasonType, PublishConflict} from 'types/drafts';

/**
 * A publish the server refused because the page moved underneath the draft.
 *
 * Publish is the one Docs endpoint whose error body carries data — the current
 * server page — so the rejection is unpacked into a typed error here rather than
 * leaving every call site to reach into `RestError.body`.
 */
export class PublishConflictError extends Error {
    reason: ConflictReasonType | string;

    // The page as the server currently has it, or null when the re-read failed.
    currentPage: Page | null;

    constructor(conflict: PublishConflict) {
        super(conflict.error?.message ?? 'Publish conflict');
        this.name = 'PublishConflictError';
        this.reason = conflict.error?.id ?? '';
        this.currentPage = conflict.current_page ?? null;
    }

    // Force-publishing overwrites a concurrent edit, which is a choice the user can
    // make. An unpublished parent is not: the parent has to be published first, so
    // offering "publish anyway" there would just fail again.
    get isForceable(): boolean {
        return this.reason !== ConflictReason.ParentUnpublished;
    }
}

const isPublishConflict = (body: unknown): body is PublishConflict =>
    Boolean(body) && typeof body === 'object' && 'current_page' in (body as object);

// Rethrows a publish rejection as PublishConflictError when it is one, and
// unchanged otherwise.
export function asPublishConflict(error: unknown): never {
    if (error instanceof RestError && error.status === 409 && isPublishConflict(error.body)) {
        throw new PublishConflictError(error.body);
    }
    throw error;
}
