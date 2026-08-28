// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

export class ShareSpaceModalPage {
    readonly page: Page;
    readonly dialog: Locator;
    readonly peopleInput: Locator;

    constructor(page: Page) {
        this.page = page;
        // By prefix: the accessible name carries the space title.
        this.dialog = page.getByRole('dialog', {name: /^Share /});
        this.peopleInput = this.dialog.getByLabel('Add people or groups');
    }

    async expectOpen() {
        await expect(this.peopleInput).toBeVisible();
    }

    async addMember(username: string) {
        await this.peopleInput.fill(username);

        // This modal commits each pick immediately; there is no Add button to press.
        await this.page.getByRole('option', {name: new RegExp(username)}).first().click();
    }

    async expectMemberListed(username: string) {
        await expect(this.dialog.getByText(new RegExp(username)).first()).toBeVisible();
    }
}
