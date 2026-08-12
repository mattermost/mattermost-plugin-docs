// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {existsSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {resolve} from 'node:path';

export interface E2EState {
    baseURL: string;
    adminUsername: string;
    adminPassword: string;
    teamName: string;
}

// Playwright transpiles these files to CommonJS, so __dirname is the portable
// choice here; import.meta is not available at runtime.
const projectRoot = resolve(__dirname, '../..');
const stateFile = resolve(projectRoot, '.e2e-state.json');

// The bridge between globalSetup and the tests. Playwright evaluates
// playwright.config.ts before globalSetup runs, so the container's mapped port
// cannot be baked into the config — it is written here and read by the fixture.
export function writeState(state: E2EState) {
    writeFileSync(stateFile, JSON.stringify(state, null, 2));
}

export function readState(): E2EState {
    if (!existsSync(stateFile)) {
        throw new Error(
            `Missing ${stateFile}. globalSetup did not complete — run the suite via "npm test" rather than invoking Playwright directly.`,
        );
    }

    return JSON.parse(readFileSync(stateFile, 'utf8')) as E2EState;
}

export function clearState() {
    rmSync(stateFile, {force: true});
}
