// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DocsServerContainer, adminPassword, adminUsername, defaultTeamName} from './mmcontainer';
import {clearState, writeState} from './state';

// Reusing an already-running server is opt-in through this variable alone. It
// deliberately does NOT key off MM_SERVICESETTINGS_SITEURL: that is exported by
// most Mattermost dev shells, and keying off it would silently seed teams and
// users into a developer's live server instead of a throwaway container.
const useExistingServer = process.env.MM_E2E_USE_EXISTING_SERVER === 'true';

// Returning the teardown as a closure keeps the container handle in scope. A
// separate globalTeardown file would only see it if both modules happened to load
// in the same process, and would silently leak containers when they didn't.
export default async function globalSetup(): Promise<() => Promise<void>> {
    // A killed run leaves this behind, and a stale file pointing at a server that is
    // no longer there is a confusing way to fail.
    clearState();

    if (useExistingServer) {
        const baseURL = process.env.MM_SERVICESETTINGS_SITEURL || 'http://localhost:8065';

        console.log(`[e2e] MM_E2E_USE_EXISTING_SERVER=true — running against ${baseURL}. This seeds real data into that server.`);

        writeState({
            baseURL,
            adminUsername: process.env.MM_ADMIN_USERNAME || adminUsername,
            adminPassword: process.env.MM_ADMIN_PASSWORD || adminPassword,
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
