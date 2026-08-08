// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineConfig} from '@playwright/test';

import {duration, testConfig} from '@mattermost/playwright-lib';

// External mode only: the suite drives a server that is already running and
// serving the webapp with the plugin deployed, addressed by PW_BASE_URL (the lib
// defaults it to http://localhost:8065). Nothing here starts a server, so the
// suite carries no Docker or image dependency — unlike server/e2e, which owns
// its container.
export default defineConfig({
    globalSetup: './global_setup.ts',
    forbidOnly: testConfig.isCI,
    outputDir: './results/output',
    retries: testConfig.isCI ? 2 : 0,
    testDir: 'specs',
    timeout: duration.two_min,
    workers: testConfig.workers,
    expect: {
        timeout: duration.ten_sec,
    },
    use: {
        baseURL: testConfig.baseURL,
        ignoreHTTPSErrors: true,
        headless: testConfig.headless,
        locale: 'en-US',
        screenshot: 'only-on-failure',
        timezoneId: new Intl.DateTimeFormat().resolvedOptions().timeZone,
        trace: 'retain-on-failure',
        video: 'retain-on-failure',
        actionTimeout: duration.half_min,
    },
    projects: [
        {
            name: 'chrome',
            use: {
                browserName: 'chromium',
                viewport: {width: 1280, height: 1024},
            },
        },
    ],
    reporter: [
        ['html', {open: 'never', outputFolder: './results/reporter'}],
        ['list'],
    ],
});
