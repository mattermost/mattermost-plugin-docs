// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import type {Browser} from '@playwright/test';

import {expect, newContext, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {readJsonOrThrow, requestedWith} from '../helpers/client';
import {addSpaceMember, createSpace} from '../helpers/docs';
import {restoreBaselineTeamPermissions} from '../helpers/preflight';
import {addUserToTeam, createTeam} from '../helpers/team';
import {createUser, type SeededUser} from '../helpers/user';
import {SpacePage} from '../pages/space_page';
import {SpaceSettingsModalPage} from '../pages/space_settings_modal_page';
import {SpacesSidebarPage} from '../pages/spaces_sidebar_page';
import {SystemSchemePermissionsPage} from '../pages/system_scheme_permissions_page';

type Role = {
    id: string;
    permissions: string[];
};

/**
 * The team-level space permissions, as an administrator manages them in the System Console.
 *
 * These four permissions (view / create / manage / delete spaces) are core's, not the plugin's:
 * the paired core branch adds them to the system scheme's permission tree, gated on the
 * EnableDocs feature flag. They are exercised here rather than in the core PR because the
 * consequence of changing one is only observable with the Docs plugin installed — core alone
 * has no Docs UI that consumes these permissions.
 *
 * So this is the one assertion that neither repo can make on its own: a permission toggled in
 * core's console changes which Docs journeys the plugin UI offers and can complete.
 *
 * Note the two layers do not overlap. This is the *team* question — may this user make a space
 * at all. What a member may do *inside* a space is the space's own permission set, covered by
 * space_permissions.spec.ts. The group's own description says as much: "The permissions a member
 * holds inside a space are set in the space itself, not here."
 *
 * CAUTION for anyone adding to this file: unlike the space-scoped specs, these tests write the
 * *system scheme*, which is server-wide and shared by every worker. Each mutation marks the role
 * for restoration in `afterEach`, whose separate timeout still runs when the test body exhausts
 * its own. Anything running concurrently would still see the mutation window. They are safe beside
 * space_permissions.spec.ts because that suite creates its spaces as the system admin, who bypasses
 * create_space via manage_system, and probes its member against a space permission rather than this
 * team permission. Any new test here that writes the scheme must mark the role for restoration once
 * its write lands; a case needing a durable non-default permission should seed its own team-override
 * scheme instead of editing the system scheme.
 */
test.describe('system console space permissions', () => {
    let teamId: string;
    let teamName: string;
    let spaceId: string;
    let spaceTitle: string;
    let member: SeededUser;
    let baselineTeamUserRole: Role;
    let restoreRoleAfterTest = false;

    test.beforeEach(async ({browser, server}) => {
        restoreRoleAfterTest = false;
        await restoreBaselineTeamPermissions(server.baseURL, server.adminUsername, server.adminPassword);

        const context = await newContext(browser, {baseURL: server.baseURL});
        const page = await context.newPage();

        await loginAs(page, server.adminUsername, server.adminPassword);

        const roleResponse = await page.request.get('/api/v4/roles/name/team_user', requestedWith);
        baselineTeamUserRole = await readJsonOrThrow<Role>(roleResponse, 'Unable to capture the team-user permission baseline');

        const team = await createTeam(page, 'docs-sysconsole');
        teamId = team.id;
        teamName = team.name;

        member = await createUser(page, 'docs-sysconsole-member');
        await addUserToTeam(page, teamId, member.id);

        spaceTitle = `Console Space ${member.username.slice(-8)}`;
        const space = await createSpace(page, teamId, spaceTitle);
        spaceId = space.id;
        await addSpaceMember(page, spaceId, member.id);

        await context.close();
    });

    // Restores the exact role baseline captured before each test. This is fixture cleanup, not a
    // permission outcome: the mutation and every consequence are still driven/asserted in the UI.
    // Cleanup deliberately uses the API because a second System Console toggle can itself fail
    // mid-navigation and leave read_space revoked, cascading into every later browser persona.
    //
    // Its own context rather than the `page` fixture: this runs after a test that may have
    // failed mid-navigation, so it cannot inherit that page's state.
    const restoreBaseline = async (
        baseURL: string,
        browser: Browser,
        adminUsername: string,
        adminPassword: string,
    ) => {
        const context = await newContext(browser, {baseURL});
        try {
            const page = await context.newPage();
            await loginAs(page, adminUsername, adminPassword);

            const response = await page.request.put(`/api/v4/roles/${baselineTeamUserRole.id}/patch`, {
                ...requestedWith,
                data: {permissions: baselineTeamUserRole.permissions},
            });
            if (!response.ok()) {
                throw new Error(`Unable to restore team_user after a System Console test: ${response.status()} ${await response.text()}`);
            }
        } finally {
            await context.close();
        }
    };

    // afterEach has its own timeout budget. Keeping cleanup here means a test that spends all 60
    // seconds waiting on a browser assertion cannot lose the role restore when Playwright ends it.
    test.afterEach(async ({browser, server}) => {
        if (!restoreRoleAfterTest) {
            return;
        }

        restoreRoleAfterTest = false;
        await restoreBaseline(server.baseURL, browser, server.adminUsername, server.adminPassword);
    });

    /**
     * @objective The Spaces permission group is present in the system scheme.
     *
     * The group is feature-flag gated, so its absence is how a server without Docs core support
     * would present itself here. Pinning the four permissions by row means a rename or a dropped
     * entry in core fails against the plugin that depends on it, rather than silently.
     */
    test('offers the Spaces permission group with its four permissions', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        const scheme = new SystemSchemePermissionsPage(page);

        // # Open the system scheme as a system administrator
        await loginAs(page, server.adminUsername, server.adminPassword);
        await scheme.goto();

        // * Verify the group is rendered for All Members
        await scheme.expectSpacesGroupVisible();

        // # Expand it
        await scheme.expandSpacesGroup();

        // * Verify each permission is offered, and that the defaults are what a fresh server
        //   grants an ordinary member: view and create, but not manage or delete.
        await scheme.expectPermissionChecked('read_space', true);
        await scheme.expectPermissionChecked('create_space', true);
        await scheme.expectPermissionChecked('manage_space', false);
        await scheme.expectPermissionChecked('delete_space', false);
    });

    /**
     * @objective Revoking create_space in the console stops a member creating Docs spaces.
     *
     * The cross-repo assertion: core's console writes the role, the plugin's route reads it.
     * Neither half proves this alone.
     */
    test('revoking create_space stops a member creating a space', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const scheme = new SystemSchemePermissionsPage(page);
        let revoked = false;

        try {
            // * Verify an ordinary team member starts with the browser entry point.
            const beforeContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await beforeContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                await sidebar.goto(teamName);
                await expect(sidebar.createSpaceButton).toBeVisible();
            } finally {
                await beforeContext.close();
            }

            // # Revoke create_space from All Members and save
            await loginAs(page, server.adminUsername, server.adminPassword);
            await scheme.goto();
            await scheme.expectSpacesGroupVisible();
            await scheme.expandSpacesGroup();
            await scheme.togglePermission('create_space');
            revoked = true;
            await scheme.save();

            // * Verify a fresh member session no longer offers any create journey.
            const afterContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await afterContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                await sidebar.goto(teamName);
                await expect(sidebar.createSpaceButton).toBeHidden();

                await memberPage.getByRole('button', {name: 'Add or browse spaces'}).click();
                await expect(memberPage.getByRole('menuitem', {name: 'Create a space'})).toBeHidden();
                await expect(memberPage.getByRole('menuitem', {name: 'Browse spaces'})).toBeVisible();
            } finally {
                await afterContext.close();
            }

            // * Verify the change survives a reload rather than living in page state
            await page.reload();
            await scheme.expectSpacesGroupVisible();
            await scheme.expandSpacesGroup();
            await scheme.expectPermissionChecked('create_space', false);

            // * Verify the neighbouring permission was untouched — the row click toggled one
            //   permission, not the whole group
            await scheme.expectPermissionChecked('read_space', true);
        } finally {
            if (revoked) {
                restoreRoleAfterTest = true;
            }
        }
    });

    /** @objective Revoking read_space removes discovery and known-link access in the UI. */
    test('revoking read_space removes a member\'s space access', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const scheme = new SystemSchemePermissionsPage(page);
        let revoked = false;

        try {
            // * An invited member can discover and open the fixture before the team grant changes.
            const beforeContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await beforeContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                const space = new SpacePage(memberPage);
                await sidebar.goto(teamName);
                await sidebar.expectSpaceListed(spaceTitle);
                await sidebar.openSpace(spaceTitle);
                await space.expectOpen(spaceTitle);
            } finally {
                await beforeContext.close();
            }

            await loginAs(page, server.adminUsername, server.adminPassword);
            await scheme.goto();
            await scheme.expandSpacesGroup();
            await scheme.togglePermission('read_space');
            revoked = true;
            await scheme.save();

            // * A fresh browser session loses both the list entry and a known deep link.
            const afterContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await afterContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                await sidebar.goto(teamName);
                await expect(sidebar.spaceLink(spaceTitle)).toBeHidden();
                await memberPage.goto(`/${teamName}/spaces/${spaceId}`);
                await expect(memberPage).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
            } finally {
                await afterContext.close();
            }

            await page.reload();
            await scheme.expandSpacesGroup();
            await scheme.expectPermissionChecked('read_space', false);
        } finally {
            if (revoked) {
                restoreRoleAfterTest = true;
            }
        }
    });

    /** @objective manage_space unlocks settings and roster management, but not archive. */
    test('granting manage_space unlocks only the manage tier', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const scheme = new SystemSchemePermissionsPage(page);
        let granted = false;

        try {
            // * The ordinary member starts without either privileged header entry.
            const beforeContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await beforeContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                const settings = new SpaceSettingsModalPage(memberPage);
                await sidebar.goto(teamName);
                await sidebar.openSpace(spaceTitle);
                await settings.openSpaceHeaderMenu(spaceTitle);
                await expect(memberPage.getByRole('menuitem', {name: 'Space settings'})).toBeHidden();
                await expect(memberPage.getByRole('menuitem', {name: 'Archive space'})).toBeHidden();
            } finally {
                await beforeContext.close();
            }

            await loginAs(page, server.adminUsername, server.adminPassword);
            await scheme.goto();
            await scheme.expandSpacesGroup();
            await scheme.togglePermission('manage_space');
            granted = true;
            await scheme.save();

            // * The exact tier appears in a fresh session: settings yes, archive still no.
            const afterContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await afterContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                const settings = new SpaceSettingsModalPage(memberPage);
                await sidebar.goto(teamName);
                await sidebar.openSpace(spaceTitle);
                await settings.openSpaceHeaderMenu(spaceTitle);
                await expect(memberPage.getByRole('menuitem', {name: 'Space settings'})).toBeVisible();
                await expect(memberPage.getByRole('menuitem', {name: 'Archive space'})).toBeHidden();
            } finally {
                await afterContext.close();
            }

            await page.reload();
            await scheme.expandSpacesGroup();
            await scheme.expectPermissionChecked('manage_space', true);
        } finally {
            if (granted) {
                restoreRoleAfterTest = true;
            }
        }
    });

    /** @objective delete_space unlocks and completes archive without unlocking settings. */
    test('granting delete_space unlocks only the archive tier', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const scheme = new SystemSchemePermissionsPage(page);
        let granted = false;

        try {
            await loginAs(page, server.adminUsername, server.adminPassword);
            await scheme.goto();
            await scheme.expandSpacesGroup();
            await scheme.togglePermission('delete_space');
            granted = true;
            await scheme.save();

            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await context.newPage();
                await loginAs(memberPage, member.username, member.password);
                const sidebar = new SpacesSidebarPage(memberPage);
                const space = new SpacePage(memberPage);
                const settings = new SpaceSettingsModalPage(memberPage);
                await sidebar.goto(teamName);
                await sidebar.openSpace(spaceTitle);
                await settings.openSpaceHeaderMenu(spaceTitle);
                await expect(memberPage.getByRole('menuitem', {name: 'Space settings'})).toBeHidden();
                await expect(memberPage.getByRole('menuitem', {name: 'Archive space'})).toBeVisible();
                await memberPage.keyboard.press('Escape');
                await space.archiveFromHeader(spaceTitle);
            } finally {
                await context.close();
            }

            await page.reload();
            await scheme.expandSpacesGroup();
            await scheme.expectPermissionChecked('delete_space', true);
        } finally {
            if (granted) {
                restoreRoleAfterTest = true;
            }
        }
    });
});
