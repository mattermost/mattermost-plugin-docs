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

        // The selected person first appears as a temporary picker chip while the POST is still in
        // flight. Wait for the roster handle instead: it is rendered only after addSpaceMember has
        // resolved and dispatched the committed membership. Without this, a test can close the
        // page while the request is pending and abort the very membership it intends to verify.
        await this.expectMemberListed(username);
    }

    async expectMemberListed(username: string) {
        // Exact handle distinguishes the committed MemberList row from the picker's pending chip,
        // whose display name commonly contains the same generated username.
        await expect(this.dialog.getByText(`@${username}`, {exact: true})).toBeVisible();
    }
}
