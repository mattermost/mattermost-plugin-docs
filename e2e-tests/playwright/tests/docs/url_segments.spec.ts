// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import {readFileSync} from 'node:fs';
import {join, resolve} from 'node:path';

import {expect, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {uniqueSuffix} from '../helpers/client';
import {createPage, createSpace, getPageDraft, setSpaceLandingPage, type DocsPage, type Space} from '../helpers/docs';
import {createTeam} from '../helpers/team';
import {DRAFTS_SEGMENT, OVERVIEW_SEGMENT, RESERVED_SEGMENTS, SPACE_OR_PAGE_ID, SPACE_OR_PAGE_ID_PATTERN} from '../data/url_segments';
import {SpacePage} from '../pages/space_page';
import {SpacesSidebarPage} from '../pages/spaces_sidebar_page';

// Playwright transpiles to CommonJS, so import.meta is unavailable.
const repoRoot = resolve(__dirname, '../../../..');

// The id grammar as the app declares it, read from source.
//
// Every other mirrored value here is pinned by a URL assertion: rename a segment and the
// specs below stop finding the screen. The grammar is the one that can drift in silence,
// because server ids are alphanumeric and satisfy a widened or narrowed grammar equally,
// so no URL would ever disagree. Reading the declaration is what makes the copy honest.
function declaredIdGrammar(): string {
    const source = readFileSync(join(repoRoot, 'webapp', 'src', 'routing', 'paths.ts'), 'utf8');
    const declared = (/const SPACE_OR_PAGE_ID = '([^']*)';/).exec(source);

    if (!declared) {
        throw new Error(
            'routing/paths.ts no longer declares SPACE_OR_PAGE_ID as a single-quoted literal. ' +
            'Update this check alongside it rather than dropping it.',
        );
    }

    // What was captured is source text, where a backslash is written twice. Parsing it
    // back to a value keeps the comparison about what the two grammars mean, not how
    // either one is spelled.
    return JSON.parse(`"${declared[1]}"`) as string;
}

