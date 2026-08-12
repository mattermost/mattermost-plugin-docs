// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test as base, expect} from '@playwright/test';

import {readState, type E2EState} from './helpers/state';

// Specs import `test` from here rather than from @playwright/test: baseURL is only
// known once globalSetup has mapped the container's port, which is after the config
// module has already been evaluated. Fixtures resolve per-test, so they can read it.
export const test = base.extend<{server: E2EState}>({
    server: async ({}, use) => { // eslint-disable-line no-empty-pattern
        await use(readState());
    },

    baseURL: async ({server}, use) => {
        await use(server.baseURL);
    },

    page: async ({page}, use) => {
        // Mattermost otherwise answers the first navigation with the "open in the
        // Desktop App?" interstitial, and the product never loads. The container sets
        // MM_SERVICESETTINGS_ENABLEDESKTOPLANDINGPAGE=false, but a server the suite is
        // merely pointed at (MM_E2E_USE_EXISTING_SERVER) is not ours to reconfigure,
        // so the preference is set per browser instead.
        await page.addInitScript(() => {
            window.localStorage.setItem('__landingPageSeen__', 'true');
        });

        await use(page);
    },
});

export {expect};
