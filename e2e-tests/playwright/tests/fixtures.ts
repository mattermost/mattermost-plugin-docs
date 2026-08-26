// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test as base, expect} from '@playwright/test';

import {readState, type E2EState} from './helpers/state';

// Specs import `test` from here, not @playwright/test: baseURL is only known after
// globalSetup maps the container port, and fixtures resolve late enough to read it.
export const test = base.extend<{server: E2EState}>({
    server: async ({}, use) => { // eslint-disable-line no-empty-pattern
        await use(readState());
    },

    baseURL: async ({server}, use) => {
        await use(server.baseURL);
    },

    page: async ({page}, use) => {
        // Skips the desktop-app interstitial, which otherwise swallows the first
        // navigation. Set per browser because a server we are only pointed at
        // (MM_E2E_USE_EXISTING_SERVER) is not ours to reconfigure.
        await page.addInitScript(() => {
            window.localStorage.setItem('__landingPageSeen__', 'true');
        });

        await use(page);
    },
});

export {expect};
