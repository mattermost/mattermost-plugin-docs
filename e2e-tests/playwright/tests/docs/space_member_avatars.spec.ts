// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import type {Page} from '@playwright/test';

import {expect, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {uniqueSuffix} from '../helpers/client';
import {addSpaceMember, createSpace} from '../helpers/docs';
import {addUserToTeam, createTeam} from '../helpers/team';
import {createUser, type SeededUser} from '../helpers/user';
import {SpacePage} from '../pages/space_page';

// Core's Avatars shows every avatar up to four and switches to three plus a "+N"
// chip beyond that, so the two spaces below sit deliberately either side of that
// boundary. Seeded over the API — the subject is the stack, not space creation.
const CROWDED_MEMBERS = 5;
const SMALL_MEMBERS = 1;

// The stack is core's, published on window.Components. Hosts predating that export
// make Docs render its own fallback stack, whose counts and popover behaviour these
// assertions don't describe — so skip rather than fail against an older image.
async function openSpace(page: Page, space: {id: string; title: string}, teamName: string) {
    const spacePage = new SpacePage(page);

    await page.goto(`/${teamName}/spaces/${space.id}`);
    await spacePage.expectOpen(space.title);

    const published = await page.evaluate(
        () => Boolean((window as unknown as {Components?: {Avatars?: unknown}}).Components?.Avatars),
    );

    test.skip(!published, 'Host does not publish Components.Avatars; Docs is on its fallback stack (MM-70358)');

    return spacePage;
}

test.describe('space member avatars', () => {
    let teamName: string;
    let crowdedSpace: {id: string; title: string};
    let smallSpace: {id: string; title: string};

    test.beforeAll(async ({browser, server}) => {
        const context = await browser.newContext({baseURL: server.baseURL});
        const page = await context.newPage();

        await loginAs(page, server.adminUsername, server.adminPassword);

        const team = await createTeam(page, 'docs-avatars');
        teamName = team.name;

        const seedMembers = async (spaceId: string, count: number) => {
            for (let i = 0; i < count; i++) {
                // Serial: each call needs the previous user's id, and the counts are small.
                // eslint-disable-next-line no-await-in-loop
                const user: SeededUser = await createUser(page, 'docs-avatar');

                // eslint-disable-next-line no-await-in-loop
                await addUserToTeam(page, team.id, user.id);

                // eslint-disable-next-line no-await-in-loop
                await addSpaceMember(page, spaceId, user.id);
            }
        };

        crowdedSpace = await createSpace(page, team.id, `PW Crowded ${uniqueSuffix()}`);
        await seedMembers(crowdedSpace.id, CROWDED_MEMBERS);

        smallSpace = await createSpace(page, team.id, `PW Small ${uniqueSuffix()}`);
        await seedMembers(smallSpace.id, SMALL_MEMBERS);

        await context.close();
    });

    test.beforeEach(async ({page, server}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
    });

    /**
     * @objective The overflow chip is opaque, so the avatar it overlaps does not show through.
     * @precondition A space with more members than the stack displays.
     */
    test('renders an opaque overflow chip', {tag: '@docs'}, async ({page}) => {
        // # Open the crowded space's overview
        const spacePage = await openSpace(page, crowdedSpace, teamName);

        // * Core caps the stack at three once the total passes four
        await expect(spacePage.memberAvatarImages()).toHaveCount(3);

        // * The chip is filled, not translucent (MM-70358)
        await spacePage.expectOverflowChipOpaque();
    });

    /**
     * @objective A space small enough to show every member renders no overflow chip.
     * @precondition A space with at most four members.
     */
    test('omits the overflow chip when every member fits', {tag: '@docs'}, async ({page}) => {
        // # Open the small space's overview
        const spacePage = await openSpace(page, smallSpace, teamName);

        // * Every member is shown
        await expect(spacePage.memberAvatarImages().first()).toBeVisible();

        // * No "+N" chip
        await expect(spacePage.memberOverflowChip).toHaveCount(0);
    });

    /**
     * @objective Member avatars are interactive, which the hand-rolled stack was not.
     * @precondition A space with at least one member.
     */
    test('opens a profile popover from a member avatar', {tag: '@docs'}, async ({page}) => {
        // # Open the crowded space's overview
        const spacePage = await openSpace(page, crowdedSpace, teamName);

        // # Click the first member avatar
        await spacePage.openMemberProfile();

        // * The host's profile popover opens
        await expect(spacePage.profilePopover).toBeVisible();
    });

    /**
     * @objective Member avatars are keyboard reachable.
     * @precondition A space with at least one member.
     */
    test('focuses a member avatar by keyboard', {tag: '@docs'}, async ({page}) => {
        // # Open the crowded space's overview
        const spacePage = await openSpace(page, crowdedSpace, teamName);

        // # Move focus onto the first avatar's popover trigger
        const trigger = spacePage.memberAvatars.getByRole('button').first();
        await trigger.focus();

        // * It took focus rather than being an inert span
        await expect(trigger).toBeFocused();

        // # Activate it from the keyboard
        await page.keyboard.press('Enter');

        // * The popover opens without a pointer
        await expect(spacePage.profilePopover).toBeVisible();
    });
});
