// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

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
    space_id: string;
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
