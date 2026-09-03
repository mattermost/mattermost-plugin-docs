// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {APIResponse, Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith} from './client';

// Seeding seam for specs whose subject is not authoring itself; the authoring spec
// drives that journey through the UI instead.
const apiRoot = '/plugins/com.mattermost.docs/api/v1';

export interface Space {
    id: string;
    title: string;
    team_id: string;
}

export interface DocsPage {
    id: string;
    title: string;
    space_id: string;
}

export interface SpaceMember {
    user_id: string;
}

export interface PageDraft {
    page_id: string;
    title: string;
    body: string;
}

// Null, not an error: a published page with no unpublished edits has no draft.
export async function getPageDraft(page: Page, spaceId: string, pageId: string): Promise<PageDraft | null> {
    const response = await page.request.get(`${apiRoot}/spaces/${spaceId}/pages/${pageId}/draft`, requestedWith);

    if (response.status() === 404) {
        return null;
    }

    return readJsonOrThrow<PageDraft>(response, `Unable to fetch draft for page ${pageId}`);
}

export async function createSpace(page: Page, teamId: string, title: string): Promise<Space> {
    const response = await page.request.post(`${apiRoot}/teams/${teamId}/spaces`, {
        ...requestedWith,
        data: {title},
    });

    return readJsonOrThrow<Space>(response, `Unable to create space "${title}"`);
}

export async function createPage(page: Page, spaceId: string, title: string, body = ''): Promise<DocsPage> {
    const response = await page.request.post(`${apiRoot}/spaces/${spaceId}/pages`, {
        ...requestedWith,
        data: {title, body},
    });

    return readJsonOrThrow<DocsPage>(response, `Unable to create page "${title}"`);
}

// force=true overrides first-write-wins.
export async function publishDraft(page: Page, spaceId: string, pageId: string, force = false): Promise<DocsPage> {
    const response = await page.request.post(`${apiRoot}/spaces/${spaceId}/pages/${pageId}/draft/publish`, {
        ...requestedWith,
        data: {force},
    });

    return readJsonOrThrow<DocsPage>(response, `Unable to publish draft for page ${pageId}`);
}

export async function addSpaceMember(page: Page, spaceId: string, userId: string): Promise<SpaceMember> {
    const response = await page.request.post(`${apiRoot}/spaces/${spaceId}/members`, {
        ...requestedWith,
        data: {user_id: userId},
    });

    return readJsonOrThrow<SpaceMember>(response, `Unable to add user ${userId} to space ${spaceId}`);
}

// force=true skips the optimistic-lock baseline (expected_update_at).
export async function movePageToSpace(page: Page, spaceId: string, pageId: string, targetSpaceId: string): Promise<DocsPage> {
    const response = await page.request.patch(`${apiRoot}/spaces/${spaceId}/pages/${pageId}/move-to-space`, {
        ...requestedWith,
        data: {target_space_id: targetSpaceId, force: true},
    });

    return readJsonOrThrow<DocsPage>(response, `Unable to move page ${pageId} to space ${targetSpaceId}`);
}

export interface PageComment {
    id: string;
    space_id: string;
    page_id: string;
    user_id: string;
    root_id: string;
    message: string;
    comment_type: string;
    anchor_id?: string;
    reply_count: number;
    resolved: boolean;
    resolved_by?: string;
    resolved_at?: number;
    resolved_reason?: string;
    create_at: number;
    update_at: number;
    edit_at?: number;
}

export interface PageCommentCounts {
    total: number;
    open: number;
    resolved: number;
}

// Roots are keyset-paged (next_after cursor); replies are offset-paged (page number).
export interface CommentRootsWindow {
    items: PageComment[];
    next_after?: string;
    per_page: number;
    has_more: boolean;
}

export interface CommentRepliesPage {
    items: PageComment[];
    page: number;
    per_page: number;
    has_more: boolean;
}

export interface CommentCreate {
    message: string;
    comment_type?: string;
    anchor_id?: string;
}

const commentsRoot = (spaceId: string, pageId: string) => `${apiRoot}/spaces/${spaceId}/pages/${pageId}/comments`;

// Raw response, not parsed JSON: the create-validation specs assert the 400s.
export async function createCommentResponse(page: Page, spaceId: string, pageId: string, create: CommentCreate): Promise<APIResponse> {
    return page.request.post(commentsRoot(spaceId, pageId), {...requestedWith, data: create});
}

export async function createComment(page: Page, spaceId: string, pageId: string, create: CommentCreate): Promise<PageComment> {
    const response = await createCommentResponse(page, spaceId, pageId, create);

    return readJsonOrThrow<PageComment>(response, `Unable to create a comment on page ${pageId}`);
}

export async function createCommentReply(page: Page, spaceId: string, pageId: string, rootId: string, message: string): Promise<PageComment> {
    const response = await page.request.post(`${commentsRoot(spaceId, pageId)}/${rootId}/replies`, {
        ...requestedWith,
        data: {message},
    });

    return readJsonOrThrow<PageComment>(response, `Unable to reply to comment ${rootId}`);
}

