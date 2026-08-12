// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

import {requestedWith} from './client';

// Logs in via the API on the page's own browser context, so subsequent navigations
// are authenticated without driving the Mattermost login UI. This must be called on
// the page under test — a request context created elsewhere shares no cookie jar
// with it, and navigation would land on the login screen.
export async function loginAs(page: Page, loginId: string, password: string) {
    const response = await page.request.post('/api/v4/users/login', {
        ...requestedWith,
        data: {login_id: loginId, password},
    });

    if (!response.ok()) {
        throw new Error(`Unable to login as ${loginId}: ${response.status()} ${await response.text()}`);
    }
}
