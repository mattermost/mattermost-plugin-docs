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

// Playwright transpiles to CommonJS, so import.meta is unavailable.
const projectRoot = resolve(__dirname, '../..');
const stateFile = resolve(projectRoot, '.e2e-state.json');

// Playwright evaluates the config before globalSetup, so the mapped port cannot live
// there; it is written here and read by the baseURL fixture.
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
