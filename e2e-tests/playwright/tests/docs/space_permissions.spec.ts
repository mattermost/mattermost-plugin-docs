// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import type {Browser, Page} from '@playwright/test';

import {expect, newContext, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {requestedWith, uniqueSuffix} from '../helpers/client';
import {addSpaceMember, createPage, createSpace} from '../helpers/docs';
import {demoteToGuest, ensureGuestAccountsEnabled, setGuestAccountsEnabled} from '../helpers/guest';
import {readState} from '../helpers/state';
import {addUserToTeam, createTeam, promoteToTeamAdmin, removeUserFromTeam, setTeamAdminSpaceTiers} from '../helpers/team';
import {createUser, type SeededUser, suppressOnboarding} from '../helpers/user';
import {SpacePage} from '../pages/space_page';
import {SpaceSettingsModalPage} from '../pages/space_settings_modal_page';
import {SpacesSidebarPage} from '../pages/spaces_sidebar_page';

/**
 * The permissions surface, driven through the browser.
 *
 * What only this suite can prove: that the toggle a space admin moves in the UI reaches
 * the server, that what comes back is what the UI then shows, and that the change lands
 * on another member's authority. The handler tests assert the same endpoints far more
 * cheaply against a mocked plugin API, and the Jest suite asserts the same rendering
 * against mocked reads — neither runs the UI against a real server, so neither can catch a
 * control wired to the wrong field, a write that never reads back, or a lock that only
 * looks locked.
 *
 * The APIs are used only to seed fixtures whose creation is not under test. Permission outcomes
 * are asserted through the browser: an E2E case must prove what a person can see and complete,
 * not substitute a direct request for the UI journey it claims to cover. Route enforcement itself
 * belongs to the Go handler and service suites.
 */
test.describe('space permissions', () => {
    let teamName: string;
    let teamId: string;
    let spaceTitle: string;
    let spaceId: string;
    let adminId: string;
    let member: SeededUser;

    // In the team, deliberately not in the space. The actor the open/private distinction
    // exists for, and the one this suite had no persona for.
    let nonMember: SeededUser;

    // Per-test, not shared: each test mutates the space's permission state, and a shared
    // space would make them order-dependent.
    test.beforeEach(async ({browser, server}) => {
        const context = await newContext(browser, {baseURL: server.baseURL});
        const page = await context.newPage();

        await loginAs(page, server.adminUsername, server.adminPassword);

        spaceTitle = `PW Perms ${uniqueSuffix()}`;

        const team = await createTeam(page, 'docs-perms');
        teamName = team.name;
        teamId = team.id;

        // The admin authors from the browser later, so they need the team too.
        const admin = await page.request.get('/api/v4/users/me', requestedWith);
        adminId = (await admin.json() as {id: string}).id;
        await addUserToTeam(page, teamId, adminId);

        // The admin drives the browser in most of these tests and is not created by createUser, so
        // the onboarding overlay it suppresses has to be suppressed here too.
        await suppressOnboarding(page, adminId);

        member = await createUser(page, 'docs-perms-member');
        await addUserToTeam(page, teamId, member.id);

        // Added to the team but never to the space, so their only route in is whatever the
        // space's view access grants a team member.
        nonMember = await createUser(page, 'docs-perms-nonmember');
        await addUserToTeam(page, teamId, nonMember.id);

        // Created over the API: the authoring journey is covered by its own spec, and this
        // one is about what happens to a space that already exists.
        const space = await createSpace(page, teamId, spaceTitle);
        spaceId = space.id;
        await addSpaceMember(page, spaceId, member.id);

        await context.close();
    });

    // Whether the space view offers the member page creation. Kept as a browser helper so both
    // sides of a default toggle are observed at the affordance where the person would act.
    const memberIsOfferedAuthoring = async (baseURL: string, browser: Browser): Promise<boolean> => {
        const context = await newContext(browser, {baseURL});
        try {
            const probePage = await context.newPage();
            await loginAs(probePage, member.username, member.password);

            const sidebar = new SpacesSidebarPage(probePage);
            const space = new SpacePage(probePage);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await space.expectOpen(spaceTitle);

            return await space.addPageButton.isVisible();
        } finally {
            await context.close();
        }
    };

    const authorInBrowser = async (page: Page, user: SeededUser, titlePrefix: string, canEdit = true): Promise<string> => {
        const sidebar = new SpacesSidebarPage(page);
        const space = new SpacePage(page);
        const title = `${titlePrefix} ${uniqueSuffix()}`;

        await loginAs(page, user.username, user.password);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await space.expectOpen(spaceTitle);
        await space.addPage(title);
        await space.expectDraftRoute();
        await space.writeBody(`Authored by ${user.username}.`);
        await space.expectDraftSaved();
        await space.publish();
        await space.expectPublished(canEdit);

        return title;
    };

    const expectRosterEntry = async (
        baseURL: string,
        browser: Browser,
        adminUsername: string,
        adminPassword: string,
        username: string,
        listed: boolean,
        autoJoined = false,
    ) => {
        const context = await newContext(browser, {baseURL});
        try {
            const adminPage = await context.newPage();
            await loginAs(adminPage, adminUsername, adminPassword);
            const sidebar = new SpacesSidebarPage(adminPage);
            const settings = new SpaceSettingsModalPage(adminPage);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.openPermissions();
            await settings.expectMemberListed(username, listed);
            if (listed && autoJoined) {
                await settings.expectAutoJoinedMarker();
            }
        } finally {
            await context.close();
        }
    };

    /**
     * @objective Revoking a space default in the UI removes that authority from a member.
     *
     * The whole chain in one test: the admin's click, the server's answer, the UI's
     * re-read, and a different user's permission changing as a result.
     *
     * @precondition A space seeded with the contribute default (which grants create_page)
     * and a second team member added to it.
     */
    test('revoking a space default removes a member\'s authority', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        // * Verify the member can complete authoring in the browser before the revocation.
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            await authorInBrowser(await memberContext.newPage(), member, 'Before revoke');
        } finally {
            await memberContext.close();
        }

        // # Open the space's permissions as its admin
        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // * Verify the UI shows the seeded contribute default
        await settings.expectPermission('Create pages', true);
        await settings.expectPermission('Delete any page', false);

        // # Revoke page creation
        await settings.togglePermission('Create pages');

        // * Verify the member is no longer offered the authoring journey.
        expect(await memberIsOfferedAuthoring(server.baseURL, browser)).toBe(false);
    });

    /**
     * @objective A permission change survives reopening the modal.
     *
     * Catches a control that writes successfully but renders from local state: the second
     * open re-reads from the server, so a stale write shows up as the old value.
     */
    test('every space-default permission can be changed and reads back', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);
        const labels = ['Create pages', 'Comment on pages', 'Edit pages', 'Delete own pages', 'Delete any page'];

        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // # Flip every control, recording the server-seeded starting values rather than
        // hard-coding a preset. A wiring mutation between any label and permission id now fails.
        const initial = new Map<string, boolean>();
        for (const label of labels) {
            initial.set(label, await settings.permission(label).isChecked());
            await settings.togglePermission(label);
        }

        // # Reopen the modal so its state comes from a fresh read
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // * Verify every flipped value survived the round trip.
        for (const label of labels) {
            await settings.expectPermission(label, !initial.get(label));
        }

        // # Flip every control back and force a second fresh read. This exercises both grant
        // and revoke paths for all five controls, not merely five variations of one direction.
        for (const label of labels) {
            await settings.togglePermission(label);
        }
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        for (const label of labels) {
            await settings.expectPermission(label, initial.get(label) ?? false);
        }
    });

    /**
     * @objective A space admin can flip the space to Private, and it reads back.
     *
     * The option was inert scaffolding ("Coming soon") until the permissions work landed,
     * so this pins that it is now a real control.
     */
    test('view access can be changed in both directions and reads back', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // * Verify a space created without an explicit view access starts public
        await settings.expectAccess('Public');

        // # Make it private
        await settings.chooseAccess('Private');

        // # Reopen so the value comes from the server rather than from the click
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // * Verify the space is private
        await settings.expectAccess('Private');

        // # Restore Public and re-read again. This catches a one-way implementation that can
        // privatize a space but cannot restore the team-wide read grant.
        await settings.chooseAccess('Public');
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.expectAccess('Public');
    });

    /**
     * @objective An ordinary member is not offered the space's settings surface at all.
     *
     * The settings entry is gated on the same authority the member-list route requires
     * (requireSpaceManage), so a plain member cannot reach the permissions tab in the first
     * place — the read behind the gate answers 403 for them. Worth pinning in a browser
     * because it is the menu, not the route, that decides what a member is offered: an
     * ungated entry would put them in front of controls that could only fail.
     *
     * The locked rendering of those controls, for an actor who can open the tab but not
     * administer the space, is covered where it belongs — permissions_tab.test.tsx.
     */
    test('an ordinary member is not offered space settings', {tag: ['@docs', '@permissions']}, async ({page}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        // # Open the space as an ordinary member and open its header menu
        await loginAs(page, member.username, member.password);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openSpaceHeaderMenu(spaceTitle);

        // * Verify the menu opened, so the absence below is a real absence rather than a
        //   menu that never rendered
        await expect(page.getByRole('menuitem', {name: 'Copy link'})).toBeVisible();

        // * Verify neither admin-only entry is offered
        await expect(page.getByRole('menuitem', {name: 'Space settings'})).toBeHidden();
        await expect(page.getByRole('menuitem', {name: 'Archive space'})).toBeHidden();
    });

    /**
     * @objective A per-member grant made in the matrix changes that member's authority.
     *
     * The per-principal half of the Confluence-style matrix, end to end: the space default
     * withholds delete_page from everyone, the admin grants it to one member in that
     * member's own row, and the member's authority changes while the space default does
     * not. Proves the row writes granted_permissions for the right principal rather than
     * editing the space-wide set.
     */
    test('granting a member a permission in their own row changes their authority', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        // # Seed a page the member did not author, so the probe below needs delete_page
        //   rather than delete_own_page
        const adminContext = await newContext(browser, {baseURL: server.baseURL});
        const seededTitle = `Seed ${uniqueSuffix()}`;
        try {
            const adminPage = await adminContext.newPage();
            await loginAs(adminPage, server.adminUsername, server.adminPassword);
            await createPage(adminPage, spaceId, seededTitle);
        } finally {
            await adminContext.close();
        }

        // * Verify the member is not offered deletion before the grant.
        const beforeContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await beforeContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.expectPageAction(seededTitle, 'Delete page', false);
        } finally {
            await beforeContext.close();
        }

        // # Open the space's permissions as its admin
        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // * Verify the space default withholds it, and so does the member's own row
        await settings.expectPermission('Delete any page', false);
        await settings.expectMemberPermission(member.id, 'delete_page', false);

        // # Grant it to that member alone
        await settings.toggleMemberPermission(member.id, 'delete_page');

        // * Verify the space-wide default was not what changed
        await settings.expectPermission('Delete any page', false);

        // * Verify it survives a reopen, so the grant reached the server rather than
        //   living in the modal's local state
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.expectMemberPermission(member.id, 'delete_page', true);

        // * Verify that member can now complete the real page-menu action and confirmation.
        const afterContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await afterContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.deletePage(seededTitle);
        } finally {
            await afterContext.close();
        }
    });

    /**
     * @objective Every per-member matrix cell writes the permission named by its column.
     *
     * The delete-any test above proves one grant changes real authority. This matrix test covers
     * the remaining wiring cheaply while still crossing the browser/server boundary twice: every
     * cell is granted, freshly read, revoked, and freshly read again. Swapping any two ids, dropping
     * a cell from the replace payload, or making either direction local-only is observable.
     */
    test('every per-member permission can be granted and revoked', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);
        const permissions = ['create_page', 'comment_page', 'edit_page', 'delete_own_page', 'delete_page', 'admin_space'];

        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        for (const permission of permissions) {
            await settings.expectMemberPermission(member.id, permission, false);
            await settings.toggleMemberPermission(member.id, permission);
        }

        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        for (const permission of permissions) {
            await settings.expectMemberPermission(member.id, permission, true);
        }

        for (const permission of permissions) {
            await settings.toggleMemberPermission(member.id, permission);
        }

        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        for (const permission of permissions) {
            await settings.expectMemberPermission(member.id, permission, false);
        }
    });

    /**
     * @objective A page action the space default withholds is not offered on the page.
     *
     * The contribute default grants delete_own_page but not delete_page, so a member may
     * delete what they wrote and nothing else. The page menu must use the resolved permission
     * set too, so it does not offer an action the server will refuse.
     */
    test('a member is not offered page actions the space default withholds', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const space = new SpacePage(page);
        const seededTitle = `Seed ${uniqueSuffix()}`;

        // # Seed a page the member did not author, so removing it needs delete_page
        const adminContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const adminPage = await adminContext.newPage();
            await loginAs(adminPage, server.adminUsername, server.adminPassword);
            await createPage(adminPage, spaceId, seededTitle);
        } finally {
            await adminContext.close();
        }

        // # Open that page as the member and open its menu
        await loginAs(page, member.username, member.password);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await space.openPageFromTree(seededTitle);
        await space.expectPageAction(seededTitle, 'Delete page', false);
    });

    /**
     * @objective The default edit and delete-own grants control the complete page journeys.
     *
     * This deliberately performs the actions before revocation and checks their UI disappears
     * afterward. An absence-only test would also pass if Edit, Rename, or Delete were broken for
     * everyone; the positive half makes each negative assertion discriminating.
     */
    test('edit, rename, and delete-own actions follow the space defaults', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const seededTitle = `Admin page ${uniqueSuffix()}`;
        const renamedTitle = `Member renamed ${uniqueSuffix()}`;

        // # Seed somebody else's page; page fixture creation is not the behavior under test.
        const seedContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const seedPage = await seedContext.newPage();
            await loginAs(seedPage, server.adminUsername, server.adminPassword);
            await createPage(seedPage, spaceId, seededTitle, 'Initial body');
        } finally {
            await seedContext.close();
        }

        // # As the ordinary member, create and delete an owned page through the UI.
        const ownedTitle = await authorInBrowser(page, member, 'Owned page');
        const space = new SpacePage(page);
        await space.deletePage(ownedTitle);

        // # Complete both edit surfaces on somebody else's page while edit_page is granted.
        await space.openPageFromTree(seededTitle);
        await expect(space.editButton).toBeVisible();
        await space.editButton.click();
        await space.expectPublishedPageEditing();
        await space.writePublishedBody(' Edited by the member.');
        await space.expectDraftSaved();
        await space.update();
        await space.expectPublished();
        await space.renamePage(seededTitle, renamedTitle);

        // # Revoke Edit pages and Delete own pages through Space Settings.
        const adminContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const adminPage = await adminContext.newPage();
            await loginAs(adminPage, server.adminUsername, server.adminPassword);
            const adminSidebar = new SpacesSidebarPage(adminPage);
            const settings = new SpaceSettingsModalPage(adminPage);
            await adminSidebar.goto(teamName);
            await adminSidebar.openSpace(spaceTitle);
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.openPermissions();
            await settings.expectPermission('Edit pages', true);
            await settings.expectPermission('Delete own pages', true);
            await settings.togglePermission('Edit pages');
            await settings.togglePermission('Delete own pages');
        } finally {
            await adminContext.close();
        }

        // * A fresh member session can still author, but is offered neither editing nor deletion
        // of the newly-owned page after publishing it.
        const afterContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const afterPage = await afterContext.newPage();
            const afterTitle = await authorInBrowser(afterPage, member, 'After revoke', false);
            const afterSpace = new SpacePage(afterPage);
            await expect(afterSpace.editButton).toBeHidden();
            await afterSpace.expectPageAction(afterTitle, 'Rename', false);
            await afterSpace.expectPageAction(afterTitle, 'Delete page', false);
        } finally {
            await afterContext.close();
        }

    });

    /**
     * @objective A real (non-system) space administrator can use every space-admin surface.
     *
     * The system administrator is intentionally only the grantor here. The promoted user proves
     * admin_space itself unlocks settings, space metadata, access/default controls, member
     * add/remove, and self-leave; revocation proves those capabilities are not coming from their
     * ordinary team membership.
     */
    test('a space administrator can manage the space and membership, then leave', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);
        const candidate = await createUser(page, 'docs-perms-candidate');
        await addUserToTeam(page, teamId, candidate.id);

        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'admin_space');
        await settings.close();

        // * The promoted ordinary user receives the administrator-only surfaces in a fresh session.
        const promotedContext = await newContext(browser, {baseURL: server.baseURL});
        const renamedSpace = `Renamed ${uniqueSuffix()}`;
        try {
            const promotedPage = await promotedContext.newPage();
            await loginAs(promotedPage, member.username, member.password);
            const promotedSidebar = new SpacesSidebarPage(promotedPage);
            const promotedSpace = new SpacePage(promotedPage);
            const promotedSettings = new SpaceSettingsModalPage(promotedPage);
            await promotedSidebar.goto(teamName);
            await promotedSidebar.openSpace(spaceTitle);
            await promotedSettings.openSpaceHeaderMenu(spaceTitle);
            await expect(promotedPage.getByRole('menuitem', {name: 'Space settings'})).toBeVisible();
            await expect(promotedPage.getByRole('menuitem', {name: 'Archive space'})).toBeVisible();
            await promotedPage.keyboard.press('Escape');

            // # Rename the space using its real Info-tab save flow.
            await promotedSettings.openFromSpaceHeader(spaceTitle);
            await promotedSettings.renameSpace(renamedSpace);
            await promotedSettings.openPermissions();
            await promotedSettings.expectAccessEnabled('Private', true);
            await promotedSettings.expectPermissionEnabled('Create pages', true);

            // # Add and remove another team member through the roster UI.
            await promotedSettings.addMember(candidate.username);
            await promotedSettings.expectMemberPermissionEnabled(candidate.id, 'admin_space', true);
            await promotedSettings.removeMember(candidate.username);
            await promotedSettings.close();
            await promotedSpace.expectOpen(renamedSpace);
        } finally {
            await promotedContext.close();
        }
        spaceTitle = renamedSpace;

        // # Revoke admin_space and prove a fresh session loses both privileged entries.
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'admin_space');
        await settings.close();

        const demotedContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const demotedPage = await demotedContext.newPage();
            await loginAs(demotedPage, member.username, member.password);
            const demotedSidebar = new SpacesSidebarPage(demotedPage);
            const demotedSettings = new SpaceSettingsModalPage(demotedPage);
            await demotedSidebar.goto(teamName);
            await demotedSidebar.openSpace(spaceTitle);
            await demotedSettings.openSpaceHeaderMenu(spaceTitle);
            await expect(demotedPage.getByRole('menuitem', {name: 'Space settings'})).toBeHidden();
            await expect(demotedPage.getByRole('menuitem', {name: 'Archive space'})).toBeHidden();
        } finally {
            await demotedContext.close();
        }

        // # Promote once more, then leave from the user's own roster row. The system-admin creator
        // remains, so both last-admin and last-member invariants permit this positive path.
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'admin_space');
        await settings.close();

        const leavingContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const leavingPage = await leavingContext.newPage();
            await loginAs(leavingPage, member.username, member.password);
            const leavingSidebar = new SpacesSidebarPage(leavingPage);
            const leavingSettings = new SpaceSettingsModalPage(leavingPage);
            await leavingSidebar.goto(teamName);
            await leavingSidebar.openSpace(spaceTitle);
            await leavingSettings.openFromSpaceHeader(spaceTitle);
            await leavingSettings.openPermissions();
            await leavingSettings.leaveFromOwnRow(member.username);
            await expect(leavingPage).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
        } finally {
            await leavingContext.close();
        }
    });

    /** @objective The UI reports and preserves the last-space-administrator invariant. */
    test('the last space administrator cannot demote themselves or leave', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // # Attempt to remove admin_space from the creator's own row.
        const ownAdmin = settings.memberPermission(adminId, 'admin_space');
        await expect(ownAdmin).toBeChecked();
        await ownAdmin.click();
        await expect(page.getByRole('alert')).toContainText('A space must keep at least one administrator.');
        await expect(ownAdmin).toBeChecked();

        // # Attempt to leave while the other member is ordinary rather than an administrator.
        await settings.requestLeaveFromOwnRow(server.adminUsername);
        const leaveAlert = page.getByRole('alert').filter({hasText: `Unable to leave ${spaceTitle}`});
        await expect(leaveAlert).toContainText(`Unable to leave ${spaceTitle}`);
        await expect(leaveAlert).toContainText('Make another member an administrator before you leave');
        await expect(settings.dialog).toBeVisible();
    });

    /**
     * @objective A team manage_space holder can manage metadata and the roster, but not the
     * stricter space-wide permission controls or archive action.
     *
     * The actor is deliberately not a space member. Their authority comes only from an isolated
     * team scheme, so this fails if the UI substitutes membership, admin_space, or delete_space for
     * the manage tier.
     */
    test('a team manager has only the manage-space UI tier', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        const manager = await createUser(page, 'docs-perms-manager');
        const candidate = await createUser(page, 'docs-perms-manager-add');
        await addUserToTeam(page, teamId, manager.id);
        await addUserToTeam(page, teamId, candidate.id);
        await setTeamAdminSpaceTiers(page, teamId, {manage: true, delete: false});
        await promoteToTeamAdmin(page, teamId, manager.id);

        const context = await newContext(browser, {baseURL: server.baseURL});
        try {
            const managerPage = await context.newPage();
            await loginAs(managerPage, manager.username, manager.password);
            const sidebar = new SpacesSidebarPage(managerPage);
            const space = new SpacePage(managerPage);
            const settings = new SpaceSettingsModalPage(managerPage);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);

            // * The two team tiers are independent in the header.
            await settings.openSpaceHeaderMenu(spaceTitle);
            await expect(managerPage.getByRole('menuitem', {name: 'Space settings'})).toBeVisible();
            await expect(managerPage.getByRole('menuitem', {name: 'Archive space'})).toBeHidden();
            await managerPage.keyboard.press('Escape');

            // # Rename is a manage-tier operation and must complete.
            const renamed = `Managed ${uniqueSuffix()}`;
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.renameSpace(renamed);
            await settings.openPermissions();

            // * Space-wide controls require admin_space even though this actor can manage people.
            await settings.expectAccessEnabled('Private', false);
            await settings.expectPermissionEnabled('Create pages', false);

            // * Ordinary member grants and roster changes are live, but promotion is not.
            await settings.expectMemberPermissionEnabled(member.id, 'create_page', true);
            await settings.expectMemberPermissionEnabled(member.id, 'admin_space', false);
            await settings.toggleMemberPermission(member.id, 'comment_page');
            await settings.addMember(candidate.username);
            await settings.expectMemberPermissionEnabled(candidate.id, 'create_page', true);
            await settings.removeMember(candidate.username);
            await settings.close();
            await space.expectOpen(renamed);
        } finally {
            await context.close();
        }
    });

    /** @objective delete_space unlocks archive without accidentally unlocking manage_space. */
    test('a delete-only team administrator can archive but cannot open settings', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        const deleter = await createUser(page, 'docs-perms-deleter');
        await addUserToTeam(page, teamId, deleter.id);
        await setTeamAdminSpaceTiers(page, teamId, {manage: false, delete: true});
        await promoteToTeamAdmin(page, teamId, deleter.id);

        const context = await newContext(browser, {baseURL: server.baseURL});
        try {
            const deleterPage = await context.newPage();
            await loginAs(deleterPage, deleter.username, deleter.password);
            const sidebar = new SpacesSidebarPage(deleterPage);
            const space = new SpacePage(deleterPage);
            const settings = new SpaceSettingsModalPage(deleterPage);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);

            await settings.openSpaceHeaderMenu(spaceTitle);
            await expect(deleterPage.getByRole('menuitem', {name: 'Space settings'})).toBeHidden();
            await expect(deleterPage.getByRole('menuitem', {name: 'Archive space'})).toBeVisible();
            await deleterPage.keyboard.press('Escape');

            // # Complete the destructive UI journey, including confirmation.
            await space.archiveFromHeader(spaceTitle);
        } finally {
            await context.close();
        }
    });

    /** @objective Removing a space member from the team removes all browser-visible access. */
    test('a former team member cannot discover or deep-link the space', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        await removeUserFromTeam(page, teamId, member.id);

        await loginAs(page, member.username, member.password);
        await page.goto(`/${teamName}/spaces/${spaceId}`);
        await expect(page).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
        await expect(page.getByRole('main').getByRole('button', {name: spaceTitle, exact: true})).toBeHidden();
    });

    /**
     * A team member who is not a member of the space, on a space that is open.
     *
     * This is the persona the open/private distinction exists for, and the suite had no actor
     * for it. An open space is readable by any team member through the server's
     * read_public_channel fall-through. When the space default grants the requested authoring,
     * the UI explicitly joins such a reader immediately before its first draft write — so the
     * product's claim is that authoring is one click away and the join happens on the way to it.
     *
     * The UI derives the authoring affordance from the open space's default permissions and calls
     * the server's self-join route before writing. These tests keep both halves of that flow aligned.
     */
    test.describe('a non-member of an open space', () => {
        // Drives the non-member through authoring in the browser — the flow that is supposed
        // to turn a reader into a member. Returns the title it published.
        const authorAsNonMember = async (page: Page): Promise<string> => {
            return authorInBrowser(page, nonMember, 'NonMember');
        };

        /**
         * @objective An open space offers a non-member the authoring its default grants.
         *
         * The discovery half is asserted first to distinguish the open-space read path from
         * the authoring affordance supplied by the space default.
         */
        test('is offered the authoring the space default grants', {tag: ['@docs', '@permissions']}, async ({page}) => {
            const sidebar = new SpacesSidebarPage(page);
            const space = new SpacePage(page);

            // # Open the space as someone who was never added to it
            await loginAs(page, nonMember.username, nonMember.password);
            await sidebar.goto(teamName);

            // * Verify an open space is discoverable without membership
            await sidebar.expectSpaceListed(spaceTitle);

            await sidebar.openSpace(spaceTitle);

            // * Verify it opens, so the caller can read it
            await space.expectOpen(spaceTitle);

            // * Verify authoring is offered, because the space's contribute default grants
            //   create_page to anyone who is a member — which writing here is meant to make
            //   them.
            await expect(space.addPageButton).toBeVisible();
        });

        /**
         * @objective Authoring in an open space is what joins the author to it.
         *
         * Reading must not join — a space someone merely opened should not acquire them as a
         * member — so the pre-assertion is as much the point as the post-assertion.
         */
        test('becomes a member by authoring, not by reading', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
            const sidebar = new SpacesSidebarPage(page);
            const space = new SpacePage(page);

            // # Read the space without writing anything
            await loginAs(page, nonMember.username, nonMember.password);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await space.expectOpen(spaceTitle);

            // * Verify reading alone did not add them to the visible roster.
            await expectRosterEntry(
                server.baseURL,
                browser,
                server.adminUsername,
                server.adminPassword,
                nonMember.username,
                false,
            );

            // # Author a page
            await authorAsNonMember(page);

            // * Verify authoring did, including the provenance shown to an administrator.
            await expectRosterEntry(
                server.baseURL,
                browser,
                server.adminUsername,
                server.adminPassword,
                nonMember.username,
                true,
                true,
            );
        });

        /**
         * @objective Making a space private withdraws the access it granted openly.
         *
         * Confluence parity, which is what this models: space permissions are additive grants,
         * and withdrawing a broad grant immediately drops everyone who held access only
         * through it while an individual grant survives untouched. Here the broad grant is
         * view_access=open and the individual grant is a deliberate invitation, so the author
         * who joined by writing goes and the invited member stays.
         *
         * Without this, "make this space private" leaves every open-access author inside it
         * with their write authority intact, and the only remedy is removing them one at a
         * time from the roster.
         */
        test('loses access when the space is made private, while an invited member keeps it', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
            const settings = new SpaceSettingsModalPage(page);
            const sidebar = new SpacesSidebarPage(page);

            // # Join by authoring, so the space holds one member of each kind. In its own
            //   context: every actor in this suite gets one, so the admin below drives a
            //   browser that was never signed in as anyone else.
            const authorContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                await authorAsNonMember(await authorContext.newPage());
            } finally {
                await authorContext.close();
            }

            // # Make the space private, as its admin, through the UI
            await loginAs(page, server.adminUsername, server.adminPassword);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.openPermissions();
            await settings.chooseAccess('Private');
            await settings.close();

            // * Verify the deliberately invited member still sees and opens the space.
            const invitedContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const invitedPage = await invitedContext.newPage();
                await loginAs(invitedPage, member.username, member.password);
                const invitedSidebar = new SpacesSidebarPage(invitedPage);
                const invitedSpace = new SpacePage(invitedPage);
                await invitedSidebar.goto(teamName);
                await invitedSidebar.expectSpaceListed(spaceTitle);
                await invitedSidebar.openSpace(spaceTitle);
                await invitedSpace.expectOpen(spaceTitle);
            } finally {
                await invitedContext.close();
            }

            // * Verify the space is no longer even discoverable to them
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const nonMemberPage = await context.newPage();
                await loginAs(nonMemberPage, nonMember.username, nonMember.password);

                const nonMemberSidebar = new SpacesSidebarPage(nonMemberPage);
                await nonMemberSidebar.goto(teamName);
                await expect(nonMemberSidebar.spaceLink(spaceTitle)).toBeHidden();

                // A known deep link must not bypass discovery.
                await nonMemberPage.goto(`/${teamName}/spaces/${spaceId}`);
                await expect(nonMemberPage).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
                await expect(nonMemberPage.getByRole('main').getByRole('button', {name: spaceTitle, exact: true})).toBeHidden();
            } finally {
                await context.close();
            }
        });
    });

    /**
     * The remaining persona on the same space the tests above use: a guest.
     *
     * The distinction guests carry is that the space's own default permission set does not
     * reach them — the server pins a guest to read_page whatever the default grants, so a
     * guest and an ordinary member on the *identical* space differ in what they may do. That
     * contrast is the point of seeding them here rather than on a space of their own.
     *
     * The guest API invariants (read 200, create 403, edit 403, permission grant 400) are
     * asserted far more cheaply by the handler tests. What is left for a browser, and asserted
     * below, is that the UI agrees with them: a guest who may not author is not shown the
     * affordances for it.
     */
    test.describe('a guest', () => {
        let guest: SeededUser;
        let guestNonMember: SeededUser;

        // Held for the whole describe. Toggling guest support off after every case can deactivate
        // the next case's newly-demoted actors while config propagation is still in flight.
        let guestAccountsWereEnabled = true;

        test.beforeAll(async ({browser}) => {
            const server = readState();
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const page = await context.newPage();
                await loginAs(page, server.adminUsername, server.adminPassword);
                guestAccountsWereEnabled = await ensureGuestAccountsEnabled(page);
            } finally {
                await context.close();
            }
        });

        test.beforeEach(async ({browser, server}) => {
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const page = await context.newPage();
                await loginAs(page, server.adminUsername, server.adminPassword);

                guest = await createUser(page, 'docs-perms-guest');
                guestNonMember = await createUser(page, 'docs-perms-guest-outsider');
                await addUserToTeam(page, teamId, guest.id);
                await addUserToTeam(page, teamId, guestNonMember.id);
                await addSpaceMember(page, spaceId, guest.id);

                // Last, so the demotion converts the memberships seeded above.
                await demoteToGuest(page, guest.id);
                await demoteToGuest(page, guestNonMember.id);
            } finally {
                await context.close();
            }
        });

        test.afterAll(async ({browser}) => {
            if (guestAccountsWereEnabled) {
                return;
            }

            const server = readState();
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const page = await context.newPage();
                await loginAs(page, server.adminUsername, server.adminPassword);
                await setGuestAccountsEnabled(page, false);
            } finally {
                await context.close();
            }
        });

        /**
         * @objective A guest can open and read a space it belongs to.
         *
         * The read half of the guest invariant, through the browser: the demotion converts the
         * space membership rather than removing it, so the space still opens.
         */
        test('can open and read the space it belongs to', {tag: ['@docs', '@permissions']}, async ({page}) => {
            const sidebar = new SpacesSidebarPage(page);
            const space = new SpacePage(page);

            // # Open the space as the guest
            await loginAs(page, guest.username, guest.password);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);

            // * Verify the space renders for a guest
            await space.expectOpen(spaceTitle);
        });

        /**
         * @objective A guest's row in the permission matrix is rendered locked.
         *
         * Driven by the admin, because they are the only persona who can open Space
         * Settings at all — a guest never sees this surface. The server refuses every
         * grant to a guest (app.space.member.guest_not_assignable), so the row must say
         * so rather than offer toggles whose write can only fail.
         *
         * The member's row alongside it is the control: it proves the lock is the guest's
         * standing rather than the whole matrix being disabled.
         */
        test('has a locked row in the permission matrix', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
            const sidebar = new SpacesSidebarPage(page);
            const settings = new SpaceSettingsModalPage(page);

            // # Open the space's permissions as its admin
            await loginAs(page, server.adminUsername, server.adminPassword);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.openPermissions();

            // * Verify an ordinary member's row is editable, so the guest's lock below is
            //   about the guest and not about the matrix being read-only
            await expect(settings.memberPermission(member.id, 'edit_page')).toBeEnabled();

            // * Verify the guest's row is offered but not editable
            await expect(settings.memberPermission(guest.id, 'edit_page')).toBeVisible();
            await expect(settings.memberPermission(guest.id, 'edit_page')).toBeDisabled();
        });

        /**
         * @objective A guest is not offered page authoring it cannot perform.
         *
         * The space view withholds the control that would create a page: page creation is gated on
         * the caller's own create_page, resolved by the same single-space read the view already
         * performs. Server enforcement is pinned by the Go suites rather than duplicated here.
         *
         * Not guest-specific — a member of a space whose default has had create_page revoked
         * is in the same position, which is why the gate keys on the permission rather than on
         * guest standing.
         */
        test('is not offered page authoring it cannot perform', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
            // * Verify the member IS offered it on this same space, so the guest's absence
            //   below is the gate discriminating rather than the control being gone for
            //   everyone — which a guest-only assertion cannot tell apart.
            const memberContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await memberContext.newPage();
                await loginAs(memberPage, member.username, member.password);
                await new SpacesSidebarPage(memberPage).goto(teamName);
                await new SpacesSidebarPage(memberPage).openSpace(spaceTitle);

                const memberSpace = new SpacePage(memberPage);
                await memberSpace.expectOpen(spaceTitle);
                await expect(memberSpace.addPageButton).toBeVisible();
            } finally {
                await memberContext.close();
            }

            const sidebar = new SpacesSidebarPage(page);
            const space = new SpacePage(page);

            // # Open the same space as the guest
            await loginAs(page, guest.username, guest.password);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await space.expectOpen(spaceTitle);

            // * Verify page creation is not offered
            await expect(space.addPageButton).toBeHidden();
        });

        test('cannot discover an open space without an invitation', {tag: ['@docs', '@permissions']}, async ({page}) => {
            const sidebar = new SpacesSidebarPage(page);

            await loginAs(page, guestNonMember.username, guestNonMember.password);
            await sidebar.goto(teamName);

            // Guests do not receive the ordinary team member's open-space fall-through.
            await expect(sidebar.spaceLink(spaceTitle)).toBeHidden();
            await page.goto(`/${teamName}/spaces/${spaceId}`);
            await expect(page).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
        });
    });
});
