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
import {addSpaceMember, apiRoot, createPage, createSpace, isSpaceMember} from '../helpers/docs';
import {demoteToGuest, setGuestAccountsEnabled} from '../helpers/guest';
import {addUserToTeam, createTeam} from '../helpers/team';
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
 * A permission consequence is asserted twice: over the API, for what the server enforces,
 * and in the browser, for what the product offers. Those are not the same fact, and this
 * suite previously assumed they were. They can disagree in both directions — a control the
 * server would accept but the client withholds is a feature nobody can reach, and a control
 * the client offers but the server refuses is an error message where an affordance should
 * have been. Only asserting both catches either.
 */
test.describe('space permissions', () => {
    let teamName: string;
    let teamId: string;
    let spaceTitle: string;
    let spaceId: string;
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
        const adminId = (await admin.json() as {id: string}).id;
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

    // Probes the member's authority directly. Not createPage(): that throws on a refusal,
    // and a refusal is the expected outcome half the time here.
    const memberCanCreatePage = async (baseURL: string, browser: Browser): Promise<number> => {
        const context = await newContext(browser, {baseURL});
        try {
            const page = await context.newPage();
            await loginAs(page, member.username, member.password);

            const response = await page.request.post(`${apiRoot}/spaces/${spaceId}/pages`, {
                ...requestedWith,
                data: {title: `Probe ${uniqueSuffix()}`, body: ''},
            });

            return response.status();
        } finally {
            await context.close();
        }
    };

    // Whether the member may delete a page they did not author — which is delete_page, not
    // delete_own_page. Probed directly for the same reason memberCanCreatePage is: a refusal
    // is the expected answer half the time.
    const memberCanDeletePage = async (baseURL: string, browser: Browser, pageId: string): Promise<number> => {
        const context = await newContext(browser, {baseURL});
        try {
            const probePage = await context.newPage();
            await loginAs(probePage, member.username, member.password);

            const response = await probePage.request.delete(`${apiRoot}/spaces/${spaceId}/pages/${pageId}`, requestedWith);
            return response.status();
        } finally {
            await context.close();
        }
    };

    // Whether the space view OFFERS the member page creation. The API probes above answer
    // what the server enforces; this answers what the product lets them reach. A test that
    // asserts only the first passes while the feature is unusable.
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

    // Whether userId holds a membership in the space, read as the admin. Used where the
    // question is who the space ended up containing rather than what they may do.
    const spaceContains = async (
        server: {baseURL: string; adminUsername: string; adminPassword: string},
        browser: Browser,
        userId: string,
    ): Promise<boolean> => {
        const context = await newContext(browser, {baseURL: server.baseURL});
        try {
            const adminPage = await context.newPage();
            await loginAs(adminPage, server.adminUsername, server.adminPassword);

            return await isSpaceMember(adminPage, spaceId, userId);
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

        // * Verify the seeded default already grants the member page creation, so the
        //   revocation below is what changes it rather than it never having been granted.
        expect(await memberCanCreatePage(server.baseURL, browser)).toBe(201);

        // * Verify the product offers it too. Asserted alongside the status code, not
        //   instead of it: a grant the server honours and the UI hides is not a grant the
        //   user has.
        expect(await memberIsOfferedAuthoring(server.baseURL, browser)).toBe(true);

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

        // * Verify the member can no longer create a page. The 403 is the point: the click
        //   changed server-enforced authority, not just a checkbox.
        expect(await memberCanCreatePage(server.baseURL, browser)).toBe(403);

        // * Verify the revocation reached the product too, so the member is not left
        //   clicking a control that can only fail.
        expect(await memberIsOfferedAuthoring(server.baseURL, browser)).toBe(false);
    });

    /**
     * @objective A permission change survives reopening the modal.
     *
     * Catches a control that writes successfully but renders from local state: the second
     * open re-reads from the server, so a stale write shows up as the old value.
     */
    test('a permission change is read back from the server', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const settings = new SpaceSettingsModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // # Grant a permission the contribute default withholds
        await settings.expectPermission('Delete any page', false);
        await settings.togglePermission('Delete any page');

        // # Reopen the modal so its state comes from a fresh read
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();

        // * Verify both the granted and the untouched permission survived the round trip
        await settings.expectPermission('Delete any page', true);
        await settings.expectPermission('Create pages', true);
    });

    /**
     * @objective A space admin can flip the space to Private, and it reads back.
     *
     * The option was inert scaffolding ("Coming soon") until the permissions work landed,
     * so this pins that it is now a real control.
     */
    test('view access can be changed to private and reads back', {tag: ['@docs', '@permissions']}, async ({page, server}) => {
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
        let seededPageId: string;
        try {
            const adminPage = await adminContext.newPage();
            await loginAs(adminPage, server.adminUsername, server.adminPassword);
            seededPageId = (await createPage(adminPage, spaceId, `Seed ${uniqueSuffix()}`)).id;
        } finally {
            await adminContext.close();
        }

        // * Verify the member cannot delete it to begin with
        expect(await memberCanDeletePage(server.baseURL, browser, seededPageId)).toBe(403);

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

        // * Verify the grant changed what that member may actually do. This is the point:
        //   a checkbox that persists but grants nothing would pass every assertion above.
        expect(await memberCanDeletePage(server.baseURL, browser, seededPageId)).toBe(200);

        // * Verify it survives a reopen, so the grant reached the server rather than
        //   living in the modal's local state
        await settings.close();
        await settings.openFromSpaceHeader(spaceTitle);
        await settings.openPermissions();
        await settings.expectMemberPermission(member.id, 'delete_page', true);
    });

    /**
     * @objective A page action the space default withholds is not offered on the page.
     *
     * The contribute default grants delete_own_page but not delete_page, so a member may
     * delete what they wrote and nothing else. The page header withholds create and edit on
     * exactly this reasoning; the page menu withholds nothing, so Delete page is offered on
     * a page the member did not author and answers 403 when clicked.
     *
     * EXPECTED TO FAIL: page_menu.tsx reads no permission at all. Two surfaces of one
     * feature disagree about whether an action the server refuses should be shown.
     */
    test('a member is not offered page actions the space default withholds', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const space = new SpacePage(page);
        const seededTitle = `Seed ${uniqueSuffix()}`;

        // # Seed a page the member did not author, so removing it needs delete_page
        const adminContext = await newContext(browser, {baseURL: server.baseURL});
        let seededPageId: string;
        try {
            const adminPage = await adminContext.newPage();
            await loginAs(adminPage, server.adminUsername, server.adminPassword);
            seededPageId = (await createPage(adminPage, spaceId, seededTitle)).id;
        } finally {
            await adminContext.close();
        }

        // * Verify the server refuses it, so the absence asserted below would be the menu
        //   agreeing with enforcement rather than the menu having failed to render
        expect(await memberCanDeletePage(server.baseURL, browser, seededPageId)).toBe(403);

        // # Open that page as the member and open its menu
        await loginAs(page, member.username, member.password);
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await space.openPageFromTree(seededTitle);
        await page.getByRole('button', {name: `Page options for ${seededTitle}`}).click();

        // * Verify the menu opened, so the absence below is a real absence
        await expect(page.getByRole('menuitem', {name: 'Copy link'})).toBeVisible();

        // * Verify the action the server just refused is not offered
        await expect(page.getByRole('menuitem', {name: 'Delete page'})).toBeHidden();
    });

    /**
     * A team member who is not a member of the space, on a space that is open.
     *
     * This is the persona the open/private distinction exists for, and the suite had no actor
     * for it. An open space is readable by any team member through the server's
     * read_public_channel fall-through, and the server will join such a reader to the space on
     * their first write when the space default grants it — so the product's claim is that
     * authoring is one click away and the join happens on the way to it.
     *
     * EXPECTED TO FAIL, all three. The two halves were built against different assumptions and
     * were never driven end to end together: the server auto-joins a non-member who writes,
     * while the space view resolves their present-tense permission set (read_page alone) and
     * correctly withholds the control that would send that write. Nothing in the UI reaches the
     * join. These tests assert the intended behaviour, not the behaviour that ships today.
     */
    test.describe('a non-member of an open space', () => {
        // Drives the non-member through authoring in the browser — the flow that is supposed
        // to turn a reader into a member. Returns the title it published.
        const authorAsNonMember = async (page: Page): Promise<string> => {
            const sidebar = new SpacesSidebarPage(page);
            const space = new SpacePage(page);
            const title = `NonMember ${uniqueSuffix()}`;

            await loginAs(page, nonMember.username, nonMember.password);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await space.expectOpen(spaceTitle);

            // Fails here today: the view withholds page creation from a non-member, so the
            // write that would join them is never sent.
            await space.addPage(title);
            await space.expectDraftRoute();
            await space.writeBody('Authored without an invitation.');
            await space.expectDraftSaved();
            await space.publish();
            await space.expectPublished();

            return title;
        };

        /**
         * @objective An open space offers a non-member the authoring its default grants.
         *
         * The discovery half is asserted first and passes today, which localises the failure:
         * the space is listed and opens, so the read fall-through works and it is only the
         * affordance that is missing.
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
            //   them. This is the assertion the product fails.
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

            // * Verify reading alone did not make them a member
            expect(await spaceContains(server, browser, nonMember.id)).toBe(false);

            // # Author a page
            await authorAsNonMember(page);

            // * Verify authoring did. The roster is the observable outcome: whatever mechanism
            //   joins them, the space has to end up containing them or their next write fails.
            expect(await spaceContains(server, browser, nonMember.id)).toBe(true);
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

            // * Verify the precondition: one invited member, one who joined by writing
            expect(await spaceContains(server, browser, member.id)).toBe(true);
            expect(await spaceContains(server, browser, nonMember.id)).toBe(true);

            // # Make the space private, as its admin, through the UI
            await loginAs(page, server.adminUsername, server.adminPassword);
            await sidebar.goto(teamName);
            await sidebar.openSpace(spaceTitle);
            await settings.openFromSpaceHeader(spaceTitle);
            await settings.openPermissions();
            await settings.chooseAccess('Private');
            await settings.close();

            // * Verify the invitation survives the withdrawal of the broad grant
            expect(await spaceContains(server, browser, member.id)).toBe(true);

            // * Verify the author who held access only through the open setting does not
            expect(await spaceContains(server, browser, nonMember.id)).toBe(false);

            // * Verify the space is no longer even discoverable to them
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const nonMemberPage = await context.newPage();
                await loginAs(nonMemberPage, nonMember.username, nonMember.password);

                const nonMemberSidebar = new SpacesSidebarPage(nonMemberPage);
                await nonMemberSidebar.goto(teamName);
                await expect(nonMemberSidebar.spaceLink(spaceTitle)).toBeHidden();
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

        // What GuestAccountsSettings.Enable was before the demotion, or null when the demotion
        // needed no config change. Restored from a fresh page in afterEach — see setGuestAccountsEnabled.
        let guestAccountsWereEnabled: boolean | null = null;

        test.beforeEach(async ({browser, server}) => {
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const page = await context.newPage();
                await loginAs(page, server.adminUsername, server.adminPassword);

                guest = await createUser(page, 'docs-perms-guest');
                await addUserToTeam(page, teamId, guest.id);
                await addSpaceMember(page, spaceId, guest.id);

                // Last, so the demotion converts the memberships seeded above.
                guestAccountsWereEnabled = await demoteToGuest(page, guest.id);
            } finally {
                await context.close();
            }
        });

        test.afterEach(async ({browser, server}) => {
            if (guestAccountsWereEnabled === null) {
                return;
            }

            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const page = await context.newPage();
                await loginAs(page, server.adminUsername, server.adminPassword);
                await setGuestAccountsEnabled(page, guestAccountsWereEnabled);
            } finally {
                guestAccountsWereEnabled = null;
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
         * The affordance half of the guest invariant. The server refuses the guest's create
         * (the sibling test below), and the space view withholds the control that would make
         * it: page creation is gated on the caller's own create_page, resolved by the same
         * single-space read the view already performs.
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

        /**
         * @objective The space default does not reach a guest.
         *
         * Probed over the API for the same reason the member's authority is: a status code is
         * the same fact either way, and it does not depend on how a denial happens to surface.
         * The contrast is with the member on this identical space, who answers 201.
         */
        test('is refused page creation the space default grants a member', {tag: ['@docs', '@permissions']}, async ({server, browser}) => {
            // * Verify the space default does grant page creation to an ordinary member
            expect(await memberCanCreatePage(server.baseURL, browser)).toBe(201);

            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const page = await context.newPage();
                await loginAs(page, guest.username, guest.password);

                // * Verify the guest may read the space
                const read = await page.request.get(`${apiRoot}/spaces/${spaceId}`, requestedWith);
                expect(read.status()).toBe(200);

                // * Verify the same default that admitted the member does not reach the guest
                const create = await page.request.post(`${apiRoot}/spaces/${spaceId}/pages`, {
                    ...requestedWith,
                    data: {title: `Guest probe ${uniqueSuffix()}`, body: ''},
                });
                expect(create.status()).toBe(403);
            } finally {
                await context.close();
            }
        });
    });
});
