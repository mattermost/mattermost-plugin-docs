// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

// System Console → User Management → Permissions → System Scheme.
//
// This page belongs to the core webapp, not to this plugin: the Spaces permission group it
// renders is added by the paired core branch and gated on the EnableDocs feature flag. It is
// driven from here because only this repo has the plugin installed, so only here can a change
// made in the console be observed as a change in what a Docs route allows.
//
// Row-level locators go through the ids core builds from the role name
// (`<role>-<group>-<permission>`) rather than through the visible label: the same four space
// permissions are rendered once per role tree on this page, so a label match is ambiguous by
// four. Clicking a group row toggles every permission in it, while the caret beside it only
// expands — the two are separate targets in core's markup.
export class SystemSchemePermissionsPage {
    readonly page: Page;
    readonly saveButton: Locator;
    readonly spacesGroup: Locator;

    // What core roots the row ids on: the system scheme page synthesizes a role object for each
    // tree and names the All Members one 'all_users' — not 'system_user', the underlying role it
    // aggregates.
    private static readonly allMembersRole = 'all_users';

    constructor(page: Page) {
        this.page = page;
        this.saveButton = page.getByRole('button', {name: 'Save'});
        this.spacesGroup = page.locator(`#${SystemSchemePermissionsPage.allMembersRole}-spaces`);
    }

    async goto() {
        await this.page.goto('/admin_console/user_management/permissions/system_scheme');
        await expect(this.saveButton).toBeVisible();
    }

    async expectSpacesGroupVisible() {
        await expect(this.spacesGroup).toBeVisible();
        await expect(this.spacesGroup).toContainText('Spaces');
    }

    // The caret, not the row: the row's own click handler selects the whole group.
    async expandSpacesGroup() {
        await this.spacesGroup.locator('.permission-arrow').click();
        await expect(this.permissionRow('read_space')).toBeVisible();
    }

    permissionRow(permission: string): Locator {
        return this.page.locator(`#${SystemSchemePermissionsPage.allMembersRole}-spaces-${permission}`);
    }

    // Core renders the tick as a styled div rather than an input, so "checked" is the presence
    // of its checked marker rather than a form state.
    private checkedMarker(permission: string): Locator {
        return this.permissionRow(permission).getByTestId('permissionCheckbox-checked');
    }

    async expectPermissionChecked(permission: string, checked: boolean) {
        if (checked) {
            await expect(this.checkedMarker(permission)).toBeVisible();
        } else {
            await expect(this.checkedMarker(permission)).toBeHidden();
        }
    }

    async togglePermission(permission: string) {
        // isVisible() does not retry, and expandSpacesGroup only proves the read_space row rendered,
        // so this row can still be absent when the read runs. A premature false inverts the
        // assertion below, which then fails on a click that succeeded.
        await this.permissionRow(permission).waitFor();

        const wasChecked = await this.checkedMarker(permission).isVisible();

        await this.permissionRow(permission).click();

        await this.expectPermissionChecked(permission, !wasChecked);
    }

    // Save is disabled again once the write lands, which is what makes it a completion signal
    // rather than just a click.
    async save() {
        await expect(this.saveButton).toBeEnabled();
        await this.saveButton.click();
        await expect(this.saveButton).toBeDisabled();
    }
}
