// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith, slugify, uniqueSuffix} from './client';

export interface Team {
    id: string;
    name: string;
}

interface Scheme {
    id: string;
    default_team_admin_role: string;
}

interface Role {
    id: string;
    permissions: string[];
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

export async function removeUserFromTeam(page: Page, teamId: string, userId: string) {
    const response = await page.request.delete(`/api/v4/teams/${teamId}/members/${userId}`, requestedWith);

    if (!response.ok()) {
        throw new Error(`Unable to remove user ${userId} from team ${teamId}: ${response.status()} ${await response.text()}`);
    }
}

export async function promoteToTeamAdmin(page: Page, teamId: string, userId: string) {
    const response = await page.request.put(`/api/v4/teams/${teamId}/members/${userId}/schemeRoles`, {
        ...requestedWith,
        data: {scheme_guest: false, scheme_user: true, scheme_admin: true},
    });

    if (!response.ok()) {
        throw new Error(`Unable to promote user ${userId} in team ${teamId}: ${response.status()} ${await response.text()}`);
    }
}

/**
 * Gives this test's team an isolated permission scheme whose team administrators carry exactly
 * the requested space-management tiers. Fixture setup may use the API; the specs assert every
 * consequence through the browser. A team-local scheme avoids changing the system role while
 * another Playwright file is running.
 */
export async function setTeamAdminSpaceTiers(
    page: Page,
    teamId: string,
    tiers: {manage: boolean; delete: boolean},
) {
    const suffix = uniqueSuffix().replace(/-/g, '');
    const schemeResponse = await page.request.post('/api/v4/schemes', {
        ...requestedWith,
        data: {
            name: `pw_docs_${suffix}`.slice(0, 64),
            display_name: `PW Docs ${suffix}`,
            description: 'Playwright-isolated Docs permission scheme',
            scope: 'team',
        },
    });
    const scheme = await readJsonOrThrow<Scheme>(schemeResponse, 'Unable to create an isolated team permission scheme');

    const assignResponse = await page.request.put(`/api/v4/teams/${teamId}/scheme`, {
        ...requestedWith,
        data: {scheme_id: scheme.id},
    });
    if (!assignResponse.ok()) {
        throw new Error(`Unable to assign scheme ${scheme.id} to team ${teamId}: ${assignResponse.status()} ${await assignResponse.text()}`);
    }

    const roleResponse = await page.request.get(`/api/v4/roles/name/${scheme.default_team_admin_role}`, requestedWith);
    const role = await readJsonOrThrow<Role>(roleResponse, 'Unable to read the isolated team-administrator role');
    const permissions = role.permissions.filter((permission) => !['manage_space', 'delete_space'].includes(permission));
    if (tiers.manage) {
        permissions.push('manage_space');
    }
    if (tiers.delete) {
        permissions.push('delete_space');
    }

    const patchResponse = await page.request.put(`/api/v4/roles/${role.id}/patch`, {
        ...requestedWith,
        data: {permissions: permissions.sort()},
    });
    if (!patchResponse.ok()) {
        throw new Error(`Unable to configure role ${role.id}: ${patchResponse.status()} ${await patchResponse.text()}`);
    }
}
