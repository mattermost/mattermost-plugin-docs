// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test as base, expect, type Browser, type BrowserContext, type BrowserContextOptions} from '@playwright/test';

import {readState, type E2EState} from './helpers/state';

// Skips the desktop-app interstitial, which otherwise swallows the first navigation.
const skipLandingPage = (context: BrowserContext) => context.addInitScript(() => {
    window.localStorage.setItem('__landingPageSeen__', 'true');
});

// Builds a context the way the `page` fixture below does. Specs that need a second identity build
// their own context, and one built with browser.newContext directly carries no init script — so a
// spec that then navigates hits the interstitial the fixture exists to skip.
export async function newContext(browser: Browser, options?: BrowserContextOptions): Promise<BrowserContext> {
    const context = await browser.newContext(options);
    await skipLandingPage(context);
    return context;
}

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
        // Set per browser because a server we are only pointed at
        // (MM_E2E_USE_EXISTING_SERVER) is not ours to reconfigure.
        await skipLandingPage(page.context());

        await use(page);
    },
});

export {expect};
