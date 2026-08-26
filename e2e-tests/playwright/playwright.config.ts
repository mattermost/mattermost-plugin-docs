// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineConfig, devices} from '@playwright/test';

// Opt-in: videos are much heavier than traces. Takes Playwright's own mode names.
type VideoMode = 'on' | 'off' | 'retain-on-failure' | 'on-first-retry';
const video = (process.env.PW_VIDEO as VideoMode) || 'off';

// No `use.baseURL`: the port is mapped at runtime and this module is evaluated before
// globalSetup. tests/fixtures.ts supplies it instead.
export default defineConfig({
    testDir: './tests',
    testMatch: '**/*.spec.ts',
    globalSetup: './tests/helpers/bootstrap.ts',
    forbidOnly: Boolean(process.env.CI),
    fullyParallel: false,
    timeout: 60_000,

    // Playwright applies no timeout to globalSetup.
    globalTimeout: 20 * 60_000,
    outputDir: 'test-results',
    reporter: [
        ['list'],
        ['junit', {outputFile: 'results/junit/test-results.xml'}],
    ],
    retries: process.env.CI ? 2 : 0,
    workers: process.env.CI ? 1 : undefined,
    use: {
        screenshot: 'only-on-failure',
        trace: 'retain-on-failure',
        video,
    },
    projects: [
        {
            name: 'chromium',
            use: {
                ...devices['Desktop Chrome'],
            },
        },
    ],
});
