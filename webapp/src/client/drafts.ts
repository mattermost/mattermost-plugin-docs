// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from 'types/docs';
import type {Draft, DraftPatch, PageActiveEditors, PublishConflict} from 'types/drafts';

import {doFetch, RestError} from './rest';

const draftPath = (spaceId: string, pageId: string): string =>
    `/spaces/${encodeURIComponent(spaceId)}/pages/${encodeURIComponent(pageId)}/draft`;

export function createSpaceDraft(spaceId: string, title = '', parentId = ''): Promise<Draft> {
    return doFetch<Draft>(`/spaces/${encodeURIComponent(spaceId)}/drafts`, {
        method: 'POST',
        body: {title, parent_id: parentId},
    });
}

export function getPageDraft(spaceId: string, pageId: string, signal?: AbortSignal): Promise<Draft> {
    return doFetch<Draft>(draftPath(spaceId, pageId), {signal});
}

export function updatePageDraft(spaceId: string, pageId: string, patch: DraftPatch, signal?: AbortSignal): Promise<Draft> {
    return doFetch<Draft>(draftPath(spaceId, pageId), {
        method: 'PATCH',
        body: patch,
        signal,
    });
}

export function deletePageDraft(spaceId: string, pageId: string): Promise<void> {
    return doFetch<void>(draftPath(spaceId, pageId), {method: 'DELETE'});
}

export function getPageActiveEditors(spaceId: string, pageId: string, signal?: AbortSignal): Promise<PageActiveEditors> {
    return doFetch<PageActiveEditors>(
        `/spaces/${encodeURIComponent(spaceId)}/pages/${encodeURIComponent(pageId)}/active-editors`,
        {signal},
    );
}

export class PublishConflictError extends Error {
    reason: string;
    currentPage: Page | null;

    constructor(conflict: PublishConflict) {
        super(conflict.error?.message ?? 'Publish conflict');
        this.name = 'PublishConflictError';
        this.reason = conflict.error?.id ?? '';
        this.currentPage = conflict.current_page ?? null;
    }
}

const isPublishConflict = (body: unknown): body is PublishConflict =>
    Boolean(body) && typeof body === 'object' && 'current_page' in (body as object);

export async function publishPageDraft(spaceId: string, pageId: string, force = false): Promise<Page> {
    try {
        return await doFetch<Page>(`${draftPath(spaceId, pageId)}/publish`, {
            method: 'POST',
            body: {force},
        });
    } catch (error) {
        if (error instanceof RestError && error.status === 409 && isPublishConflict(error.body)) {
            throw new PublishConflictError(error.body);
        }
        throw error;
    }
}
