// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

// The Find-docs switcher: a modal search over spaces and pages, opened either from
// the sidebar button or with Cmd/Ctrl+K.
export class DocsSwitcherPage {
    readonly page: Page;
    readonly searchInput: Locator;
    readonly results: Locator;

    constructor(page: Page) {
        this.page = page;

        // Located by the input rather than the dialog: the modal's accessible name
        // changes from "Find docs" to "Find spaces or pages" as soon as a query is
        // typed, so it is not a stable handle.
        this.searchInput = page.getByRole('combobox', {name: 'Search all spaces and pages'});
        this.results = page.getByRole('listbox');
    }

    // Takes the shortcut the product advertises rather than assuming one — see
    // SpacesSidebarPage.advertisedSwitcherShortcut. The shortcut toggles rather than
    // opens, so the switcher has to be known-closed first or this would dismiss an
    // already-open one.
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

    // Keyboard selection: the switcher advertises arrow/Enter navigation, and it is the
    // faster path for anyone who opened it with the shortcut.
    async selectResultWithKeyboard(title: string) {
        await expect(this.result(title)).toBeVisible();
        await this.searchInput.press('ArrowDown');
        await this.searchInput.press('Enter');
    }
}
