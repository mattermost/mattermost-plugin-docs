// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

export class SpacesSidebarPage {
    readonly page: Page;
    readonly sidebar: Locator;
    readonly createSpaceButton: Locator;
    readonly findDocsButton: Locator;

    constructor(page: Page) {
        this.page = page;
        this.sidebar = page.getByLabel('Spaces', {exact: true});

        // Scoped: the empty state offers a second button with the same name.
        this.createSpaceButton = this.sidebar.getByRole('button', {name: 'Create a space'});

        this.findDocsButton = this.sidebar.getByRole('button', {name: 'Find docs'});
    }

    async goto(teamName: string) {
        await this.page.goto(`/${teamName}/spaces`);
        await this.createSpaceButton.waitFor();
    }

    async openCreateSpace() {
        await this.createSpaceButton.click();
    }

    spaceLink(spaceTitle: string): Locator {
        return this.sidebar.getByRole('link', {name: spaceTitle});
    }

    async expectSpaceListed(spaceTitle: string) {
        await expect(this.spaceLink(spaceTitle)).toBeVisible();
    }

    async openSpace(spaceTitle: string) {
        await this.spaceLink(spaceTitle).click();
    }

    async openSwitcher() {
        await this.findDocsButton.click();
    }

    // Read, not hard-coded: the app picks Meta or Control from the user agent, and
    // Playwright's Desktop Chrome descriptor reports a Windows UA even on macOS.
    async advertisedSwitcherShortcut(): Promise<string> {
        const shortcut = await this.findDocsButton.getAttribute('aria-keyshortcuts');

        if (!shortcut) {
            throw new Error('The Find docs button advertises no aria-keyshortcuts');
        }

        return shortcut;
    }
}
