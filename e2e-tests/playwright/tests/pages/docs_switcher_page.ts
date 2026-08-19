// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

// Modal search over spaces and pages, opened from the sidebar or with Cmd/Ctrl+K.
export class DocsSwitcherPage {
    readonly page: Page;
    readonly searchInput: Locator;
    readonly results: Locator;

    constructor(page: Page) {
        this.page = page;

        // By input, not dialog: the modal's accessible name changes once a query is typed.
        this.searchInput = page.getByRole('combobox', {name: 'Search all spaces and pages'});
        this.results = page.getByRole('listbox');
    }

    // Toggles rather than opens, so it must be known-closed first.
    async openWithShortcut(shortcut: string) {
        await this.expectClosed();
        await this.page.keyboard.press(shortcut);
        await this.expectOpen();
    }

    async expectOpen() {
        await expect(this.searchInput).toBeVisible();
    }

    async expectClosed() {
        await expect(this.searchInput).toBeHidden();
    }

    async search(query: string) {
        await this.searchInput.fill(query);
    }

    result(title: string): Locator {
        return this.results.getByRole('option', {name: title});
    }

    async selectResult(title: string) {
        await this.result(title).click();
    }

    // The list pre-highlights the first entry and ArrowDown wraps, so walk the highlight
    // onto the wanted option: a fixed number of presses opens whatever else matched.
    async selectResultWithKeyboard(title: string) {
        const option = this.result(title);
        await expect(option).toBeVisible();

        await expect(async () => {
            if (await option.getAttribute('aria-selected') !== 'true') {
                await this.searchInput.press('ArrowDown');
            }
            await expect(option).toHaveAttribute('aria-selected', 'true');
        }).toPass({timeout: 10_000});

        await this.searchInput.press('Enter');
    }
}
