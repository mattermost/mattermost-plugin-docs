// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import {expect, newContext, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {readJsonOrThrow, requestedWith, uniqueSuffix} from '../helpers/client';
import {addUserToTeam, createTeam} from '../helpers/team';
import {createUser, type SeededUser} from '../helpers/user';
import {richText, type RichText} from '../data/rich_text';
import {CreateSpaceModalPage} from '../pages/create_space_modal_page';
import {DocsSwitcherPage} from '../pages/docs_switcher_page';
import {ShareSpaceModalPage} from '../pages/share_space_modal_page';
import {SpacePage} from '../pages/space_page';
import {SpacesSidebarPage} from '../pages/spaces_sidebar_page';

// Serial: later tests use the space and page the first test creates, and a plain describe would
// retry them alone against a freshly seeded team.
test.describe.serial('docs authoring', () => {
    let spaceTitle: string;
    let pageTitle: string;
    let body: RichText;
    let teamName: string;
    let member: SeededUser;
    let publishedPageUrl: string;


    test.beforeAll(async ({browser, server}) => {
        const context = await newContext(browser, {baseURL: server.baseURL});
        const page = await context.newPage();

        await loginAs(page, server.adminUsername, server.adminPassword);

        // Here, not module scope, so a retry cannot match the failed attempt's leftovers.
        spaceTitle = `PW Space ${uniqueSuffix()}`;
        pageTitle = `PW Page ${uniqueSuffix()}`;
        body = richText(uniqueSuffix());

        const team = await createTeam(page, 'docs-authoring');
        member = await createUser(page, 'docs-member');
        await addUserToTeam(page, team.id, member.id);
        teamName = team.name;

        await context.close();
    });

    /**
     * @objective Write and publish a page in a new space, then share the space.
     * @precondition A team and a second team member are seeded via the API.
     */
    test('creates a space, writes and publishes a page, and adds a member', {tag: '@docs'}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const createSpaceModal = new CreateSpaceModalPage(page);
        const spacePage = new SpacePage(page);
        const shareModal = new ShareSpaceModalPage(page);

        // # Authenticate this browser context, then open the Docs product
        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);

        // # Create a space
        await sidebar.openCreateSpace();
        await createSpaceModal.expectOpen();
        await createSpaceModal.createSpace(spaceTitle);

        // * The new space opens
        await spacePage.expectOpen(spaceTitle);

        // # Add a page and give it a title
        await spacePage.addPage(pageTitle);

        // * The page starts life as an unpublished draft
        await spacePage.expectDraftRoute();

        // # Write a body covering the editor's text formats
        await spacePage.writeRichBody(body);

        // * Each format was applied as it was typed
        const draftFormats = spacePage.bodyFormats();
        await expect(draftFormats.heading1).toHaveText(body.heading1);
        await expect(draftFormats.heading2).toHaveText(body.heading2);
        await expect(draftFormats.bold).toHaveText(body.bold);
        await expect(draftFormats.italic).toHaveText(body.italic);
        await expect(draftFormats.strike).toHaveText(body.strike);
        await expect(draftFormats.inlineCode).toHaveText(body.inlineCode);
        await expect(draftFormats.quote).toContainText(body.quote);
        await expect(draftFormats.bulletItems).toHaveText(body.bullets);
        await expect(draftFormats.orderedItems).toHaveText(body.ordered);
        await expect(draftFormats.rule).toBeVisible();
        await expect(draftFormats.codeBlock).toHaveText(body.code);

        // * The editor reports that the body change made its unsaved → saving → saved trip.
        await spacePage.expectDraftSaved();

        // # Publish the draft
        await spacePage.publish();

        // * The draft becomes a published page, keeping its title and body
        await spacePage.expectPublished();
        await spacePage.expectPageTitle(pageTitle);
        await spacePage.expectBody(body.heading1);
        publishedPageUrl = page.url();

        // # Share the space with the seeded teammate
        await spacePage.openShare();
        await shareModal.expectOpen();
        await shareModal.addMember(member.username);

        // * The teammate is now a member of the space
        await shareModal.expectMemberListed(member.username);
    });

    /**
     * @objective Find the shared space through the product's navigation and read it.
     * @precondition The previous test published the page and added this user.
     */
    test('lets the added member navigate to and read the published page', {tag: '@docs'}, async ({page}) => {
        const sidebar = new SpacesSidebarPage(page);
        const switcher = new DocsSwitcherPage(page);
        const spacePage = new SpacePage(page);

        // # Sign in as the teammate and open Docs for the team
        await loginAs(page, member.username, member.password);
        await sidebar.goto(teamName);

        // * The space shared with them is listed in the sidebar
        await sidebar.expectSpaceListed(spaceTitle);

        // # Reach the space, then the page, from the left-hand sidebar
        await sidebar.openSpace(spaceTitle);
        await spacePage.expectOpen(spaceTitle);
        await spacePage.openPageFromTree(pageTitle);

        // * The page opens
        await spacePage.expectPageTitle(pageTitle);

        // # Reach the space again through the Find-docs switcher, opened from the sidebar
        await sidebar.goto(teamName);
        await sidebar.openSwitcher();
        await switcher.expectOpen();
        await switcher.search(spaceTitle);
        await switcher.selectResult(spaceTitle);

        // * The switcher navigated to the space
        await spacePage.expectOpen(spaceTitle);

        // # Reach the page with the keyboard: shortcut, query, arrow, Enter
        await switcher.openWithShortcut(await sidebar.advertisedSwitcherShortcut());
        await switcher.search(pageTitle);
        await switcher.selectResultWithKeyboard(pageTitle);

        // * The switcher closed and landed on the page
        await switcher.expectClosed();
        await spacePage.expectPageTitle(pageTitle);

        // # Finally, load the published URL directly
        await page.goto(publishedPageUrl);

        // * The title is as the author left it
        await spacePage.expectPageTitle(pageTitle);

        // * Every format survived publishing and renders for the reader
        const formats = spacePage.bodyFormats();
        await expect(formats.heading1).toHaveText(body.heading1);
        await expect(formats.heading2).toHaveText(body.heading2);
        await expect(formats.bold).toHaveText(body.bold);
        await expect(formats.italic).toHaveText(body.italic);
        await expect(formats.strike).toHaveText(body.strike);
        await expect(formats.inlineCode).toHaveText(body.inlineCode);
        await expect(formats.quote).toContainText(body.quote);
        await expect(formats.bulletItems).toHaveText(body.bullets);
        await expect(formats.orderedItems).toHaveText(body.ordered);
        await expect(formats.rule).toBeVisible();
        await expect(formats.codeBlock).toHaveText(body.code);

        // * The reader gets the published page, not a draft or an editable surface
        await expect(page).not.toHaveURL(/\/drafts\//);
        await spacePage.expectBodyReadOnly();
    });

    /**
     * @objective The visibility selected in the create modal controls another team member's access.
     * @precondition A second user belongs to the team but is not invited to either new space.
     */
    test('creates private and public spaces with their selected access', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        const sidebar = new SpacesSidebarPage(page);
        const createSpaceModal = new CreateSpaceModalPage(page);
        const privateTitle = `Private ${uniqueSuffix()}`;
        const publicTitle = `Public ${uniqueSuffix()}`;

        await loginAs(page, server.adminUsername, server.adminPassword);

        // # Create without changing the initial selection.
        await sidebar.goto(teamName);
        await sidebar.openCreateSpace();
        await createSpaceModal.expectOpen();
        await createSpaceModal.expectVisibility('Private');
        const privateResponsePromise = page.waitForResponse((response) => (
            response.request().method() === 'POST' && /\/api\/v1\/teams\/[^/]+\/spaces$/.test(response.url())
        ));
        await createSpaceModal.createSpace(privateTitle);
        const privateResponse = await privateResponsePromise;
        expect(privateResponse.ok()).toBe(true);
        const privateSpace = await privateResponse.json() as {id: string; view_access: string};
        expect(privateSpace.view_access).toBe('private');
        await new SpacePage(page).expectOpen(privateTitle);

        // # Create another space after explicitly selecting Public.
        await sidebar.goto(teamName);
        await sidebar.openCreateSpace();
        await createSpaceModal.expectOpen();
        await createSpaceModal.chooseVisibility('Public');
        const publicResponsePromise = page.waitForResponse((response) => (
            response.request().method() === 'POST' && /\/api\/v1\/teams\/[^/]+\/spaces$/.test(response.url())
        ));
        await createSpaceModal.createSpace(publicTitle);
        const publicResponse = await publicResponsePromise;
        expect(publicResponse.ok()).toBe(true);
        const publicSpace = await publicResponse.json() as {id: string; view_access: string};
        expect(publicSpace.view_access).toBe('open');
        await new SpacePage(page).expectOpen(publicTitle);

        // * A fresh teammate who was never invited cannot discover or deep-link the private
        // space, but can discover and open the public one.
        const memberContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await memberContext.newPage();
            const memberSidebar = new SpacesSidebarPage(memberPage);
            await loginAs(memberPage, member.username, member.password);
            await memberSidebar.goto(teamName);
            await expect(memberSidebar.spaceLink(privateTitle)).toBeHidden();
            await memberPage.goto(`/${teamName}/spaces/${privateSpace.id}`);
            await expect(memberPage).not.toHaveURL(new RegExp(`/spaces/${privateSpace.id}(?:[/?#]|$)`));

            await memberSidebar.goto(teamName);
            await memberSidebar.expectSpaceListed(publicTitle);
            await memberSidebar.openSpace(publicTitle);
            await new SpacePage(memberPage).expectOpen(publicTitle);
        } finally {
            await memberContext.close();
        }
    });

    /**
     * @objective An unlicensed server offers only the three default-permission presets it can save.
     * @precondition The first test created the space under the contribute preset.
     */
    test('changes among the included permission presets without a custom-scheme license', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        test.setTimeout(120_000);

        const sidebar = new SpacesSidebarPage(page);
        const spacePage = new SpacePage(page);
        const share = new ShareSpaceModalPage(page);

        await loginAs(page, server.adminUsername, server.adminPassword);

        const licenseResponse = await page.request.get('/api/v4/license/client?format=old', requestedWith);
        const license = await readJsonOrThrow<Record<string, string>>(licenseResponse, 'Unable to read the client license');
        test.skip(
            license.CustomPermissionsSchemes === 'true' || license.SkuShortName === 'professional',
            'This scenario covers the unlicensed preset-only surface.',
        );

        // # Open the space's compact sharing and permission surface on an unlicensed server.
        await sidebar.goto(teamName);
        await sidebar.openSpace(spaceTitle);
        await spacePage.openShare();
        await share.expectOpen();

        // * The arbitrary checkbox matrix is replaced by the three included presets.
        await share.expectDefaultSummary('Can edit');
        await share.expectDefaultPresetOptions();

        // # Start a draft while Contribute still grants creation. Keep this one browser stale so
        // its already-rendered Publish button can submit to the live gate after the preset changes.
        const staleContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const stalePage = await staleContext.newPage();
            await stalePage.routeWebSocket(/\/api\/v4\/websocket/, () => {});
            await loginAs(stalePage, member.username, member.password);
            const staleSidebar = new SpacesSidebarPage(stalePage);
            const staleSpace = new SpacePage(stalePage);
            await staleSidebar.goto(teamName);
            await staleSidebar.openSpace(spaceTitle);
            await staleSpace.addPage(`Comment denied ${uniqueSuffix()}`);
            await staleSpace.expectDraftRoute();
            await staleSpace.writeBody('This draft must not publish under the Comment preset.');
            await staleSpace.expectDraftSaved();

            // # Select Comment, then close and reopen Share to force a server-backed read.
            await share.chooseDefaultPreset('Comment');
            await share.close();
            await spacePage.openShare();
            await share.expectOpen();
            await share.expectDefaultSummary('Can comment');

            // * The stale authoring UI reaches the real enforcement point and is rejected.
            const deniedResponsePromise = stalePage.waitForResponse((response) => (
                response.request().method() === 'POST' && response.url().includes('/draft/publish')
            ));
            await staleSpace.publish();
            expect((await deniedResponsePromise).status()).toBe(403);
            await staleSpace.expectDraftRoute();
            await expect(stalePage.getByRole('alert').getByText('Could not publish the page. Please try again.', {exact: true})).toBeVisible();
        } finally {
            await staleContext.close();
        }

        const expectMemberCanOnlyRead = async () => {
            const context = await newContext(browser, {baseURL: server.baseURL});
            try {
                const memberPage = await context.newPage();
                const memberSidebar = new SpacesSidebarPage(memberPage);
                const memberSpace = new SpacePage(memberPage);
                await loginAs(memberPage, member.username, member.password);
                await memberSidebar.goto(teamName);
                await memberSidebar.openSpace(spaceTitle);
                await memberSpace.openPageFromTree(pageTitle);
                await memberSpace.expectPageTitle(pageTitle);
                await memberSpace.expectBody(body.heading1);
                await memberSpace.expectBodyReadOnly();
                await expect(memberSpace.addPageButton).toBeHidden();
            } finally {
                await context.close();
            }
        };

        // * Comment still permits reading the existing page but withholds page creation.
        await expectMemberCanOnlyRead();

        // # Select Read only and prove both persistence and its effective read-only outcome.
        await share.chooseDefaultPreset('Read only');
        await share.close();
        await spacePage.openShare();
        await share.expectOpen();
        await share.expectDefaultSummary('Can view');
        await expectMemberCanOnlyRead();

        // # Restore Contribute and force another fresh Share read.
        await share.chooseDefaultPreset('Contribute');
        await share.close();
        await spacePage.openShare();
        await share.expectOpen();
        await share.expectDefaultSummary('Can edit');
        await share.close();

        // * A fresh member now completes a real Add page → autosave → Publish journey.
        const contributeTitle = `Preset publish ${uniqueSuffix()}`;
        const contributeBody = 'Published through the restored Contribute preset.';
        const contributeContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const memberPage = await contributeContext.newPage();
            const memberSidebar = new SpacesSidebarPage(memberPage);
            const memberSpace = new SpacePage(memberPage);
            await loginAs(memberPage, member.username, member.password);
            await memberSidebar.goto(teamName);
            await memberSidebar.openSpace(spaceTitle);
            await memberSpace.addPage(contributeTitle);
            await memberSpace.expectDraftRoute();
            await memberSpace.writeBody(contributeBody);
            await memberSpace.expectDraftSaved();
            await memberSpace.publish();
            await memberSpace.expectPublished();
        } finally {
            await contributeContext.close();
        }

        // * Another authenticated browser reads the persisted page and body.
        const verifyContext = await newContext(browser, {baseURL: server.baseURL});
        try {
            const verifyPage = await verifyContext.newPage();
            const verifySidebar = new SpacesSidebarPage(verifyPage);
            const verifySpace = new SpacePage(verifyPage);
            await loginAs(verifyPage, server.adminUsername, server.adminPassword);
            await verifySidebar.goto(teamName);
            await verifySidebar.openSpace(spaceTitle);
            await verifySpace.openPageFromTree(contributeTitle);
            await verifySpace.expectPageTitle(contributeTitle);
            await verifySpace.expectBody(contributeBody);
        } finally {
            await verifyContext.close();
        }
    });
});
