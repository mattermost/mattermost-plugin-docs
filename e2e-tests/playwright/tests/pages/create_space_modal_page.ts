// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

const visibilityOptionNames = {
    Public: 'Public Space Any team member can view',
    Private: 'Private Space Only invited members',
} as const;

type VisibilityOption = keyof typeof visibilityOptionNames;

export class CreateSpaceModalPage {
    readonly page: Page;
    readonly dialog: Locator;
    readonly nameInput: Locator;
    readonly createButton: Locator;

    constructor(page: Page) {
        this.page = page;
        this.dialog = page.getByRole('dialog', {name: 'Create a new space'});
        this.nameInput = this.dialog.getByLabel('Space name');
        this.createButton = this.dialog.getByRole('button', {name: 'Create', exact: true});
    }

    async expectOpen() {
        await expect(this.nameInput).toBeVisible();
    }

    visibilityOption(name: VisibilityOption): Locator {
        return this.dialog.getByRole('radio', {name: visibilityOptionNames[name], exact: true});
    }

    async expectVisibility(name: VisibilityOption) {
        await expect(this.visibilityOption(name)).toHaveAttribute('aria-checked', 'true');
    }

    async chooseVisibility(name: VisibilityOption) {
        await this.visibilityOption(name).click();
        await this.expectVisibility(name);
    }

    async createSpace(name: string) {
        await this.nameInput.fill(name);
        await expect(this.createButton).toBeEnabled();
        await this.createButton.click();
        await expect(this.dialog).toBeHidden();
    }
}
