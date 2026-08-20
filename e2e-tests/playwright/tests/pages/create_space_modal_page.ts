// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

export class CreateSpaceModalPage {
    readonly page: Page;
    readonly dialog: Locator;
    readonly nameInput: Locator;
    readonly createButton: Locator;

    constructor(page: Page) {
        this.page = page;
        this.dialog = page.getByRole('dialog');
        this.nameInput = this.dialog.getByLabel('Space name');
        this.createButton = this.dialog.getByRole('button', {name: 'Create', exact: true});
    }

    async expectOpen() {
        await expect(this.nameInput).toBeVisible();
    }

    async createSpace(name: string) {
        await this.nameInput.fill(name);
        await expect(this.createButton).toBeEnabled();
        await this.createButton.click();
        await expect(this.dialog).toBeHidden();
    }
}
