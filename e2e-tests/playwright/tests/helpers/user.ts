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

// The onboarding task list mounts a portal overlay that intercepts pointer events across the
// whole product, so a freshly created user cannot be driven through the UI at all until it is
// dismissed — every click times out on an element that is visible, enabled and stable.
//
// The container sets MM_SERVICESETTINGS_ENABLEONBOARDINGFLOW=false, which is why this never
// surfaced there. A run against an existing server must not reconfigure it (see the page fixture),
// so the flow is suppressed per user instead, which touches nothing but that user's own
// preferences. Harmless where the server already has it off.
//
// Exported because the admin is not created here — the suite is handed those credentials — and an
// admin driving the browser hits the same overlay.
export async function suppressOnboarding(page: Page, userId: string): Promise<void> {
    const response = await page.request.put(`/api/v4/users/${userId}/preferences`, {
        ...requestedWith,
        data: [
            {user_id: userId, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
            {user_id: userId, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
        ],
    });

    if (!response.ok()) {
        throw new Error(`Unable to suppress onboarding for ${userId}: ${response.status()} ${await response.text()}`);
    }
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
    await suppressOnboarding(page, user.id);

    return {...user, password};
}
