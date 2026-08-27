// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

type AccessOption = 'Public' | 'Private';
type DefaultSummary = 'Can view' | 'Can comment' | 'Can edit' | 'Custom';
type DefaultPreset = 'Contribute' | 'Comment' | 'Read only';

const presetSummary: Record<DefaultPreset, DefaultSummary> = {
    Contribute: 'Can edit',
    Comment: 'Can comment',
    'Read only': 'Can view',
};

export class ShareSpaceModalPage {
    readonly page: Page;
    readonly dialog: Locator;
    readonly peopleInput: Locator;

    constructor(page: Page) {
        this.page = page;
        // By prefix: the accessible name carries the space title.
        this.dialog = page.getByRole('dialog', {name: /^Share /});
        this.peopleInput = this.dialog.getByLabel('Add people');
    }

    async expectOpen() {
        await expect(this.peopleInput).toBeVisible();
    }

    async close() {
        await this.dialog.getByRole('button', {name: 'Close'}).click();
        await expect(this.dialog).toBeHidden();
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

    private accessTrigger(): Locator {
        return this.dialog.getByRole('button', {name: /^(Public|Private)$/});
    }

    async expectAccess(access: AccessOption) {
        await expect(this.accessTrigger()).toHaveAccessibleName(access);
    }

    async chooseAccess(access: AccessOption) {
        await this.accessTrigger().click();
        await this.page.getByRole('menuitem', {name: access, exact: true}).click();
        await this.expectAccess(access);
    }

    private defaultPermissionsTrigger(): Locator {
        return this.dialog.getByRole('button', {name: /^(Can view|Can comment|Can edit|Custom)$/});
    }

    async expectDefaultSummary(summary: DefaultSummary) {
        await expect(this.defaultPermissionsTrigger()).toHaveAccessibleName(summary);
    }

    async openDefaultPermissions() {
        const menu = this.page.getByRole('menu');
        await expect(async () => {
            if (!(await menu.isVisible())) {
                await this.defaultPermissionsTrigger().click();
            }
            await expect(menu).toBeVisible({timeout: 1_000});
        }).toPass({timeout: 5_000});
    }

    defaultCapability(label: string): Locator {
        return this.page.getByRole('menu').getByRole('menuitemcheckbox', {name: label, exact: true});
    }

    async expectDefaultCapability(label: string, checked: boolean) {
        await this.openDefaultPermissions();
        await expect(this.defaultCapability(label)).toBeChecked({checked});
        await this.page.keyboard.press('Escape');
        await expect(this.page.getByRole('menu')).toBeHidden();
    }

    async toggleDefaultCapability(label: string) {
        await this.openDefaultPermissions();
        const capability = this.defaultCapability(label);
        const wasChecked = await capability.isChecked();

        await capability.click();

        // Checkbox menu items deliberately remain open while the server-derived set settles.
        await expect(capability).toBeChecked({checked: !wasChecked});
        await expect(this.page.getByRole('menu')).toBeVisible();
    }

    private defaultPreset(label: DefaultPreset): Locator {
        // Match the title at the start of the accessible name. "Comment" also appears in the
        // Contribute description, so a substring text filter would resolve both rows.
        return this.page.getByRole('menu').getByRole('menuitem', {name: new RegExp(`^${label}\\b`)});
    }

    async expectDefaultPresetOptions() {
        await this.openDefaultPermissions();
        await expect(this.defaultPreset('Contribute')).toBeVisible();
        await expect(this.defaultPreset('Comment')).toBeVisible();
        await expect(this.defaultPreset('Read only')).toBeVisible();
        await expect(this.page.getByRole('menuitemcheckbox')).toHaveCount(0);
        await this.page.keyboard.press('Escape');
        await expect(this.page.getByRole('menu')).toBeHidden();
    }

    async chooseDefaultPreset(label: DefaultPreset) {
        await this.openDefaultPermissions();
        await this.defaultPreset(label).click();
        await this.expectDefaultSummary(presetSummary[label]);
    }

    memberPermissionsTrigger(username: string): Locator {
        return this.dialog.getByRole('button', {name: `Permissions for ${username}`, exact: true});
    }

    async expectMemberSummary(username: string, summary: DefaultSummary | 'Admin' | 'Guest') {
        await expect(this.memberPermissionsTrigger(username)).toContainText(summary);
    }

    private async openMemberPermissions(username: string) {
        const menu = this.page.getByRole('menu');
        await expect(async () => {
            if (!(await menu.isVisible())) {
                await this.memberPermissionsTrigger(username).click();
            }
            await expect(menu.getByRole('menuitemcheckbox').first()).toBeVisible({timeout: 1_000});
        }).toPass({timeout: 5_000});
    }

    memberCapability(label: string): Locator {
        return this.page.getByRole('menu').getByRole('menuitemcheckbox', {name: label, exact: true});
    }

    async expectMemberCapability(username: string, label: string, checked: boolean) {
        await this.openMemberPermissions(username);
        await expect(this.memberCapability(label)).toBeChecked({checked});
        await this.page.keyboard.press('Escape');
        await expect(this.page.getByRole('menu')).toBeHidden();
    }

    async toggleMemberCapability(username: string, label: string) {
        await this.openMemberPermissions(username);
        const capability = this.memberCapability(label);
        const wasChecked = await capability.isChecked();

        await capability.click();

        await expect(capability).toBeChecked({checked: !wasChecked});
        await expect(this.page.getByRole('menu')).toBeVisible();
    }
}
