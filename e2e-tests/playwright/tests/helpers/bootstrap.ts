// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DocsServerContainer, adminPassword, adminUsername, defaultTeamName} from './mmcontainer';
import {assertPluginActive, assertServerSupportsDocs} from './preflight';
import {clearState, writeState} from './state';

// Deliberately not keyed off MM_SERVICESETTINGS_SITEURL: most dev shells export it, and
// that would silently seed data into a developer's live server.
const useExistingServer = process.env.MM_E2E_USE_EXISTING_SERVER === 'true';

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

        // The container path gets these checks during setup; without them here an
        // unsupported server fails much later, as an opaque browser or API failure.
        await assertServerSupportsDocs(baseURL);
        await assertPluginActive(baseURL, username, password);

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

    writeState({
        baseURL: server.url(),
        adminUsername,
        adminPassword,
        teamName: defaultTeamName,
    });

    return async () => {
        clearState();
        await server.stop();
    };
}
