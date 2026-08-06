// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test} from '@mattermost/playwright-lib';

const PLUGIN_ID = 'com.mattermost.docs';

/**
 * @objective Verify the Docs product loads with the plugin deployed and that a space's overflow
 * menu opens the permissions modal.
 *
 * This is the one hop no other suite covers. server/e2e asserts every endpoint the modal calls
 * against a real server, and the Jest suite asserts the modal's rendering against mocked reads,
 * but neither exercises the plugin's UI running inside a real webapp — the modal is unreachable
 * from a jsdom tree that never mounts the product route, and invisible to an HTTP-only suite.
 *
 * @precondition
 * A running server, serving the webapp, with EnableDocs on and the plugin deployed. All three are
 * asserted rather than arranged: EnableDocs is read only at boot, and deploying a bundle from here
 * would make the suite own a server it is designed not to own. Each failure names its own remedy.
 */
test(
    'the space overflow menu opens the permissions modal',
    {tag: ['@docs', '@permissions']},
    async ({pw}) => {
        const {adminClient, user, team} = await pw.initSetup();

        const config = await adminClient.getConfig();
        expect(
            String(config.FeatureFlags?.EnableDocs),
            'EnableDocs must be on — restart the server with MM_FEATUREFLAGS_ENABLEDOCS=true',
        ).toBe('true');

        const plugins = await adminClient.getPlugins();
        expect(
            plugins.active.map((plugin) => plugin.id),
            `the ${PLUGIN_ID} plugin must be deployed and enabled on this server`,
        ).toContain(PLUGIN_ID);

        const {page} = await pw.testBrowser.login(user);
        await page.goto(`/${team.name}/spaces`);

        // Every space row carries the same menu; the first is enough to assert the product
        // mounted and rendered its sidebar.
        const spaceMenu = page.getByRole('button', {name: /^Space options for /}).first();
        await expect(spaceMenu).toBeVisible();

        await spaceMenu.click();
        await page.getByText('Space permissions').click();

        await expect(page.getByRole('heading', {name: /^Permissions for /})).toBeVisible();
    },
);
