// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DocsServerContainer, adminPassword, adminUsername, defaultTeamName} from './mmcontainer';
import {spacePermissionsMode} from './mode';
import {assertPluginActive, assertServerSupportsDocs, assertSpacePermissionsSupported, assertSpaceSchemeReadSupported, restoreBaselineTeamPermissions} from './preflight';
import {clearState, writeState} from './state';

// Deliberately not keyed off MM_SERVICESETTINGS_SITEURL: most dev shells export it, and
// that would silently seed data into a developer's live server.
const useExistingServer = process.env.MM_E2E_USE_EXISTING_SERVER === 'true';

// The checks that need a running server and an admin, run identically on both paths: a setup
// problem must fail here, naming itself, rather than surfacing later as a spec failure that reads
// like a product bug. Every one of these was added after a real run misattributed its cause.
async function assertReadyForSpecs(baseURL: string, username: string, password: string, remedy = '') {
    await assertPluginActive(baseURL, username, password);
    await assertSpaceSchemeReadSupported(baseURL, username, password, remedy);

    if (!spacePermissionsMode) {
        return;
    }
    // Both are specific to the space-permission specs: the authoring run neither changes a space
    // default nor touches team roles, so it should not pay for either.
    await restoreBaselineTeamPermissions(baseURL, username, password);
    await assertSpacePermissionsSupported(baseURL, username, password, remedy);
}

// Teardown is returned as a closure to keep the container handle in scope; a separate
// globalTeardown file would leak containers whenever it loaded in another process.
export default async function globalSetup(): Promise<() => Promise<void>> {
    // A killed run leaves a stale file pointing at a server that is gone.
    clearState();

    if (useExistingServer) {
        const baseURL = process.env.MM_SERVICESETTINGS_SITEURL || 'http://localhost:8065';

        console.log(`[e2e] MM_E2E_USE_EXISTING_SERVER=true — running against ${baseURL}. This seeds real data into that server.`);

        const username = process.env.MM_ADMIN_USERNAME || adminUsername;
        const password = process.env.MM_ADMIN_PASSWORD || adminPassword;

        // Without these an unsupported server fails much later, as an opaque browser or API failure.
        await assertServerSupportsDocs(baseURL);
        await assertReadyForSpecs(baseURL, username, password,
            'This server is built from the paired core branch — rebuild and restart it so it carries the current core work.');

        writeState({
            baseURL,
            adminUsername: username,
            adminPassword: password,
            teamName: process.env.MM_TEAM_NAME || defaultTeamName,
        });

        return async () => clearState();
    }

    console.log('[e2e] Starting a throwaway Mattermost container (set MM_E2E_USE_EXISTING_SERVER=true to target a running server instead).');

    const server = await new DocsServerContainer().start();

    try {
        await assertReadyForSpecs(server.url(), adminUsername, adminPassword,
            'Set MM_IMAGE locally, add an e2e-core-commit marker to the PR description in cloud CI, or run ' +
            'against a compatible existing server with MM_E2E_USE_EXISTING_SERVER=true.');

        writeState({
            baseURL: server.url(),
            adminUsername,
            adminPassword,
            teamName: defaultTeamName,
        });
    } catch (error) {
        await server.stop();
        throw error;
    }

    return async () => {
        clearState();
        await server.stop();
    };
}
