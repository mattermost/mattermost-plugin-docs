// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {readFileSync} from 'node:fs';
import {join, resolve} from 'node:path';

// Playwright transpiles to CommonJS, so import.meta is unavailable.
const repoRoot = resolve(__dirname, '../../../..');

export const pluginId = 'com.mattermost.docs';

const requestTimeoutMs = 30_000;

function requiredServerVersion(): string {
    const manifest = JSON.parse(readFileSync(join(repoRoot, 'plugin.json'), 'utf8')) as {min_server_version: string};
    return manifest.min_server_version;
}

function isVersionAtLeast(actual: string, required: string): boolean {
    const toParts = (v: string) => v.split('.').map((n) => parseInt(n, 10) || 0);
    const [a, r] = [toParts(actual), toParts(required)];

    for (let i = 0; i < Math.max(a.length, r.length); i++) {
        const diff = (a[i] ?? 0) - (r[i] ?? 0);
        if (diff !== 0) {
            return diff > 0;
        }
    }

    return true;
}

// Fails setup rather than letting an unsupported server surface later as an unexplained
// 501 in a browser test.
export async function assertServerSupportsDocs(baseURL: string, remedy = '') {
    // Client config, not `mmctl version`: that reports the mmctl build, not the server.
    const response = await fetch(
        `${baseURL}/api/v4/config/client?format=old`,
        {signal: AbortSignal.timeout(requestTimeoutMs)},
    );
    const config = await response.json() as Record<string, string>;

    const required = requiredServerVersion();
    const version = config.Version;

    if (!version || !isVersionAtLeast(version, required)) {
        throw new Error(
            `Mattermost server at ${baseURL} reports version ${version ?? 'unknown'}, but the plugin requires >= ${required}. ${remedy}`.trim(),
        );
    }

    if (config.FeatureFlagEnableDocs !== 'true') {
        throw new Error(
            `Mattermost server at ${baseURL} does not have the EnableDocs feature flag on, so it lacks Docs core support. ${remedy}`.trim(),
        );
    }
}

// The webapp listing, not the admin plugin listing: it reports what the browser tests
// actually need, and any authenticated user can read it.
export async function assertPluginActive(baseURL: string, username: string, password: string) {
    const login = await fetch(`${baseURL}/api/v4/users/login`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({login_id: username, password}),
        signal: AbortSignal.timeout(requestTimeoutMs),
    });

    const token = login.headers.get('token');
    if (!login.ok || !token) {
        throw new Error(
            `Unable to sign in to ${baseURL} as "${username}" (${login.status}). Set MM_ADMIN_USERNAME and MM_ADMIN_PASSWORD for that server.`,
        );
    }

    const response = await fetch(`${baseURL}/api/v4/plugins/webapp`, {
        headers: {Authorization: `Bearer ${token}`},
        signal: AbortSignal.timeout(requestTimeoutMs),
    });

    if (!response.ok) {
        throw new Error(`Unable to list webapp plugins on ${baseURL} (${response.status}).`);
    }

    const manifests = await response.json() as Array<{id: string}>;

    if (!manifests.some((manifest) => manifest.id === pluginId)) {
        throw new Error(`Plugin ${pluginId} is not active on ${baseURL}. Install and enable it before running the suite.`);
    }
}
