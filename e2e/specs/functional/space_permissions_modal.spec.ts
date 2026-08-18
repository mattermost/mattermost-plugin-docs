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
 * A running server, serving the webapp, with EnableDocs on and the plugin deployed. EnableDocs and
 * the bundle are asserted rather than arranged: EnableDocs is read only at boot, and deploying a
 * bundle from here would make the suite own a server it is designed not to own. Each failure names
 * its own remedy.
 *
 * The plugin's enabled state is the exception, and is arranged. initSetup replaces the whole config
 * with a fixed baseline whose PluginStates names four unrelated plugins, which disables every other
 * installed plugin — this one included. Asserting it is therefore impossible: the reset happens
 * after the precondition is met and before any assertion could read it.
 */
test(
    'the space overflow menu opens the permissions modal',
    {tag: ['@docs', '@permissions']},
    async ({pw}) => {
        // # Capture the plugin directories the server booted with — initSetup's config baseline
        // resets them to the defaults, while the running plugin environment keeps its boot-time
        // paths. On a server with custom directories that mismatch makes every later plugin
        // upload fail 500 ("plugin not found"), so they are restored right after initSetup.
        const {adminClient: preSetupClient} = await pw.getAdminClient();
        const bootPluginSettings = (await preSetupClient.getConfig()).PluginSettings;

        // # Initialize a user and team on the running server
        const {adminClient, user, team} = await pw.initSetup();

        // # Restore the boot-time plugin directories; see the capture comment above
        await adminClient.patchConfig({
            PluginSettings: {
                Directory: bootPluginSettings.Directory,
                ClientDirectory: bootPluginSettings.ClientDirectory,
            },
        });

        // * Verify EnableDocs is on (read only at boot, so it cannot be arranged from here)
        const config = await adminClient.getConfig();
        expect(
            String(config.FeatureFlags?.EnableDocs),
            'EnableDocs must be on — restart the server with MM_FEATUREFLAGS_ENABLEDOCS=true',
        ).toBe('true');

        // # Re-enable the plugin after initSetup's config reset disabled it; see @precondition
        await adminClient.enablePlugin(PLUGIN_ID);

        // * Verify the plugin bundle is deployed and active. Polled rather than read once:
        // enablePlugin returns before the plugin environment finishes activating, so an
        // immediate read can catch the plugin still listed as inactive.
        await expect
            .poll(
                async () => (await adminClient.getPlugins()).active.map((plugin) => plugin.id),
                {message: `the ${PLUGIN_ID} plugin must be deployed on this server`, timeout: 30_000},
            )
            .toContain(PLUGIN_ID);

        // # Log in and open the Docs product route
        const {page} = await pw.testBrowser.login(user);
        await page.goto(`/${team.name}/spaces`);

        // * Verify the product mounted and rendered its sidebar. Every space row carries the
        // same menu; the first is enough.
        const spaceMenu = page.getByRole('button', {name: /^Space options for /}).first();
        await expect(spaceMenu).toBeVisible();

        // # Open the space menu and choose its permissions entry
        await spaceMenu.click();
        await page.getByRole('menuitem', {name: 'Space permissions'}).click();

        // * Verify the permissions modal opened
        await expect(page.getByRole('heading', {name: /^Permissions for /})).toBeVisible();
    },
);
