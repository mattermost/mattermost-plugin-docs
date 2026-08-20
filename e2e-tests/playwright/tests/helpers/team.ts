// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith, slugify, uniqueSuffix} from './client';

export interface Team {
    id: string;
    name: string;
}

export async function createTeam(page: Page, teamPrefix: string): Promise<Team> {
    const suffix = uniqueSuffix();
    const namespace = 'pw';
    const normalizedPrefix = slugify(teamPrefix, 'playwright');
    const truncatedPrefix = normalizedPrefix.slice(0, Math.max(1, 60 - namespace.length - suffix.length - 2));
    const name = `${namespace}-${truncatedPrefix}-${suffix}`;

    const response = await page.request.post('/api/v4/teams', {
        ...requestedWith,
        data: {
            name,
            display_name: `${teamPrefix} ${suffix}`,
            type: 'O',
        },
    });

    return readJsonOrThrow<Team>(response, 'Unable to create team');
}

export async function addUserToTeam(page: Page, teamId: string, userId: string) {
    const response = await page.request.post(`/api/v4/teams/${teamId}/members`, {
        ...requestedWith,
        data: {team_id: teamId, user_id: userId},
    });

    if (!response.ok()) {
        throw new Error(`Unable to add user ${userId} to team ${teamId}: ${response.status()} ${await response.text()}`);
    }
}
