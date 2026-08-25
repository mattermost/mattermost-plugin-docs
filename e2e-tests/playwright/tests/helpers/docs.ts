// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith} from './client';

// Seeding seam for specs whose subject is not authoring itself; the authoring spec
// drives that journey through the UI instead.
//
// Exported so a spec can probe a route whose refusal is the expected outcome: the helpers
// here throw on a non-OK response, which is wrong when the assertion is the 403 itself.
export const apiRoot = '/plugins/com.mattermost.docs/api/v1';

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

// Reads a space's whole roster. Paginated on the wire; one generous page is enough for a
// suite whose spaces hold a handful of members, and asking for more than the server's cap
// simply clamps.
export async function listSpaceMembers(page: Page, spaceId: string): Promise<SpaceMember[]> {
    const response = await page.request.get(`${apiRoot}/spaces/${spaceId}/members?per_page=200`, requestedWith);
    const body = await readJsonOrThrow<{items: SpaceMember[]}>(response, `Unable to list members of space ${spaceId}`);

    return body.items;
}

// Whether userId currently holds a membership in spaceId. The roster route admits any reader,
// so this answers for a caller who is not an administrator of the space.
export async function isSpaceMember(page: Page, spaceId: string, userId: string): Promise<boolean> {
    const members = await listSpaceMembers(page, spaceId);

    return members.some((member) => member.user_id === userId);
}
