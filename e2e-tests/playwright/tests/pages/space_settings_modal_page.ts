// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

// The space's exposure controls live behind Space settings → Permissions: who can find
// the space (view access) and what its members can do (the default permission set).
// Both are admin-only, and both are rendered for a non-admin in a locked state, so the
// same locators serve the allowed and the refused case.
export class SpaceSettingsModalPage {
    readonly page: Page;
    readonly dialog: Locator;
    readonly permissionsTab: Locator;
    readonly permissionLegend: Locator;

    constructor(page: Page) {
        this.page = page;
        this.dialog = page.getByRole('dialog', {name: 'Space Settings'});
        this.permissionsTab = this.dialog.getByRole('tab', {name: 'Permissions'});
        this.permissionLegend = this.dialog.getByText('Everyone with access to this space can:');
    }

    // Scoped to main and matched exactly: the sidebar row for the same space offers a
    // "Space options for <title>" button whose menu deliberately has no settings entry, and
    // an unscoped substring match reaches that one first.
    //
    // The Space settings item only exists for a member, so a non-member reaching this fails
    // here rather than at an assertion further down.
    async openSpaceHeaderMenu(spaceTitle: string) {
        await this.page.getByRole('main').getByRole('button', {name: spaceTitle, exact: true}).click();
    }

    async openFromSpaceHeader(spaceTitle: string) {
        await this.openSpaceHeaderMenu(spaceTitle);
        await this.page.getByRole('menuitem', {name: 'Space settings'}).click();
        await expect(this.dialog).toBeVisible();
    }

    async openPermissions() {
        await this.permissionsTab.click();

        // The permission set is the last thing the tab renders and its state arrives over
        // the network, so waiting on the legend means waiting for a tab that can be read.
        await expect(this.permissionLegend).toBeVisible();
    }

    async close() {
        await this.dialog.getByRole('button', {name: 'Close'}).click();
        await expect(this.dialog).toBeHidden();
    }

    async renameSpace(name: string) {
        const input = this.dialog.getByLabel('Space name');
        await input.fill(name);

        const save = this.dialog.getByRole('button', {name: 'Save', exact: true});
        await save.click();

        // The save bar is derived from the live server-backed record. It disappears only after
        // the update is dispatched back into the store and the edited value becomes the baseline.
        await expect(save).toBeHidden();
        await expect(input).toHaveValue(name);
    }

    // Scoped to the space-default group by its legend: the per-member matrix rows below
    // render the same vocabulary, so an unscoped label match is ambiguous by the number
    // of members in the space.
    permission(label: string): Locator {
        return this.dialog
            .locator('fieldset')
            .filter({hasText: 'Everyone with access to this space can:'})
            .getByRole('checkbox', {name: label});
    }

    // A member's own toggle in the matrix. Addressed by the id the tab builds
    // (`member-<userId>-<permission>`) rather than by label: the same vocabulary is
    // rendered once for the space default and once per member, so a label match is
    // ambiguous by the number of members.
    memberPermission(userId: string, permission: string): Locator {
        return this.dialog.locator(`#member-${userId}-${permission}`);
    }

    async expectMemberPermission(userId: string, permission: string, checked: boolean) {
        await expect(this.memberPermission(userId, permission)).toBeChecked({checked});
    }

    async toggleMemberPermission(userId: string, permission: string) {
        const box = this.memberPermission(userId, permission);
        const wasChecked = await box.isChecked();

        await box.click();

        // The server's answer is what flips it, so this waits on the write landing.
        await expect(box).toBeChecked({checked: !wasChecked});
    }

    async expectMemberPermissionEnabled(userId: string, permission: string, enabled: boolean) {
        if (enabled) {
            await expect(this.memberPermission(userId, permission)).toBeEnabled();
        } else {
            await expect(this.memberPermission(userId, permission)).toBeDisabled();
        }
    }

    memberHandle(username: string): Locator {
        return this.dialog.getByText(`@${username}`, {exact: true});
    }

