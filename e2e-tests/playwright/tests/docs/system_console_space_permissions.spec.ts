// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import type {Browser} from '@playwright/test';

import {expect, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {requestedWith, uniqueSuffix} from '../helpers/client';
import {apiRoot} from '../helpers/docs';
import {addUserToTeam, createTeam} from '../helpers/team';
import {createUser, type SeededUser} from '../helpers/user';
import {SystemSchemePermissionsPage} from '../pages/system_scheme_permissions_page';

/**
 * The team-level space permissions, as an administrator manages them in the System Console.
 *
 * These four permissions (view / create / manage / delete spaces) are core's, not the plugin's:
 * the paired core branch adds them to the system scheme's permission tree, gated on the
 * EnableDocs feature flag. They are exercised here rather than in the core PR because the
 * consequence of changing one is only observable with the Docs plugin installed — core alone
 * has no route that consults create_space.
 *
 * So this is the one assertion that neither repo can make on its own: a permission toggled in
 * core's console changes what the plugin's API allows.
 *
 * Note the two layers do not overlap. This is the *team* question — may this user make a space
 * at all. What a member may do *inside* a space is the space's own permission set, covered by
 * space_permissions.spec.ts. The group's own description says as much: "The permissions a member
 * holds inside a space are set in the space itself, not here."
 *
 * CAUTION for anyone adding to this file: unlike the space-scoped specs, these tests write the
 * *system scheme*, which is server-wide and shared by every worker. The revocation below is undone
 * in the same test's `finally`, so it cannot outlive the test that made it — but it is only undone
 * at the end, so anything running concurrently still sees the revoked window. They are safe beside
 * space_permissions.spec.ts because that suite creates its spaces as the system admin, who bypasses
 * create_space via manage_system, and probes its member against a space permission rather than this
 * team permission. Any new test here that writes the scheme must undo what it wrote, in its own
 * `finally` and keyed on whether the write actually landed; a case needing a durable non-default
 * permission should seed its own team-override scheme instead of editing the system scheme.
 */
test.describe('system console space permissions', () => {
    let teamId: string;
    let member: SeededUser;

    test.beforeEach(async ({browser, server}) => {
        const context = await browser.newContext({baseURL: server.baseURL});
        const page = await context.newPage();

        await loginAs(page, server.adminUsername, server.adminPassword);

        const team = await createTeam(page, 'docs-sysconsole');
        teamId = team.id;

        member = await createUser(page, 'docs-sysconsole-member');
        await addUserToTeam(page, teamId, member.id);

        await context.close();
    });

    // Puts create_space back after a test has revoked it. Driven from a boolean the caller
    // sets once its own revoke has landed rather than from reading the console back: a
    // non-retrying read that answered "already granted" too early would toggle a permission
    // that was never revoked, which is the corruption this exists to prevent.
    //
    // Its own context rather than the `page` fixture: this runs after a test that may have
    // failed mid-navigation, so it cannot inherit that page's state.
    const restoreCreateSpace = async (baseURL: string, browser: Browser, adminUsername: string, adminPassword: string) => {
        const context = await browser.newContext({baseURL});
        try {
            const page = await context.newPage();
            await loginAs(page, adminUsername, adminPassword);

            const scheme = new SystemSchemePermissionsPage(page);
            await scheme.goto();
            await scheme.expandSpacesGroup();
            await scheme.togglePermission('create_space');
            await scheme.save();

            // Re-read rather than trusting save()'s button signal: a write that went out but
            // whose confirmation timed out would otherwise leave the scheme unverified.
            await scheme.expectPermissionChecked('create_space', true);
        } finally {
            await context.close();
        }
    };

    // The team-level probe: whether this user may create a space at all, which is what
    // create_space governs. Not createSpace(): a refusal is the expected outcome half the time.
    const memberCanCreateSpace = async (baseURL: string, browser: Browser): Promise<number> => {
        const context = await browser.newContext({baseURL});
        try {
            const page = await context.newPage();
            await loginAs(page, member.username, member.password);

            const response = await page.request.post(`${apiRoot}/teams/${teamId}/spaces`, {
                ...requestedWith,
                data: {title: `Probe ${uniqueSuffix()}`},
            });

            return response.status();
        } finally {
            await context.close();
        }
    };

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
            // * Verify an ordinary team member starts able to create a space, so the revocation
            //   below is what changes it
            expect(await memberCanCreateSpace(server.baseURL, browser)).toBe(201);

            // # Revoke create_space from All Members and save
            await loginAs(page, server.adminUsername, server.adminPassword);
            await scheme.goto();
            await scheme.expectSpacesGroupVisible();
            await scheme.expandSpacesGroup();
            await scheme.togglePermission('create_space');
            await scheme.save();
            revoked = true;

            // * Verify the member is now refused. The 403 is the point: a console toggle changed
            //   what a plugin route allows.
            expect(await memberCanCreateSpace(server.baseURL, browser)).toBe(403);

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
                await restoreCreateSpace(server.baseURL, browser, server.adminUsername, server.adminPassword);
            }
        }
    });
});
