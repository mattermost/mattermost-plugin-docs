// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineConfig, devices} from '@playwright/test';

import {spacePermissionsMode} from './tests/helpers/mode';

// Opt-in: videos are much heavier than traces. Takes Playwright's own mode names.
type VideoMode = 'on' | 'off' | 'retain-on-failure' | 'on-first-retry';
const video = (process.env.PW_VIDEO as VideoMode) || 'off';
const spacePermissionSpecPattern = '**/*space_permissions.spec.ts';

// No `use.baseURL`: the port is mapped at runtime and this module is evaluated before
// globalSetup. tests/fixtures.ts supplies it instead.
export default defineConfig({
    testDir: './tests',
    // The two modes are separate suites, not additive. In particular, the licensed permission
    // run must not collect the preset-only authoring scenario a second time: that scenario is
    // intentionally unlicensed and can only skip on this server.
    testMatch: spacePermissionsMode ? spacePermissionSpecPattern : '**/*.spec.ts',

    // Permission specs are excluded from the ordinary unlicensed authoring run.
    testIgnore: spacePermissionsMode ? [] : [spacePermissionSpecPattern],
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
    // The permission mode includes System Console cases that deliberately mutate the shared
    // system scheme and restore it in finally blocks. Keep that mode serial locally too: a local
    // multi-worker run must not expose unrelated personas to a temporary read/manage/delete role.
    workers: spacePermissionsMode || process.env.CI ? 1 : undefined,
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
