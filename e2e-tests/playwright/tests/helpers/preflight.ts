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

/**
 * Proves that the running Mattermost binary implements the scheme-read RPC used by the plugin
 * binary. This is intentionally a runtime probe: a local go.mod replace can compile the plugin
 * against a sibling core checkout while Testcontainers still boots an older published image.
 * Both halves then look individually valid, but the first member mutation fails with an opaque
 * 500 because the server cannot dispatch GetSchemeForChannel.
 */
export async function assertSpaceSchemeReadSupported(baseURL: string, username: string, password: string, remedy = '') {
    const token = await adminToken(baseURL, username, password);
    const suffix = Date.now().toString(36);

    const teamResponse = await fetch(`${baseURL}/api/v4/teams`, {
        method: 'POST',
        headers: authed(token),
        body: JSON.stringify({name: `docs-scheme-read-${suffix}`, display_name: `Docs scheme read ${suffix}`, type: 'O'}),
        signal: AbortSignal.timeout(requestTimeoutMs),
    });
    if (!teamResponse.ok) {
        throw new Error(`Scheme-read preflight could not create a probe team on ${baseURL} (${teamResponse.status}).`);
    }
    const team = await teamResponse.json() as {id: string};
    let spaceID = '';

    try {
        // Omit defaults so this resolves a seeded preset and remains valid on an unlicensed server.
        const createResponse = await fetch(`${baseURL}/plugins/${pluginId}/api/v1/teams/${team.id}/spaces`, {
            method: 'POST',
            headers: authed(token),
            body: JSON.stringify({title: `Scheme read ${suffix}`}),
            signal: AbortSignal.timeout(requestTimeoutMs),
        });
        if (!createResponse.ok) {
            const body = await createResponse.text();
            throw new Error(
                `Scheme-read preflight could not create a preset-backed space (${createResponse.status}): ` +
                `${body.slice(0, 400)}\nThe running Mattermost image may predate the plugin's channel-scheme API. ` +
                remedy,
            );
        }
        const space = await createResponse.json() as {id: string};
        spaceID = space.id;

        const readResponse = await fetch(`${baseURL}/plugins/${pluginId}/api/v1/spaces/${spaceID}`, {
            headers: authed(token),
            signal: AbortSignal.timeout(requestTimeoutMs),
        });
        if (!readResponse.ok) {
            const body = await readResponse.text();
            throw new Error(
                `The plugin and Mattermost server disagree on the channel-scheme API (${readResponse.status}). ` +
                'The plugin requires GetSchemeForChannel, but the running server image does not answer it.\n' +
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

// A permission set matching none of the three seeded presets, so resolving it can only be answered
// by the plugin-created scheme pool — which is the capability being probed.
const NON_PRESET_DEFAULTS = ['comment_page', 'edit_page'];

/**
 * Fails setup when the server cannot create a channel scheme for an arbitrary space-default
 * permission set.
 *
 * Every space default outside the three seeded presets resolves through the plugin scheme API
 * (`GetOrCreatePluginChannelScheme`). A server whose plugin API predates it does not report an
 * error: the generated plugin RPC client logs a transport failure and hands back zero values, which
 * the plugin can only surface as a 500. Without this check that lands as a *test* failure — a
 * permission checkbox that silently reverts — and reads as a product bug rather than a server that
 * cannot serve the feature.
 *
 * Probed end-to-end rather than by version number, because the capability has shipped in
 * uncommitted core work before appearing in any released or pinned build: the only honest question
 * is whether *this* server can do it. The probe seeds one throwaway team and space, as every spec
 * does, and removes the space afterwards.
 */
export async function assertSpacePermissionsSupported(baseURL: string, username: string, password: string, remedy = '') {
    const token = await adminToken(baseURL, username, password);
    const suffix = Date.now().toString(36);

    const teamResponse = await fetch(`${baseURL}/api/v4/teams`, {
        method: 'POST',
        headers: authed(token),
        body: JSON.stringify({name: `docs-preflight-${suffix}`, display_name: `Docs preflight ${suffix}`, type: 'O'}),
        signal: AbortSignal.timeout(requestTimeoutMs),
    });
    if (!teamResponse.ok) {
        throw new Error(`Preflight could not create a probe team on ${baseURL} (${teamResponse.status}).`);
    }
    const team = await teamResponse.json() as {id: string};

    try {
        const spaceResponse = await fetch(`${baseURL}/plugins/${pluginId}/api/v1/teams/${team.id}/spaces`, {
            method: 'POST',
            headers: authed(token),
            body: JSON.stringify({title: `Preflight ${suffix}`, default_permissions: NON_PRESET_DEFAULTS}),
            signal: AbortSignal.timeout(requestTimeoutMs),
        });

        if (!spaceResponse.ok) {
            const body = await spaceResponse.text();
            throw new Error(
                `This server cannot resolve an arbitrary space-default permission set (${spaceResponse.status}), so every ` +
                'space-permission spec that changes a default would fail as though the product were broken.\n' +
                'The plugin scheme API (GetOrCreatePluginChannelScheme) is missing from this server build.\n' +
                `Server said: ${body.slice(0, 400)}\n` +
                `${remedy}`.trimEnd(),
            );
        }

        // Best-effort: the probe space is disposable, and a server that answered the create is
        // already proven. Failing here would turn a successful probe into a setup failure.
        const space = await spaceResponse.json() as {id: string};
        await fetch(`${baseURL}/plugins/${pluginId}/api/v1/spaces/${space.id}`, {
            method: 'DELETE',
            headers: authed(token),
            signal: AbortSignal.timeout(requestTimeoutMs),
        }).catch(() => undefined);
    } finally {
        // Runs against long-lived existing servers too, so every probe must remove its team even
        // when space creation fails. Cleanup remains best-effort to preserve the diagnostic above.
        await fetch(`${baseURL}/api/v4/teams/${team.id}?permanent=true`, {
            method: 'DELETE',
            headers: authed(token),
            signal: AbortSignal.timeout(requestTimeoutMs),
        }).catch(() => undefined);
    }
}

// The team-role permissions the suite itself changes in the System Console. A killed or timed-out
// test can leave any of them inverted for the rest of the run (and for later runs against an
// existing server), so setup restores both halves of the default: read/create present,
// manage/delete absent. Permissions outside this set are untouched.
const TEAM_USER_SPACE_PERMISSION_BASELINE: Record<string, boolean> = {
    read_space: true,
    create_space: true,
    manage_space: false,
    delete_space: false,
};

type TeamUserRole = {id: string; permissions: string[]};

const matchesTeamUserSpacePermissionBaseline = (role: TeamUserRole) => Object.entries(TEAM_USER_SPACE_PERMISSION_BASELINE).
    every(([permission, expected]) => role.permissions.includes(permission) === expected);

async function readTeamUserRole(baseURL: string, token: string): Promise<TeamUserRole> {
    const response = await fetch(`${baseURL}/api/v4/roles/name/team_user`, {
        headers: authed(token),
        signal: AbortSignal.timeout(requestTimeoutMs),
    });
    if (!response.ok) {
        throw new Error(`Unable to read the team_user role on ${baseURL} (${response.status}).`);
    }

    return await response.json() as TeamUserRole;
}

/**
 * Restores the baseline team-role permissions the suite depends on, repairing what a previous
 * aborted run left behind.
 *
 * Without this the damage surfaces as the *first assertion* of an unrelated spec — "an ordinary
 * member starts able to create a space" fails — which reads as a product regression and sends the
 * reader to the wrong code. The state is server-wide and outlives the run that broke it, so every
 * later run inherits it until someone repairs the role by hand.
 *
 * Repairing rather than only reporting is deliberate: this is a mutation the suite makes on purpose
 * and undoes on purpose, so restoring it is finishing that job, not overriding an operator's
 * choice. Permissions outside these four are untouched, and the repair says loudly what changed.
 */
export async function restoreBaselineTeamPermissions(baseURL: string, username: string, password: string) {
    const token = await adminToken(baseURL, username, password);
    const role = await readTeamUserRole(baseURL, token);
    if (matchesTeamUserSpacePermissionBaseline(role)) {
        return;
    }

    const added = Object.entries(TEAM_USER_SPACE_PERMISSION_BASELINE).
        filter(([permission, expected]) => expected && !role.permissions.includes(permission)).
        map(([permission]) => permission);
    const removed = Object.entries(TEAM_USER_SPACE_PERMISSION_BASELINE).
        filter(([permission, expected]) => !expected && role.permissions.includes(permission)).
        map(([permission]) => permission);
    const managed = new Set(Object.keys(TEAM_USER_SPACE_PERMISSION_BASELINE));
    const permissions = role.permissions.filter((permission) => !managed.has(permission));
    permissions.push(...Object.entries(TEAM_USER_SPACE_PERMISSION_BASELINE).
        filter(([, expected]) => expected).
        map(([permission]) => permission));

    console.log(
        `[e2e] repairing the team_user space-permission baseline left by an interrupted System Console test ` +
        `(add: ${added.join(', ') || 'none'}; remove: ${removed.join(', ') || 'none'}).`,
    );

    const patch = await fetch(`${baseURL}/api/v4/roles/${role.id}/patch`, {
        method: 'PUT',
        headers: authed(token),
        body: JSON.stringify({permissions: permissions.sort()}),
        signal: AbortSignal.timeout(requestTimeoutMs),
    });
    if (!patch.ok) {
        throw new Error(
            `Unable to restore the team_user space-permission baseline on ${baseURL} (${patch.status}). ` +
            'Repair it in System Console → User Management → Permissions before re-running.',
        );
    }

    // Read back from the server instead of trusting only the PATCH response. This is also the
    // readiness barrier before a new browser session resolves its team roles.
    const verified = await readTeamUserRole(baseURL, token);
    if (!matchesTeamUserSpacePermissionBaseline(verified)) {
        throw new Error(`The team_user space-permission baseline on ${baseURL} did not read back after repair.`);
    }
}
