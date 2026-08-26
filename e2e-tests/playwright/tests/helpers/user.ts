// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {randomBytes} from 'node:crypto';

import type {Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith, slugify, uniqueSuffix} from './client';

export interface SeededUser {
    id: string;
    username: string;
    password: string;
}

// Per user, not a shared constant: the suite can run against a real server, where a
// published password would let anyone who learns a seeded username sign in as them.
function generatePassword(): string {
    return `Aa1!${randomBytes(18).toString('base64url')}`;
}

export async function createUser(page: Page, userPrefix: string): Promise<SeededUser> {
    const suffix = uniqueSuffix().replace(/-/g, '');
    const normalizedPrefix = slugify(userPrefix, 'playwright-user');
    const truncatedPrefix = normalizedPrefix.slice(0, Math.max(1, 40 - suffix.length - 1));
    const username = `${truncatedPrefix}-${suffix}`;

    const password = generatePassword();

    const response = await page.request.post('/api/v4/users', {
        ...requestedWith,
        data: {
            email: `${username}@sample.mattermost.com`,
            username,
            password,
            first_name: username,
            last_name: '',
            nickname: '',
        },
    });

    const user = await readJsonOrThrow<SeededUser>(response, 'Unable to create user');
    return {...user, password};
}