// Docs URLs reserve a few segments for things that are not content, and every one of
// them sits exactly where a space or page id goes. What keeps them apart is match order
// plus a leading underscore no id can carry. The unit tests check that against the route
// table; these check it against the router the browser actually runs, where getting it
// wrong shows up as the wrong screen rather than a wrong string.
//
// Not serial: each test owns its own space, so a retry re-runs it alone cleanly.
test.describe('docs URL segments', () => {
    let teamName: string;
    let draftSpace: Space;
    let landingSpace: Space;
    let landingPage: DocsPage;

    test.beforeAll(async ({browser, server}) => {
        const context = await browser.newContext({baseURL: server.baseURL});
        const page = await context.newPage();

        await loginAs(page, server.adminUsername, server.adminPassword);

        const team = await createTeam(page, 'docs-urls');
        teamName = team.name;

        // A space per test: the landing-page test reconfigures the space it uses, and
        // sharing one would leave the drafts test depending on running first.
        draftSpace = await createSpace(page, team.id, `PW Drafts ${uniqueSuffix()}`);
        landingSpace = await createSpace(page, team.id, `PW Landing ${uniqueSuffix()}`);
        landingPage = await createPage(page, landingSpace.id, `PW Landing page ${uniqueSuffix()}`, 'The landing body.');

        await context.close();
    });

    /**
     * @objective Address an unpublished draft by the reserved drafts segment, and reach it again from the URL alone.
     * @precondition A team and an empty space are seeded via the API.
     */
    test('addresses a new draft under the reserved drafts segment', {tag: '@docs'}, async ({page, server}) => {
        const sidebar = new SpacesSidebarPage(page);
        const spacePage = new SpacePage(page);
        const pageTitle = `PW Draft ${uniqueSuffix()}`;

        // # Open the seeded space
        await loginAs(page, server.adminUsername, server.adminPassword);
        await sidebar.goto(teamName);
        await sidebar.openSpace(draftSpace.title);
        await spacePage.expectOpen(draftSpace.title);

        // # Add a page, which starts as an unpublished draft
        await spacePage.addPage(pageTitle);

        // * The whole URL is asserted, so the draft's own page id has to survive as a
        // segment of its own rather than the reserved segment being read as the page.
        // The id is matched by its declared grammar, not by what ids happen to look like
        // today, so a slug carrying a dash would still be read as the page it names
        await expect(page).toHaveURL(
            new RegExp(`/${teamName}/spaces/${draftSpace.id}/${DRAFTS_SEGMENT}/${SPACE_OR_PAGE_ID}\\?edit=1$`),
        );

        // # Let the title reach the server before reloading, so a reload race cannot be
        // mistaken here for a routing failure
        const {pageId} = spacePage.routedIds();
        await expect.poll(
            async () => (await getPageDraft(page, draftSpace.id, pageId))?.title ?? '',
            {message: 'the draft title was never autosaved', timeout: 30_000},
        ).toBe(pageTitle);

        // # Reload, which re-enters the app through the URL alone
        await page.reload();

        // * The reserved segment still resolves to this draft, still in edit mode
        await spacePage.expectDraftRoute();
        await expect(spacePage.draftEditor).toBeVisible();
        await expect(spacePage.pageTitleInput).toHaveValue(pageTitle);
    });

    /**
     * @objective Reach a space's front door through the overview segment, past a default landing page that would redirect away from it.
     * @precondition A space with a published page is seeded via the API.
     */
    test('reaches the front door through the overview segment past a landing page', {tag: '@docs'}, async ({page, server}) => {
        const spacePage = new SpacePage(page);
        const spaceUrl = `/${teamName}/spaces/${landingSpace.id}`;

        await loginAs(page, server.adminUsername, server.adminPassword);

        // # Point the space at a default landing page
        await setSpaceLandingPage(page, landingSpace.id, landingPage.id);

        // # Open the bare space URL
        await page.goto(spaceUrl);

        // * It hands over to the landing page — the redirect the overview segment exists to outrank
        await expect(page).toHaveURL(new RegExp(`${spaceUrl}/${landingPage.id}$`));
        await spacePage.expectPageTitle(landingPage.title);

        // # Ask for the front door explicitly
        await page.goto(`${spaceUrl}/${OVERVIEW_SEGMENT}`);

        // * The request stands: matched ahead of the page route, the segment is neither
        // read as a page id nor overtaken by the landing-page redirect
        await expect(page).toHaveURL(new RegExp(`${spaceUrl}/${OVERVIEW_SEGMENT}$`));
        await spacePage.expectFrontDoor(landingSpace.title);

        // # Reach the front door the way the UI offers it, from the landing page
        await page.goto(`${spaceUrl}/${landingPage.id}`);
        await spacePage.openOverview();

        // * The Overview item addresses the same URL the deep link did
        await expect(page).toHaveURL(new RegExp(`${spaceUrl}/${OVERVIEW_SEGMENT}$`));
        await spacePage.expectFrontDoor(landingSpace.title);
    });

    /**
     * @objective Confirm the server issues ids that cannot collide with a reserved segment.
     * @precondition A space and a page are seeded via the API.
     */
    test('issues ids that cannot shadow a reserved segment', {tag: '@docs'}, () => {
        // The unit tests hold the reserved segments against the id grammar; only a real
        // server can say whether the ids it mints stay inside that grammar. Together they
        // mean no space or page is ever addressed as a reserved segment — which is what
        // lets the routes match those segments first without hiding content.
        expect(landingSpace.id).toMatch(SPACE_OR_PAGE_ID_PATTERN);
        expect(landingPage.id).toMatch(SPACE_OR_PAGE_ID_PATTERN);

        for (const reserved of RESERVED_SEGMENTS) {
            expect(reserved, `${reserved} must not be a legal id`).not.toMatch(SPACE_OR_PAGE_ID_PATTERN);
        }
    });

    /**
     * @objective Hold the mirrored id grammar to the one the app routes by.
     * @precondition None: this reads the webapp source in the checkout under test.
     */
    test('mirrors the id grammar the app routes by', {tag: '@docs'}, () => {
        // Everything above rests on this copy being faithful: the assertion that server
        // ids stay inside the grammar means nothing if the grammar has moved on without
        // it. Changing either declaration alone fails here.
        expect(declaredIdGrammar()).toBe(SPACE_OR_PAGE_ID);
    });
});
