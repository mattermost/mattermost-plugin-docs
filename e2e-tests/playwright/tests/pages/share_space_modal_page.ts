// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

export class ShareSpaceModalPage {
    readonly page: Page;
    readonly dialog: Locator;
    readonly peopleInput: Locator;
    readonly addButton: Locator;

    constructor(page: Page) {
        this.page = page;

        // Matches this branch's Share modal, which still has the fixed title and a separate Add
        // button. Master reworked it to carry the space title in the accessible name and to commit
        // each pick on select, and upstream tracks that; this branch has not merged that commit yet.
        // Revert to upstream's prefix match and commit-on-select once it has.
        // (Local delta — see README-VENDORED.md.)
        this.dialog = page.getByRole('dialog', {name: 'Share space'});
        this.peopleInput = this.dialog.getByLabel('Add people or groups');
        this.addButton = this.dialog.getByRole('button', {name: 'Add', exact: true});
    }

    async expectOpen() {
        await expect(this.peopleInput).toBeVisible();
    }

    async addMember(username: string) {
        await this.peopleInput.fill(username);

        // The picker searches asynchronously; selecting the option turns it into a
        // pending chip, which is what enables Add.
        await this.page.getByRole('option', {name: new RegExp(username)}).first().click();
        await expect(this.addButton).toBeEnabled();
        await this.addButton.click();
    }

    async expectMemberListed(username: string) {
        await expect(this.dialog.getByText(new RegExp(username)).first()).toBeVisible();
    }
}
