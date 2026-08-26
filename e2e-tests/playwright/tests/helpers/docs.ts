// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith} from './client';

// Seeding seam for specs whose subject is not authoring itself. E2E outcomes are asserted in the
// browser; these helpers only arrange prerequisite spaces, pages, and memberships that another
// test is not trying to exercise.
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

export async function addSpaceMember(page: Page, spaceId: string, userId: string): Promise<SpaceMember> {
    const response = await page.request.post(`${apiRoot}/spaces/${spaceId}/members`, {
        ...requestedWith,
        data: {user_id: userId},
    });

    return readJsonOrThrow<SpaceMember>(response, `Unable to add user ${userId} to space ${spaceId}`);
}
