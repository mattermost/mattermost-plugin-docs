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

    if (!response.ok) {
        throw new Error(`Unable to read the client config at ${baseURL} (${response.status}).`);
    }

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
    const token = await adminToken(baseURL, username, password);

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

// Signs in and returns a session token, or throws naming the server and the account.
export async function adminToken(baseURL: string, username: string, password: string): Promise<string> {
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

    return token;
}

const authed = (token: string) => ({
    'Content-Type': 'application/json',
    'X-Requested-With': 'XMLHttpRequest',
    Authorization: `Bearer ${token}`,
});

/**
 * Proves that the running Mattermost binary implements the channel-resolving post RPCs every page
 * comment depends on: a comment is a core Posts row on the space's backing channel, and creating
 * one requires the server to resolve that channel for a plugin post write — which stock images
 * refuse as an unknown channel. This is intentionally a runtime probe: a local go.mod replace can
 * compile the plugin against a sibling core checkout while Testcontainers still boots an older
 * published image. Both halves then look individually valid, but the first comment create fails
 * with an opaque 500 in a spec that reads like a product bug.
 *
 * The probe stops at the smallest request whose outcome differs — one comment create. The image
 * that answers it is built from the paired core branch, which also carries the move re-home and
 * retention RPCs; those have no API-observable probe of their own (a missing re-home RPC is
 * logged, not surfaced).
 */
export async function assertCommentRPCsSupported(baseURL: string, username: string, password: string, remedy = '') {
    const token = await adminToken(baseURL, username, password);
    const suffix = Date.now().toString(36);

    const teamResponse = await fetch(`${baseURL}/api/v4/teams`, {
        method: 'POST',
        headers: authed(token),
        body: JSON.stringify({name: `docs-comment-probe-${suffix}`, display_name: `Docs comment probe ${suffix}`, type: 'O'}),
        signal: AbortSignal.timeout(requestTimeoutMs),
    });
    if (!teamResponse.ok) {
        throw new Error(`Comment-RPC preflight could not create a probe team on ${baseURL} (${teamResponse.status}).`);
    }
    const team = await teamResponse.json() as {id: string};
    let spaceID = '';

    try {
        const spaceResponse = await fetch(`${baseURL}/plugins/${pluginId}/api/v1/teams/${team.id}/spaces`, {
            method: 'POST',
            headers: authed(token),
            body: JSON.stringify({title: `Comment probe ${suffix}`}),
            signal: AbortSignal.timeout(requestTimeoutMs),
        });
        if (!spaceResponse.ok) {
            throw new Error(`Comment-RPC preflight could not create a probe space on ${baseURL} (${spaceResponse.status}).`);
        }
        const space = await spaceResponse.json() as {id: string};
        spaceID = space.id;

        const pageResponse = await fetch(`${baseURL}/plugins/${pluginId}/api/v1/spaces/${spaceID}/pages`, {
            method: 'POST',
            headers: authed(token),
            body: JSON.stringify({title: `Comment probe ${suffix}`, body: ''}),
            signal: AbortSignal.timeout(requestTimeoutMs),
        });
        if (!pageResponse.ok) {
            throw new Error(`Comment-RPC preflight could not create a probe page on ${baseURL} (${pageResponse.status}).`);
        }
        const probePage = await pageResponse.json() as {id: string};

        const commentResponse = await fetch(
            `${baseURL}/plugins/${pluginId}/api/v1/spaces/${spaceID}/pages/${probePage.id}/comments`,
            {
                method: 'POST',
                headers: authed(token),
                body: JSON.stringify({message: 'preflight probe'}),
                signal: AbortSignal.timeout(requestTimeoutMs),
            },
        );
        if (!commentResponse.ok) {
            const body = await commentResponse.text();
            throw new Error(
                `The plugin and Mattermost server disagree on the post API for backing channels (${commentResponse.status}). ` +
                'The plugin writes page comments as posts on the space\'s backing channel, but the running server ' +
                'image cannot resolve that channel for a plugin post write, so every comment operation fails.\n' +
                `Server said: ${body.slice(0, 400)}\n${remedy}`.trimEnd(),
            );
        }
    } finally {
        if (spaceID) {
            await fetch(`${baseURL}/plugins/${pluginId}/api/v1/spaces/${spaceID}`, {
                method: 'DELETE',
                headers: authed(token),
                signal: AbortSignal.timeout(requestTimeoutMs),
            }).catch(() => undefined);
        }
        await fetch(`${baseURL}/api/v4/teams/${team.id}?permanent=true`, {
            method: 'DELETE',
            headers: authed(token),
            signal: AbortSignal.timeout(requestTimeoutMs),
        }).catch(() => undefined);
    }
}
