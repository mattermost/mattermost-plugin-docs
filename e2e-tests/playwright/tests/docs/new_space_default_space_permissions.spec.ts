// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import type {Page} from '@playwright/test';

import {expect, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {readJsonOrThrow, requestedWith} from '../helpers/client';
import {createSpace, type Space} from '../helpers/docs';
import {pluginId} from '../helpers/preflight';
import {createTeam} from '../helpers/team';
import {DocsPluginSettingsPage} from '../pages/docs_plugin_settings_page';

type ServerConfig = {
    PluginSettings?: {
        Plugins?: Record<string, Record<string, unknown>>;
    };
};

const defaultPresetKey = 'newspacedefaultpreset';
const contributePermissions = ['comment_page', 'create_page', 'edit_page', 'delete_own_page'];

async function getConfiguredPreset(page: Page): Promise<string> {
    const response = await page.request.get('/api/v4/config', requestedWith);
    const config = await readJsonOrThrow<ServerConfig>(response, 'Unable to read the Docs plugin configuration');
    const settings = config.PluginSettings?.Plugins?.[pluginId];
    return String(settings?.[defaultPresetKey] ?? settings?.newSpaceDefaultPreset ?? 'contribute');
}

async function setConfiguredPreset(page: Page, preset: string) {
    const response = await page.request.put('/api/v4/config/patch', {
        ...requestedWith,
        data: {
            PluginSettings: {
                Plugins: {
                    [pluginId]: {[defaultPresetKey]: preset},
                },
            },
        },
    });
    if (!response.ok()) {
        throw new Error(`Unable to restore the Docs new-space default: ${response.status()} ${await response.text()}`);
    }
}

async function getSpace(page: Page, spaceId: string): Promise<Space> {
    const response = await page.request.get(`/plugins/${pluginId}/api/v1/spaces/${spaceId}`, requestedWith);
    return readJsonOrThrow<Space>(response, `Unable to read space ${spaceId}`);
}

test.describe('new-space default permissions', () => {
    let originalPreset = 'contribute';

    test.beforeEach(async ({page, server}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        originalPreset = await getConfiguredPreset(page);
    });

    test.afterEach(async ({page}) => {
        await setConfiguredPreset(page, originalPreset);
    });

    /**
     * @objective A system administrator can choose the preset copied into new Docs spaces.
     *
     * The test changes the setting through the rendered System Console control. Direct API calls
     * only create/read spaces and restore the shared setting during fixture cleanup.
     */
    test('copies the configured preset into future spaces without changing existing spaces', {tag: ['@docs', '@permissions']}, async ({page}) => {
        const pluginSettings = new DocsPluginSettingsPage(page);
        const team = await createTeam(page, 'docs-new-space-default');

        // # Select Comment as the site template in System Console and save it.
        await pluginSettings.goto();
        await pluginSettings.choosePreset('Comment');
        await pluginSettings.save();
        await pluginSettings.expectPreset('Comment');

        // * A create that omits default_permissions uses the selected seeded preset.
        const commentSpace = await createSpace(page, team.id, 'Comment default');
        expect(commentSpace.default_permissions).toEqual(['comment_page']);

        // # Change the site template to Read only.
        await pluginSettings.goto();
        await pluginSettings.choosePreset('Read only');
        await pluginSettings.save();
        await pluginSettings.expectPreset('Read only');

        // * The existing space keeps its copied default, while the next space uses Read only.
        const unchanged = await getSpace(page, commentSpace.id);
        expect(unchanged.default_permissions).toEqual(['comment_page']);
        const readOnlySpace = await createSpace(page, team.id, 'Read-only default');
        expect(readOnlySpace.default_permissions).toEqual([]);

        // * An explicit per-space value still overrides the site template.
        const overridden = await createSpace(page, team.id, 'Explicit contribute', contributePermissions);
        expect(overridden.default_permissions).toHaveLength(contributePermissions.length);
        expect(overridden.default_permissions).toEqual(expect.arrayContaining(contributePermissions));
    });
});
