// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

const presetValues = {
    Contribute: 'contribute',
    Comment: 'comment',
    'Read only': 'read_only',
} as const;

export type NewSpaceDefaultPresetLabel = keyof typeof presetValues;

// System Console → Plugins → Docs. Mattermost renders this page from plugin.json's settings_schema;
// the plugin webapp bundle does not need a second settings component for a standard dropdown.
export class DocsPluginSettingsPage {
    readonly page: Page;
    readonly newSpaceDefaultPreset: Locator;
    readonly saveButton: Locator;

    constructor(page: Page) {
        this.page = page;
        this.newSpaceDefaultPreset = page.getByLabel('Default permissions for new spaces');
        this.saveButton = page.getByRole('button', {name: 'Save'});
    }

    async goto() {
        await this.page.goto('/admin_console/plugins/plugin_com.mattermost.docs');
        await expect(this.newSpaceDefaultPreset).toBeVisible();
    }

    async expectPreset(label: NewSpaceDefaultPresetLabel) {
        await expect(this.newSpaceDefaultPreset).toHaveValue(presetValues[label]);
    }

    async choosePreset(label: NewSpaceDefaultPresetLabel) {
        await this.newSpaceDefaultPreset.selectOption(presetValues[label]);
    }

    async save() {
        const responsePromise = this.page.waitForResponse((response) =>
            response.request().method() === 'PUT' && response.url().includes('/api/v4/config/patch'),
        );
        await this.saveButton.click();
        const response = await responsePromise;
        if (!response.ok()) {
            throw new Error(`Unable to save Docs plugin settings: ${response.status()} ${await response.text()}`);
        }
    }
}