export interface CommentListFilters {
    resolved?: boolean;
    comment_type?: string;
    per_page?: number;
    after?: string;
}

export async function listComments(page: Page, spaceId: string, pageId: string, filters: CommentListFilters = {}): Promise<CommentRootsWindow> {
    const response = await listCommentsResponse(page, spaceId, pageId, filters);

    return readJsonOrThrow<CommentRootsWindow>(response, `Unable to list comments on page ${pageId}`);
}

// Raw response, not parsed JSON: the specs assert the 404 for a page outside the space
// and the 400 for a malformed cursor.
export async function listCommentsResponse(page: Page, spaceId: string, pageId: string, filters: CommentListFilters = {}): Promise<APIResponse> {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(filters)) {
        // An absent filter is not a filter: next_after is optional on the window, so a caller
        // paging to the end passes it through as undefined rather than omitting the key.
        if (value === undefined) {
            continue;
        }
        params.set(key, String(value));
    }

    const query = params.toString();

    return page.request.get(`${commentsRoot(spaceId, pageId)}${query ? `?${query}` : ''}`, requestedWith);
}

export async function listCommentReplies(page: Page, spaceId: string, pageId: string, rootId: string): Promise<CommentRepliesPage> {
    const response = await page.request.get(`${commentsRoot(spaceId, pageId)}/${rootId}/replies`, requestedWith);

    return readJsonOrThrow<CommentRepliesPage>(response, `Unable to list replies of comment ${rootId}`);
}

export async function getCommentCounts(page: Page, spaceId: string, pageId: string): Promise<PageCommentCounts> {
    const response = await page.request.get(`${apiRoot}/spaces/${spaceId}/pages/${pageId}/comments/counts`, requestedWith);

    return readJsonOrThrow<PageCommentCounts>(response, `Unable to read comment counts for page ${pageId}`);
}

// patchPageBody replaces a page's body, forcing past first-write-wins: these specs drive the body
// only to move comment anchors, never to test the optimistic lock.
export async function patchPageBody(page: Page, spaceId: string, pageId: string, body: string): Promise<DocsPage> {
    const response = await page.request.patch(`${apiRoot}/spaces/${spaceId}/pages/${pageId}`, {
        ...requestedWith,
        data: {body, force: true},
    });

    return readJsonOrThrow<DocsPage>(response, `Unable to patch body for page ${pageId}`);
}

// anchoredBody renders a TipTap document whose text carries one commentAnchor mark per id, the
// shape the editor produces for an inline comment.
export function anchoredBody(...anchorIds: string[]): string {
    const marks = anchorIds.map((anchorId) => ({
        type: 'text',
        text: 'anchored',
        marks: [{type: 'commentAnchor', attrs: {anchorId}}],
    }));

    return JSON.stringify({type: 'doc', content: [{type: 'paragraph', content: marks}]});
}

export async function getComment(page: Page, spaceId: string, pageId: string, commentId: string): Promise<PageComment> {
    const response = await getCommentResponse(page, spaceId, pageId, commentId);

    return readJsonOrThrow<PageComment>(response, `Unable to fetch comment ${commentId}`);
}

// Raw response, not parsed JSON: the not-found specs assert the 404 itself.
export async function getCommentResponse(page: Page, spaceId: string, pageId: string, commentId: string): Promise<APIResponse> {
    return page.request.get(`${commentsRoot(spaceId, pageId)}/${commentId}`, requestedWith);
}

// resolved flips the resolve state (roots only); message rewrites the text (author only).
export interface CommentPatch {
    resolved?: boolean;
    message?: string;
}

export async function patchComment(page: Page, spaceId: string, pageId: string, commentId: string, patch: CommentPatch): Promise<PageComment> {
    const response = await patchCommentResponse(page, spaceId, pageId, commentId, patch);

    return readJsonOrThrow<PageComment>(response, `Unable to patch comment ${commentId}`);
}

// Raw response, not parsed JSON: the guard specs assert the 400/403 refusals.
export async function patchCommentResponse(page: Page, spaceId: string, pageId: string, commentId: string, patch: CommentPatch): Promise<APIResponse> {
    return page.request.patch(`${commentsRoot(spaceId, pageId)}/${commentId}`, {
        ...requestedWith,
        data: patch,
    });
}

export async function resolveComment(page: Page, spaceId: string, pageId: string, commentId: string, resolved: boolean): Promise<PageComment> {
    return patchComment(page, spaceId, pageId, commentId, {resolved});
}

export async function resolveCommentResponse(page: Page, spaceId: string, pageId: string, commentId: string, resolved: boolean): Promise<APIResponse> {
    return patchCommentResponse(page, spaceId, pageId, commentId, {resolved});
}

// Raw response, not parsed JSON: deleting a root that still has live replies is refused
// with a 409 carrying reply_count, and the delete specs assert that body.
export async function deleteCommentResponse(page: Page, spaceId: string, pageId: string, commentId: string): Promise<APIResponse> {
    return page.request.delete(`${commentsRoot(spaceId, pageId)}/${commentId}`, requestedWith);
}