    async expectMemberListed(username: string, listed = true) {
        if (listed) {
            await expect(this.memberHandle(username)).toBeVisible();
        } else {
            await expect(this.memberHandle(username)).toBeHidden();
        }
    }

    async addMember(username: string) {
        const input = this.dialog.getByLabel('Add people or groups');
        await input.fill(username);
        await this.page.getByRole('option', {name: new RegExp(username)}).first().click();
        await this.dialog.getByRole('button', {name: 'Add', exact: true}).click();
        await this.expectMemberListed(username);
    }

    private async openMemberMenu(username: string) {
        // Seeded E2E users use their username as their display name, which is also what the
        // product places in this accessible label. Addressing the button directly avoids walking
        // a CSS-module-dependent parent chain from the @handle.
        await this.dialog.getByRole('button', {name: `Admin, manage ${username}`, exact: true}).click();
    }

    async removeMember(username: string) {
        await this.openMemberMenu(username);
        const remove = this.page.getByRole('menuitem', {name: 'Remove from space'});
        await expect(remove).toBeVisible();
        await expect(remove).toBeEnabled();

        // Adding the row starts two independent live updates (profiles and permission records).
        // Base UI can replace the menu anchor between Playwright's stability samples even though
        // the already-open item is visible and enabled. Force only skips that animation/stability
        // wait; the menu was opened and the real click handler still drives confirmation.
        await remove.click({force: true});
        await this.page.getByRole('dialog', {name: new RegExp(`Remove .*${username}`, 'i')}).getByRole('button', {name: 'Yes, remove'}).click();
        await this.expectMemberListed(username, false);
    }

    async requestLeaveFromOwnRow(username: string) {
        await this.openMemberMenu(username);
        const leave = this.page.getByRole('menuitem', {name: 'Leave space'});
        await expect(leave).toBeVisible();
        await expect(leave).toBeEnabled();

        // The roster refresh can replace Base UI's menu anchor while Playwright waits for its
        // stability checks. As with removal above, assert the item is actionable first and let
        // the real UI click handler open the confirmation without waiting on that animation.
        await leave.click({force: true});
        await this.page.getByRole('dialog', {name: 'Leave space'}).getByRole('button', {name: 'Yes, leave space'}).click();
    }

    async leaveFromOwnRow(username: string) {
        await this.requestLeaveFromOwnRow(username);
        await expect(this.dialog).toBeHidden();
    }

    async expectAutoJoinedMarker(visible = true) {
        const marker = this.dialog.getByText('Joined automatically by editing this space');
        if (visible) {
            await expect(marker).toBeVisible();
        } else {
            await expect(marker).toBeHidden();
        }
    }

    accessOption(name: 'Public' | 'Private'): Locator {
        return this.dialog.getByRole('radio', {name: new RegExp(name)});
    }

    // Waits for the checkbox to settle rather than asserting immediately: the set is
    // re-read from the server after every change, so the box is briefly stale.
    async expectPermission(label: string, checked: boolean) {
        await expect(this.permission(label)).toBeChecked({checked});
    }

    async expectPermissionEnabled(label: string, enabled: boolean) {
        if (enabled) {
            await expect(this.permission(label)).toBeEnabled();
        } else {
            await expect(this.permission(label)).toBeDisabled();
        }
    }

    async togglePermission(label: string) {
        const box = this.permission(label);
        const wasChecked = await box.isChecked();

        await box.click();

        // The server response is what flips it back, so this is the save completing —
        // not just the click landing.
        await expect(box).toBeChecked({checked: !wasChecked});
    }

    async chooseAccess(name: 'Public' | 'Private') {
        await this.accessOption(name).click();
        await expect(this.accessOption(name)).toHaveAttribute('aria-checked', 'true');
    }

    async expectAccess(name: 'Public' | 'Private') {
        await expect(this.accessOption(name)).toHaveAttribute('aria-checked', 'true');
    }

    async expectAccessEnabled(name: 'Public' | 'Private', enabled: boolean) {
        if (enabled) {
            await expect(this.accessOption(name)).toBeEnabled();
        } else {
            await expect(this.accessOption(name)).toBeDisabled();
        }
    }

}
