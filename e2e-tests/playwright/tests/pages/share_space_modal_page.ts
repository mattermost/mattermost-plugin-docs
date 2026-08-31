// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

type AccessOption = 'Public' | 'Private';

// The named tiers. The trigger summarises the current default with the same vocabulary the
// menu items use, so choosing a tier reads back as its own name.
type DefaultTier = 'Can view' | 'Can comment' | 'Can edit';
type DefaultSummary = DefaultTier | 'Custom';

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
        await this.page.getByRole('menuitemradio', {name: access, exact: true}).click();
        await this.expectAccess(access);
    }

    private defaultPermissionsTrigger(): Locator {
        return this.dialog.getByRole('button', {name: /^(Can view|Can comment|Can edit|Custom)$/});
    }

    async expectDefaultSummary(summary: DefaultSummary) {
        await expect(this.defaultPermissionsTrigger()).toHaveAccessibleName(summary);
    }

    // The default-permissions and member-permissions menus both render role=menu with identical
    // item labels, and the toggle helpers deliberately leave their menu open. Keying "already
    // open" to this helper's own trigger — not to any visible menu — keeps one helper from
    // toggling inside the other's still-open menu.
    private async openMenuFor(trigger: Locator): Promise<Locator> {
        await expect(async () => {
            if ((await trigger.getAttribute('aria-expanded')) !== 'true') {
                await trigger.click();
            }
            await expect(trigger).toHaveAttribute('aria-expanded', 'true', {timeout: 1_000});
        }).toPass({timeout: 5_000});

        const menuId = await trigger.getAttribute('aria-controls');
        const menu = menuId ? this.page.locator(`[id="${menuId}"]`) : this.page.getByRole('menu');
        await expect(menu).toBeVisible();
        return menu;
    }

    async openDefaultPermissions() {
        await this.openMenuFor(this.defaultPermissionsTrigger());
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

    private defaultTier(label: DefaultTier): Locator {
        // Match the title at the start of the accessible name: the item's description follows
        // it, and "comment" also appears in the Can edit description, so a substring text
        // filter would resolve two rows.
        return this.page.getByRole('menu').getByRole('menuitemradio', {name: new RegExp(`^${label}\\b`)});
    }

    // The unlicensed shape: the three tiers and nothing to refine them with.
    async expectDefaultTierOptionsOnly() {
        await this.openDefaultPermissions();
        await expect(this.defaultTier('Can view')).toBeVisible();
        await expect(this.defaultTier('Can comment')).toBeVisible();
        await expect(this.defaultTier('Can edit')).toBeVisible();
        await expect(this.page.getByRole('menuitemcheckbox')).toHaveCount(0);
        await this.page.keyboard.press('Escape');
        await expect(this.page.getByRole('menu')).toBeHidden();
    }

    async chooseDefaultTier(label: DefaultTier) {
        await this.openDefaultPermissions();
        await this.defaultTier(label).click();
        await this.expectDefaultSummary(label);
    }

    memberPermissionsTrigger(username: string): Locator {
        // The accessible name is prefixed with the member's visible role text once their
        // permission record resolves ("Can edit — permissions for …"), so match the tail.
        return this.dialog.getByRole('button', {name: new RegExp(`permissions for ${username}$`, 'i')});
    }

    async expectMemberSummary(username: string, summary: DefaultSummary | 'Admin' | 'Guest') {
        await expect(this.memberPermissionsTrigger(username)).toContainText(summary);
    }

    private async openMemberPermissions(username: string) {
        const menu = await this.openMenuFor(this.memberPermissionsTrigger(username));
        await expect(menu.getByRole('menuitemcheckbox').first()).toBeVisible();
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
