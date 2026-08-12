// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineConfig, devices} from '@playwright/test';

// Recording is opt-in because videos are far heavier than traces, which already cover
// most debugging. Accepts Playwright's own mode names, so PW_VIDEO=retain-on-failure
// keeps only the interesting ones. A single spec can override this with
// test.use({video: 'on'}).
type VideoMode = 'on' | 'off' | 'retain-on-failure' | 'on-first-retry';
const video = (process.env.PW_VIDEO as VideoMode) || 'off';

// No `use.baseURL` here on purpose. Under testcontainers the server's port is
// mapped at runtime, and this module is evaluated before globalSetup runs, so a
// value resolved during setup could never reach it. Specs import `test` from
// tests/fixtures.ts, which overrides baseURL once the container is up.
export default defineConfig({
    testDir: './tests',
    testMatch: '**/*.spec.ts',
    globalSetup: './tests/helpers/bootstrap.ts',
    forbidOnly: Boolean(process.env.CI),
    fullyParallel: false,
    timeout: 60_000,

    // Playwright applies no timeout to globalSetup, so a stalled image pull or an
    // unresponsive container would otherwise hang until the CI job's own limit.
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
