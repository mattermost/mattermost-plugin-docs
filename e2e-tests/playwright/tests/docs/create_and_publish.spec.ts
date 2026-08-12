// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import {expect, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {uniqueSuffix} from '../helpers/client';
import {getPageDraft} from '../helpers/docs';
import {addUserToTeam, createTeam} from '../helpers/team';
import {createUser, type SeededUser} from '../helpers/user';
import {richText, type RichText} from '../data/rich_text';
import {CreateSpaceModalPage} from '../pages/create_space_modal_page';
import {DocsSwitcherPage} from '../pages/docs_switcher_page';
import {ShareSpaceModalPage} from '../pages/share_space_modal_page';
import {SpacePage} from '../pages/space_page';
import {SpacesSidebarPage} from '../pages/spaces_sidebar_page';
test.use({video: 'on'})

// Serial: the second test opens the page the first publishes, and a plain describe
// would retry it alone against a freshly seeded team.
test.describe.serial('docs authoring', () => {
    let spaceTitle: string;
    let pageTitle: string;
    let body: RichText;
    let teamName: string;
    let member: SeededUser;
    let publishedPageUrl: string;


    test.beforeAll(async ({browser, server}) => {
        const context = await browser.newContext({baseURL: server.baseURL});
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

        // The visible indicator is not a barrier: it still reads "saved" from the title
        // save, so it is satisfied before the body is even sent.
        const {spaceId, pageId} = spacePage.routedIds();
        await expect.poll(
            async () => (await getPageDraft(page, spaceId, pageId))?.body ?? '',
            {message: 'draft body was never autosaved'},
        ).toContain(body.code);

        // * The editor agrees the draft is saved
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
});
