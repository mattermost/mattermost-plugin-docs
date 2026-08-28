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
import {pluginId} from '../helpers/preflight';
import {readState} from '../helpers/state';
import {addUserToTeam, createTeam, promoteToTeamAdmin, removeUserFromTeam, setTeamAdminSpaceTiers} from '../helpers/team';
import {createUser, type SeededUser, suppressOnboarding} from '../helpers/user';
import {ShareSpaceModalPage} from '../pages/share_space_modal_page';
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
    // sides of a default toggle are observed at the affordance where the person would act. The
    // assertion runs inside the helper's fresh context so it retries against the locator instead
    // of taking a single non-retrying snapshot.
    const expectMemberOfferedAuthoring = async (baseURL: string, browser: Browser, offered: boolean): Promise<void> => {
        const context = await newContext(browser, {baseURL});
        try {
            const probePage = await context.newPage();
            await loginAs(probePage, member.username, member.password);

            const sidebar = new SpacesSidebarPage(probePage);
            const space = new SpacePage(probePage);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await space.expectOpen(spaceTitle);

            if (offered) {
                await expect(space.addPageButton).toBeVisible();
            } else {
                await expect(space.addPageButton).toBeHidden();
            }
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

    const setSpaceDefaultPermissions = async (page: Page, permissions: string[]) => {
        const response = await page.request.put(`/plugins/${pluginId}/api/v1/spaces/${spaceId}/default-permissions`, {
            ...requestedWith,
            data: {default_permissions: permissions},
        });
        expect(response.ok()).toBe(true);
    };

    const expectPageDeleted = async (
        baseURL: string,
        browser: Browser,
        username: string,
        password: string,
        pageId: string,
    ) => {
        const context = await newContext(browser, {baseURL});
        try {
            const verifyPage = await context.newPage();
            await loginAs(verifyPage, username, password);
            const response = await verifyPage.request.get(
                `/plugins/${pluginId}/api/v1/spaces/${spaceId}/pages/${pageId}`,
                requestedWith,
            );
            expect(response.status()).toBe(404);
        } finally {
            await context.close();
        }
    };

    const expectNonMemberAccess = async (baseURL: string, browser: Browser, accessible: boolean) => {
        const context = await newContext(browser, {baseURL});
        try {
            const probePage = await context.newPage();
            await loginAs(probePage, nonMember.username, nonMember.password);
            const sidebar = new SpacesSidebarPage(probePage);
            await sidebar.goto(teamName);

            if (accessible) {
                await sidebar.expectSpaceListed(spaceTitle);
                await sidebar.openSpace(spaceTitle);
                await new SpacePage(probePage).expectOpen(spaceTitle);
            } else {
                await expect(sidebar.spaceLink(spaceTitle)).toBeHidden();
                await probePage.goto(`/${teamName}/spaces/${spaceId}`);
                await expect(probePage).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
            }
        } finally {
            await context.close();
        }
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
        const staleContext = await newContext(browser, {baseURL: server.baseURL});

        try {
            // * Verify the member can complete authoring in the browser before the revocation.
            const memberContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                await authorInBrowser(await memberContext.newPage(), member, 'Before revoke');
            } finally {
                await memberContext.close();
            }

            // # Start a second page through the real authoring UI. Suppress WebSocket delivery in
            // this one deliberately stale client so the already-rendered Publish control remains
            // available after the administrator changes authority.
            const stalePage = await staleContext.newPage();
            await stalePage.routeWebSocket(/\/api\/v4\/websocket/, () => {});
            await loginAs(stalePage, member.username, member.password);
            const staleSidebar = new SpacesSidebarPage(stalePage);
            const staleSpace = new SpacePage(stalePage);
            await staleSidebar.goto(teamName);
            await staleSidebar.openSpace(spaceTitle);
            await staleSpace.addPage(`Denied publish ${uniqueSuffix()}`);
            await staleSpace.expectDraftRoute();
            await staleSpace.writeBody('This page must remain an unpublished draft.');
            await staleSpace.expectDraftSaved();

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

            // * Verify a fresh member session is no longer offered the authoring journey.
            await expectMemberOfferedAuthoring(server.baseURL, browser, false);

            // # Submit the already-open Publish control after revocation.
            const deniedResponsePromise = stalePage.waitForResponse((response) =>
                response.request().method() === 'POST' && response.url().includes('/draft/publish'),
            );
            await staleSpace.publish();
            const deniedResponse = await deniedResponsePromise;

            // * The product flow reaches the live gate, reports the denial, and leaves the page
            // unpublished. This is not a synthetic request standing in for the UI.
            expect(deniedResponse.status()).toBe(403);
            await staleSpace.expectDraftRoute();
            await expect(stalePage.getByRole('alert').getByText('Could not publish the page. Please try again.', {exact: true})).toBeVisible();
        } finally {
            await staleContext.close();
        }
    });

    /** @objective A scheme/default change refreshes an already-open member browser over WebSocket. */
    test('refreshes page actions live when the space default changes', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            // # Keep the member on the space throughout the administrator's mutations.
            const memberPage = await memberContext.newPage();
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await loginAs(memberPage, member.username, member.password);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.expectOpen(spaceTitle);
            await expect(memberSpace.addPageButton).toBeVisible();
            const memberURL = memberPage.url();

            // # Revoke the create_page default through the compact Share capability menu.
            await loginAs(page, server.adminUsername, server.adminPassword);
            const adminSidebar = new SpacesSidebarPage(page);
            const adminSpace = new SpacePage(page);
            const share = new ShareSpaceModalPage(page);
            await adminSidebar.goto(teamName);
            await adminSidebar.openSpace(spaceTitle);
            await adminSpace.openShare();
            await share.expectOpen();
            await share.expectDefaultCapability('Create pages', true);
            await share.toggleDefaultCapability('Create pages');

            // * The untouched member page loses Add page without a reload or navigation.
            await expect(memberSpace.addPageButton).toBeHidden();
            await expect(memberPage).toHaveURL(memberURL);

            // # Restore the same default.
            await share.toggleDefaultCapability('Create pages');

            // * The same live page offers authoring again.
            await expect(memberSpace.addPageButton).toBeVisible();
            await expect(memberPage).toHaveURL(memberURL);
        } finally {
            await memberContext.close();
        }
    });

    /** @objective Membership changes refresh another administrator's already-open roster. */
    test('refreshes the permissions roster live when membership changes', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        const candidate = await createUser(page, 'docs-ws-member');
        await addUserToTeam(page, teamId, candidate.id);

        const observerContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            // # Open Permissions in a second administrator browser before either mutation.
            const observerPage = await observerContext.newPage();
            await loginAs(observerPage, server.adminUsername, server.adminPassword);
            const observerSidebar = new SpacesSidebarPage(observerPage);
            const observerSettings = new SpaceSettingsModalPage(observerPage);
            await observerSidebar.goto(teamName);
            await observerSidebar.openSpace(spaceTitle);
            await observerSettings.openFromSpaceHeader(spaceTitle);
            await observerSettings.openPermissions();
            await observerSettings.expectMemberListed(candidate.username, false);
            const observerURL = observerPage.url();

            // # Add the candidate through the first administrator browser.
            const actorSidebar = new SpacesSidebarPage(page);
            const actorSettings = new SpaceSettingsModalPage(page);
            await actorSidebar.goto(teamName);
            await actorSidebar.openSpace(spaceTitle);
            await actorSettings.openFromSpaceHeader(spaceTitle);
            await actorSettings.openPermissions();
            await actorSettings.addMember(candidate.username);

            // * The untouched observer modal gains the member row over WebSocket.
            await observerSettings.expectMemberListed(candidate.username);
            await expect(observerPage).toHaveURL(observerURL);

            // # Remove the candidate through the first browser.
            await actorSettings.removeMember(candidate.username);

            // * The same observer modal loses the persisted row without reopening.
            await observerSettings.expectMemberListed(candidate.username, false);
            await expect(observerPage).toHaveURL(observerURL);
        } finally {
            await observerContext.close();
        }
    });

    /** @objective An individual grant refreshes that member's already-open page actions. */
    test('refreshes page actions live when an individual grant changes', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        await setSpaceDefaultPermissions(page, []);
        const seededTitle = `Grant websocket ${uniqueSuffix()}`;
        await createPage(page, spaceId, seededTitle, 'A page whose edit action follows a live grant.');

        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            // # Open the page as the affected member before the grant exists.
            const memberPage = await memberContext.newPage();
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await loginAs(memberPage, member.username, member.password);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.expectPageTitle(seededTitle);
            await expect(memberSpace.editButton).toBeHidden();
            const memberURL = memberPage.url();

            // # Grant Edit pages from the member's compact capability menu in Share.
            const adminSidebar = new SpacesSidebarPage(page);
            const adminSpace = new SpacePage(page);
            const share = new ShareSpaceModalPage(page);
            await adminSidebar.goto(teamName);
            await adminSidebar.openSpace(spaceTitle);
            await adminSpace.openShare();
            await share.expectOpen();
            await share.expectDefaultSummary('Can view');
            await share.expectMemberSummary(member.username, 'Can view');
            await share.expectMemberCapability(member.username, 'Edit pages', false);
            await share.toggleMemberCapability(member.username, 'Edit pages');

            // * The grant is visibly separate from the unchanged default; its custom summary and
            // the member's untouched page both gain the new authority.
            await share.expectDefaultSummary('Can view');
            await share.expectMemberSummary(member.username, 'Custom');
            await expect(memberSpace.editButton).toBeVisible();
            await expect(memberPage).toHaveURL(memberURL);

            // # Revoke the individual grant through the same open menu.
            await share.toggleMemberCapability(member.username, 'Edit pages');

            // * Both the compact summary and the already-open member page lose it live.
            await share.expectMemberSummary(member.username, 'Can view');
            await expect(memberSpace.editButton).toBeHidden();
            await expect(memberPage).toHaveURL(memberURL);
        } finally {
            await memberContext.close();
        }
    });

    /**
     * @objective A permission change survives reopening the modal.
     *
     * Catches a control that writes successfully but renders from local state: the second
     * open re-reads from the server, so a stale write shows up as the old value.
     */
    test('every space-default permission can be changed and reads back', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);
        const labels = ['Create pages', 'Comment on pages', 'Edit pages', 'Delete own pages', 'Delete any page'];
        const seededTitle = `Custom default victim ${uniqueSuffix()}`;

        await loginAs(page, server.adminUsername, server.adminPassword);
        const seededPage = await createPage(page, spaceId, seededTitle);
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

        // * The inverse of Contribute is the deliberately non-preset combination containing only
        //   delete_page. Prove that pooled scheme changes effective authority, not just metadata:
        //   an ordinary member can now delete somebody else's seeded page.
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await memberContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.deletePage(seededTitle);
        } finally {
            await memberContext.close();
        }
        await expectPageDeleted(server.baseURL, browser, server.adminUsername, server.adminPassword, seededPage.id);

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
    test('view access can be changed in both directions and reads back', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const space = new SpacePage(page);
        const share = new ShareSpaceModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await space.openShare();
        await share.expectOpen();

        // * Verify a space created without an explicit view access starts public
        await share.expectAccess('Public');
        await expectNonMemberAccess(server.baseURL, browser, true);

        // # Make it private
        await share.chooseAccess('Private');

        // # Reopen so the value comes from the server rather than from the click
        await share.close();
        await space.openShare();
        await share.expectOpen();

        // * Verify the space is private
        await share.expectAccess('Private');
        await expectNonMemberAccess(server.baseURL, browser, false);

        // # Restore Public and re-read again. This catches a one-way implementation that can
        // privatize a space but cannot restore the team-wide read grant.
        await share.chooseAccess('Public');
        await share.close();
        await space.openShare();
        await share.expectOpen();
        await share.expectAccess('Public');

        // * The inverse transition restores actual discovery and known-space access for an
        //   eligible team member who was never invited to the space.
        await expectNonMemberAccess(server.baseURL, browser, true);
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

        // * A separately authenticated browser re-reads the tree after the delete.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await expect(verifySpace.pageTreeLink(seededTitle)).toBeHidden();
        } finally {
            await verifyContext.close();
        }
    });

    /** @objective The create_page member cell enables that member's real publish flow. */
    test('a per-member create grant lets that member publish a page', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);
        await setSpaceDefaultPermissions(page, []);

        // * With no space default and no member grant, the member cannot start authoring.
        await expectMemberOfferedAuthoring(server.baseURL, browser, false);

        // # Grant the exact create_page cell in the member's row.
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'create_page');
        await settings.close();

        // * A fresh member session completes Add page, body entry, autosave, and Publish.
        let authoredTitle = '';
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            authoredTitle = await authorInBrowser(await memberContext.newPage(), member, 'Member create grant', false);
        } finally {
            await memberContext.close();
        }

        // * Another browser reads the published title and body from the product UI.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await verifySpace.openPageFromTree(authoredTitle);
            await verifySpace.expectPageTitle(authoredTitle);
            await verifySpace.expectBody(`Authored by ${member.username}.`);
        } finally {
            await verifyContext.close();
        }
    });

    /** @objective The edit_page member cell enables that member's real update flow. */
    test('a per-member edit grant lets that member update a page', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);
        const seededTitle = `Edit grant target ${uniqueSuffix()}`;
        const marker = `Edited through member cell ${uniqueSuffix()}`;

        await loginAs(page, server.adminUsername, server.adminPassword);
        await createPage(page, spaceId, seededTitle, 'Initial body. ');
        await setSpaceDefaultPermissions(page, []);

        // * The member can read the fixture but cannot enter Edit before the grant.
        const beforeContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await beforeContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await expect(memberSpace.editButton).toBeHidden();
        } finally {
            await beforeContext.close();
        }

        // # Grant the exact edit_page cell in the member's row.
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'edit_page');
        await settings.close();

        // * A fresh member session completes Edit, autosave, and Update.
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await memberContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.editButton.click();
            await memberSpace.expectPublishedPageEditing();
            await memberSpace.writePublishedBody(marker);
            await memberSpace.expectDraftSaved();
            await memberSpace.update();
            await memberSpace.expectPublished();
        } finally {
            await memberContext.close();
        }

        // * Another browser reads the persisted edit from the rendered page.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await verifySpace.openPageFromTree(seededTitle);
            await verifySpace.expectBody(marker);
        } finally {
            await verifyContext.close();
        }
    });

    /** @objective The delete_own_page member cell enables deletion of that member's page. */
    test('a per-member delete-own grant lets that member delete their page', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        // # Publish the owned fixture through the member's UI while the contribute default is live.
        let ownedTitle = '';
        const authorContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            ownedTitle = await authorInBrowser(await authorContext.newPage(), member, 'Owned grant target');
        } finally {
            await authorContext.close();
        }

        await loginAs(page, server.adminUsername, server.adminPassword);
        await setSpaceDefaultPermissions(page, []);

        // * Clearing the default removes the owned-page delete action.
        const beforeContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await beforeContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(ownedTitle);
            await memberSpace.expectPageAction(ownedTitle, 'Delete page', false);
        } finally {
            await beforeContext.close();
        }

        // # Grant the exact delete_own_page cell in the member's row.
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'delete_own_page');
        await settings.close();

        // * A fresh member session completes the page-menu deletion and confirmation.
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await memberContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(ownedTitle);
            await memberSpace.deletePage(ownedTitle);
        } finally {
            await memberContext.close();
        }

        // * Another browser re-reads the tree and confirms the page stayed deleted.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await expect(verifySpace.pageTreeLink(ownedTitle)).toBeHidden();
        } finally {
            await verifyContext.close();
        }
    });

    /** @objective Revoking delete_page blocks an already-open product delete flow. */
    test('withheld deletion is hidden and rejected when submitted from stale UI', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);
        const seededTitle = `Seed ${uniqueSuffix()}`;

        // # Seed somebody else's page, then grant delete_page through the exact member cell.
        await loginAs(page, server.adminUsername, server.adminPassword);
        const seededPage = await createPage(page, spaceId, seededTitle, 'Must survive the denied delete.');
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'delete_page');
        await settings.close();

        // # Open the real delete confirmation as the member. Keep this client deliberately stale
        // across revocation so the confirmation can still submit to the live permission gate.
        const staleContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await staleContext.newPage();
            await memberPage.routeWebSocket(/\/api\/v4\/websocket/, () => {});
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.openPageMenu(seededTitle);
            await memberPage.getByRole('menuitem', {name: 'Delete page'}).click();
            const deleteDialog = memberPage.getByRole('dialog', {name: 'Delete page'});
            await expect(deleteDialog).toBeVisible();

            // # Revoke delete_page in the administrator's real settings UI.
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.openPermissions();
            await settings.toggleMemberPermission(member.id, 'delete_page');
            await settings.close();

            // # Confirm deletion in the already-open product dialog.
            const deniedResponsePromise = memberPage.waitForResponse((response) =>
                response.request().method() === 'DELETE' &&
                response.url().includes(`/spaces/${spaceId}/pages/${seededPage.id}`),
            );
            await deleteDialog.getByRole('button', {name: 'Delete', exact: true}).click();
            const deniedResponse = await deniedResponsePromise;

            // * The live route denies the UI action, the dialog remains, and the user sees the
            // failure rather than an optimistic disappearance.
            expect(deniedResponse.status()).toBe(403);
            await expect(deleteDialog).toBeVisible();
            await expect(memberPage.getByRole('alert').getByText(`Could not delete “${seededTitle}”.`, {exact: true})).toBeVisible();
        } finally {
            await staleContext.close();
        }

        // * Fresh member UI withholds the action, while a fresh admin UI still reads the page.
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await memberContext.newPage();
            await loginAs(memberPage, member.username, member.password);
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.openPageFromTree(seededTitle);
            await memberSpace.expectPageAction(seededTitle, 'Delete page', false);
        } finally {
            await memberContext.close();
        }

        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await verifySpace.openPageFromTree(seededTitle);
            await verifySpace.expectBody('Must survive the denied delete.');
        } finally {
            await verifyContext.close();
        }
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

        // * A fresh administrator session confirms all three outcomes persisted: the owned page
        // is gone, and the other page has both its new title and edited body.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await expect(verifySpace.pageTreeLink(ownedTitle)).toBeHidden();
            await verifySpace.openPageFromTree(renamedTitle);
            await verifySpace.expectPageTitle(renamedTitle);
            await verifySpace.expectBody('Edited by the member.');
        } finally {
            await verifyContext.close();
        }

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

        // * A fresh administrator session confirms the renamed space and removed candidate.
        await expectRosterEntry(
            server.baseURL,
            browser,
            server.adminUsername,
            server.adminPassword,
            candidate.username,
            false,
        );

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

        // * Leaving is persisted in the roster another authenticated session reads.
        await expectRosterEntry(
            server.baseURL,
            browser,
            server.adminUsername,
            server.adminPassword,
            member.username,
            false,
        );
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
    test('a team administrator with manage_space has only the manage-space UI tier', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        const manageOnlyAdmin = await createUser(page, 'docs-perms-manage-only');
        const candidate = await createUser(page, 'docs-perms-manage-add');
        await addUserToTeam(page, teamId, manageOnlyAdmin.id);
        await addUserToTeam(page, teamId, candidate.id);
        await setTeamAdminSpaceTiers(page, teamId, {manage: true, delete: false});
        await promoteToTeamAdmin(page, teamId, manageOnlyAdmin.id);
        await setSpaceDefaultPermissions(page, []);

        const context = await newContext(browser, {baseURL: server.baseURL});
        const renamed = `Managed ${uniqueSuffix()}`;
        try {
            const manageOnlyPage = await context.newPage();
            await loginAs(manageOnlyPage, manageOnlyAdmin.username, manageOnlyAdmin.password);
            const sidebar = new SpacesSidebarPage(manageOnlyPage);
            const space = new SpacePage(manageOnlyPage);
            const settings = new SpaceSettingsModalPage(manageOnlyPage);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);

            // * The two team tiers are independent in the header.
            await settings.openSpaceHeaderMenu(spaceTitle);
            await expect(manageOnlyPage.getByRole('menuitem', {name: 'Space settings'})).toBeVisible();
            await expect(manageOnlyPage.getByRole('menuitem', {name: 'Archive space'})).toBeHidden();
            await manageOnlyPage.keyboard.press('Escape');

            // # Rename is a manage-tier operation and must complete.
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.renameSpace(renamed);
            await settings.openPermissions();

            // * Space-wide controls require admin_space even though this actor can manage people.
            await settings.expectAccessEnabled('Private', false);
            await settings.expectPermissionEnabled('Create pages', false);

            // * Ordinary member grants and roster changes are live, but promotion is not.
            await settings.expectMemberPermissionEnabled(member.id, 'create_page', true);
            await settings.expectMemberPermissionEnabled(member.id, 'admin_space', false);
            await settings.toggleMemberPermission(member.id, 'create_page');
            await settings.addMember(candidate.username);
            await settings.expectMemberPermissionEnabled(candidate.id, 'create_page', true);
            await settings.removeMember(candidate.username);
            await settings.close();
            await space.expectOpen(renamed);
        } finally {
            await context.close();
        }
        spaceTitle = renamed;

        // * The manage-only administrator's member grant has a real UI consequence: the member
        // can publish even though the space default remains empty.
        const authorContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            await authorInBrowser(await authorContext.newPage(), member, 'Managed grant', false);
        } finally {
            await authorContext.close();
        }

        // * The rename and removal survive into a fresh administrator session.
        await expectRosterEntry(
            server.baseURL,
            browser,
            server.adminUsername,
            server.adminPassword,
            candidate.username,
            false,
        );
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

        // * A fresh deleter session re-reads the archived state.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            await loginAs(verifyPage, deleter.username, deleter.password);
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            await verifySidebar.goto(teamName);
            await expect(verifySidebar.spaceLink(spaceTitle)).toBeHidden();
            await verifyPage.goto(`/${teamName}/spaces/${spaceId}`);
            await expect(verifyPage).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
        } finally {
            await verifyContext.close();
        }
    });

    /** @objective Removing a space member from the team removes all browser-visible access. */
    test('a former team member cannot discover or deep-link the space', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        await removeUserFromTeam(page, teamId, member.id);

        // * A separately authenticated session re-reads team and space membership.
        const context = await newContext(browser, {baseURL: server.baseURL});
        try {
            const formerMemberPage = await context.newPage();
            await loginAs(formerMemberPage, member.username, member.password);
            await formerMemberPage.goto(`/${teamName}/spaces/${spaceId}`);
            await expect(formerMemberPage).not.toHaveURL(new RegExp(`/spaces/${spaceId}(?:[/?#]|$)`));
            await expect(formerMemberPage.getByRole('main').getByRole('button', {name: spaceTitle, exact: true})).toBeHidden();
        } finally {
            await context.close();
        }
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
     * The browser cases below read a real page and submit a real Publish control across a guest
     * demotion. They therefore pin both the rendered affordance and the server-enforced outcome.
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
        test('can open and read the space it belongs to', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
            const sidebar = new SpacesSidebarPage(page);
            const space = new SpacePage(page);
            const readableTitle = `Guest-readable ${uniqueSuffix()}`;
            const readableBody = `Visible to guest ${uniqueSuffix()}`;

            // # Seed published content as the administrator; guest reading is the journey under test.
            await loginAs(page, server.adminUsername, server.adminPassword);
            await createPage(page, spaceId, readableTitle, readableBody);

            // # Open the space as the guest
            await loginAs(page, guest.username, guest.password);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await space.openPageFromTree(readableTitle);

            // * Verify the guest reads the actual published title and body in read-only mode.
            await space.expectOpen(spaceTitle);
            await space.expectPageTitle(readableTitle);
            await space.expectBody(readableBody);
            await space.expectBodyReadOnly();
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
         * The test prepares a draft through the UI before demotion, then submits that same
         * Publish control afterward. That makes the live 403 observable through the product
         * flow even though a freshly loaded guest correctly has no Add page control.
         */
        test('cannot publish from an authoring flow opened before becoming a guest', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
            await loginAs(page, server.adminUsername, server.adminPassword);
            const futureGuest = await createUser(page, 'docs-perms-future-guest');
            await addUserToTeam(page, teamId, futureGuest.id);
            await addSpaceMember(page, spaceId, futureGuest.id);

            // # Start a page in the real UI while the actor is still an ordinary member. Block
            // WebSocket delivery only in this client so its already-rendered Publish survives
            // the demotion and can exercise the server boundary.
            const staleContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const stalePage = await staleContext.newPage();
                await stalePage.routeWebSocket(/\/api\/v4\/websocket/, () => {});
                await loginAs(stalePage, futureGuest.username, futureGuest.password);
                const staleSidebar = new SpacesSidebarPage(stalePage);
                const staleSpace = new SpacePage(stalePage);
                await staleSidebar.goto(teamName);
                await staleSidebar.openSpace(spaceTitle);
                await staleSpace.addPage(`Guest denied ${uniqueSuffix()}`);
                await staleSpace.expectDraftRoute();
                await staleSpace.writeBody('This must not become a published page.');
                await staleSpace.expectDraftSaved();

                // # Demote the actor, then click Publish in the already-open UI.
                await demoteToGuest(page, futureGuest.id);
                const deniedResponsePromise = stalePage.waitForResponse((response) =>
                    response.request().method() === 'POST' && response.url().includes('/draft/publish'),
                );
                await staleSpace.publish();
                const deniedResponse = await deniedResponsePromise;

                // * Guest enforcement rejects the UI submission and leaves it unpublished.
                expect(deniedResponse.status()).toBe(403);
                await staleSpace.expectDraftRoute();
                await expect(stalePage.getByRole('alert').getByText('Could not publish the page. Please try again.', {exact: true})).toBeVisible();
            } finally {
                await staleContext.close();
            }

            // * A fresh guest session also withholds the authoring entry point.
            const guestContext = await newContext(browser, {baseURL: server.baseURL});
            try {
                const guestPage = await guestContext.newPage();
                await loginAs(guestPage, guest.username, guest.password);
                const guestSidebar = new SpacesSidebarPage(guestPage);
                const guestSpace = new SpacePage(guestPage);
                await guestSidebar.goto(teamName);
                await guestSidebar.openSpace(spaceTitle);
                await guestSpace.expectOpen(spaceTitle);
                await expect(guestSpace.addPageButton).toBeHidden();
            } finally {
                await guestContext.close();
            }
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
