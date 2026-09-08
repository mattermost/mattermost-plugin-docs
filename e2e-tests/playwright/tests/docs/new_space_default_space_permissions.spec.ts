// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

import type {Browser, Page} from '@playwright/test';

import {expect, newContext, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {readJsonOrThrow, requestedWith} from '../helpers/client';
import {addSpaceMember, createPage, createSpace, type Space} from '../helpers/docs';
import {pluginId} from '../helpers/preflight';
import {addUserToTeam, createTeam} from '../helpers/team';
import {createUser, type SeededUser} from '../helpers/user';
import {CreateSpaceModalPage} from '../pages/create_space_modal_page';
import {DocsPluginSettingsPage} from '../pages/docs_plugin_settings_page';
import {SpacePage} from '../pages/space_page';
import {SpaceSettingsModalPage} from '../pages/space_settings_modal_page';
import {SpacesSidebarPage} from '../pages/spaces_sidebar_page';

type ServerConfig = {
    PluginSettings?: {
        Plugins?: Record<string, Record<string, unknown>>;
    };
};

const defaultPresetKey = 'newspacedefaultpreset';
const contributePermissions = ['comment_page', 'create_page', 'edit_page', 'delete_own_page'];

async function getConfiguredPreset(page: Page): Promise<string> {
    const response = await page.request.get('/api/v4/config', requestedWith);
    const config = await readJsonOrThrow<ServerConfig>(response, 'Unable to read the Docs plugin configuration');
    const settings = config.PluginSettings?.Plugins?.[pluginId];
    return String(settings?.[defaultPresetKey] ?? settings?.newSpaceDefaultPreset ?? 'contribute');
}

async function setConfiguredPreset(page: Page, preset: string) {
    const response = await page.request.put('/api/v4/config/patch', {
        ...requestedWith,
        data: {
            PluginSettings: {
                Plugins: {
                    [pluginId]: {[defaultPresetKey]: preset},
                },
            },
        },
    });
    if (!response.ok()) {
        throw new Error(`Unable to restore the Docs new-space default: ${response.status()} ${await response.text()}`);
    }
}

async function getSpace(page: Page, spaceId: string): Promise<Space> {
    const response = await page.request.get(`/plugins/${pluginId}/api/v1/spaces/${spaceId}`, requestedWith);
    return readJsonOrThrow<Space>(response, `Unable to read space ${spaceId}`);
}

async function createSpaceThroughUI(page: Page, teamId: string, teamName: string, title: string): Promise<Space> {
    const sidebar = new SpacesSidebarPage(page);
    const createModal = new CreateSpaceModalPage(page);
    const createURL = `/plugins/${pluginId}/api/v1/teams/${teamId}/spaces`;
    const createResponsePromise = page.waitForResponse((response) =>
        response.request().method() === 'POST' && response.url().includes(createURL),
    );

    await sidebar.goto(teamName);
    await sidebar.openCreateSpace();
    await createModal.expectOpen();
    await createModal.createSpace(title);
    const response = await createResponsePromise;
    if (!response.ok()) {
        throw new Error(`Unable to create ${title} through the UI: ${response.status()} ${await response.text()}`);
    }

    return await response.json() as Space;
}

async function expectMemberCannotAuthor(
    baseURL: string,
    browser: Browser,
    member: SeededUser,
    teamName: string,
    space: Space,
    readableTitle: string,
    readableBody: string,
    adminUsername: string,
    adminPassword: string,
) {
    // # Temporarily grant create_page from the member matrix so this actor can open the real
    // authoring UI, then keep that client deliberately stale while the grant is withdrawn.
    const adminContext = await newContext(browser, {baseURL});
    const staleContext = await newContext(browser, {baseURL});
    try {
        const adminPage = await adminContext.newPage();
        await loginAs(adminPage, adminUsername, adminPassword);
        const adminSidebar = new SpacesSidebarPage(adminPage);
        const settings = new SpaceSettingsModalPage(adminPage);
        await adminSidebar.goto(teamName);
        await adminSidebar.openSpace(space.title);
        await settings.openFromSpaceHeader(space.title);
        await settings.openPermissions();
        await settings.expectMemberPermission(member.id, 'create_page', false);
        await settings.toggleMemberPermission(member.id, 'create_page');
        await settings.close();

        const stalePage = await staleContext.newPage();
        await stalePage.routeWebSocket(/\/api\/v4\/websocket/, () => {});
        await loginAs(stalePage, member.username, member.password);
        const staleSidebar = new SpacesSidebarPage(stalePage);
        const staleSpace = new SpacePage(stalePage);
        await staleSidebar.goto(teamName);
        await staleSidebar.openSpace(space.title);
        await staleSpace.addPage(`Denied in ${space.title}`);
        await staleSpace.expectDraftRoute();
        await staleSpace.writeBody('This must remain an unpublished draft.');
        await staleSpace.expectDraftSaved();

        // # Withdraw the only create grant in the real settings UI, then submit the already-open
        // Publish control. The configured Comment/Read-only default must not supply authority.
        await settings.openFromSpaceHeader(space.title);
        await settings.openPermissions();
        await settings.toggleMemberPermission(member.id, 'create_page');
        await settings.close();

        const deniedResponsePromise = stalePage.waitForResponse((response) =>
            response.request().method() === 'POST' && response.url().includes('/draft/publish'),
        );
        await staleSpace.publish();
        const deniedResponse = await deniedResponsePromise;
        expect(deniedResponse.status()).toBe(403);
        await staleSpace.expectDraftRoute();
        await expect(stalePage.getByRole('alert').getByText('Could not publish the page. Please try again.', {exact: true})).toBeVisible();
    } finally {
        await staleContext.close();
        await adminContext.close();
    }

    // * A fresh member session reads the promised content but exposes no authoring journey.
    const verifyContext = await newContext(browser, {baseURL});
    try {
        const memberPage = await verifyContext.newPage();
        await loginAs(memberPage, member.username, member.password);
        const sidebar = new SpacesSidebarPage(memberPage);
        const spacePage = new SpacePage(memberPage);
        await sidebar.goto(teamName);
        await sidebar.openSpace(space.title);
        await spacePage.openPageFromTree(readableTitle);

        // The preset permits the useful outcome it promises: published content is readable.
        await spacePage.expectPageTitle(readableTitle);
        await spacePage.expectBody(readableBody);
        await spacePage.expectBodyReadOnly();

        // The server denial above and this fresh render cover enforcement and presentation.
        await expect(spacePage.addPageButton).toBeHidden();
    } finally {
        await verifyContext.close();
    }
}

async function expectMemberCanPublish(
    baseURL: string,
    browser: Browser,
    member: SeededUser,
    teamName: string,
    space: Space,
    adminUsername: string,
    adminPassword: string,
) {
    const title = `Published in ${space.title}`;
    const body = `Authored by ${member.username} in ${space.title}.`;
    const authorContext = await newContext(browser, {baseURL});
    try {
        const memberPage = await authorContext.newPage();
        await loginAs(memberPage, member.username, member.password);
        const sidebar = new SpacesSidebarPage(memberPage);
        const spacePage = new SpacePage(memberPage);
        await sidebar.goto(teamName);
        await sidebar.openSpace(space.title);
        await spacePage.addPage(title);
        await spacePage.expectDraftRoute();
        await spacePage.writeBody(body);
        await spacePage.expectDraftSaved();
        await spacePage.publish();
        await spacePage.expectPublished();
    } finally {
        await authorContext.close();
    }

    // A separate administrator session proves the published outcome persisted.
    const verifyContext = await newContext(browser, {baseURL});
    try {
        const verifyPage = await verifyContext.newPage();
        await loginAs(verifyPage, adminUsername, adminPassword);
        const sidebar = new SpacesSidebarPage(verifyPage);
        const spacePage = new SpacePage(verifyPage);
        await sidebar.goto(teamName);
        await sidebar.openSpace(space.title);
        await spacePage.openPageFromTree(title);
        await spacePage.expectPageTitle(title);
        await spacePage.expectBody(body);
    } finally {
        await verifyContext.close();
    }
}

test.describe('new-space default permissions', () => {
    let originalPreset = 'contribute';

    test.beforeEach(async ({page, server}) => {
        await loginAs(page, server.adminUsername, server.adminPassword);
        originalPreset = await getConfiguredPreset(page);
    });

    test.afterEach(async ({page}) => {
        await setConfiguredPreset(page, originalPreset);
    });

    /**
     * @objective A system administrator can choose the preset copied into new Docs spaces.
     *
     * The test changes the setting and creates configured spaces through rendered controls. API
     * calls seed published read fixtures, invite the actor, and independently confirm denials.
     */
    test('copies the configured preset into future spaces without changing existing spaces', {tag: ['@docs', '@permissions']}, async ({page, server, browser}) => {
        test.setTimeout(180_000);

        const pluginSettings = new DocsPluginSettingsPage(page);
        const sidebar = new SpacesSidebarPage(page);
        const createSpaceModal = new CreateSpaceModalPage(page);
        const team = await createTeam(page, 'docs-new-space-default');
        const member = await createUser(page, 'docs-new-space-member');
        await addUserToTeam(page, team.id, member.id);

        // # Select Comment as the site template in System Console and save it.
        await pluginSettings.goto();
        await pluginSettings.choosePreset('Comment');
        await pluginSettings.save();
        await pluginSettings.expectPreset('Comment');

        // # Create the first space through the product UI, not the fixture API.
        const createURL = '/plugins/' + pluginId + '/api/v1/teams/' + team.id + '/spaces';
        const createResponsePromise = page.waitForResponse((response) =>
            response.request().method() === 'POST' && response.url().includes(createURL),
        );
        await sidebar.goto(team.name);
        await sidebar.openCreateSpace();
        await createSpaceModal.expectOpen();
        await createSpaceModal.createSpace('Comment default');
        const createResponse = await createResponsePromise;

        // * The UI omits default_permissions and the server applies the configured seeded preset.
        expect(createResponse.request().postDataJSON()).not.toHaveProperty('default_permissions');
        if (!createResponse.ok()) {
            throw new Error('Unable to create a space through the UI: ' + await createResponse.text());
        }
        const commentSpace = await createResponse.json() as Space;
        expect(commentSpace.default_permissions).toEqual(['comment_page']);

        // * Comment allows the invited member to read published content but not create pages.
        await addSpaceMember(page, commentSpace.id, member.id);
        const commentReadTitle = 'Comment preset content';
        const commentReadBody = 'The comment preset still grants page reading.';
        await createPage(page, commentSpace.id, commentReadTitle, commentReadBody);
        await expectMemberCannotAuthor(
            server.baseURL,
            browser,
            member,
            team.name,
            commentSpace,
            commentReadTitle,
            commentReadBody,
            server.adminUsername,
            server.adminPassword,
        );

        // # Change the site template to Read only.
        await pluginSettings.goto();
        await pluginSettings.choosePreset('Read only');
        await pluginSettings.save();
        await pluginSettings.expectPreset('Read only');

        // * The existing space keeps its copied default, while the next space uses Read only.
        const unchanged = await getSpace(page, commentSpace.id);
        expect(unchanged.default_permissions).toEqual(['comment_page']);
        const readOnlySpace = await createSpaceThroughUI(page, team.id, team.name, 'Read-only default');
        expect(readOnlySpace.default_permissions).toEqual([]);
        expect((await getSpace(page, readOnlySpace.id)).default_permissions).toEqual([]);

        // * Read-only likewise renders real content but denies page creation.
        await addSpaceMember(page, readOnlySpace.id, member.id);
        const readOnlyTitle = 'Read-only preset content';
        const readOnlyBody = 'The read-only preset renders this published body.';
        await createPage(page, readOnlySpace.id, readOnlyTitle, readOnlyBody);
        await expectMemberCannotAuthor(
            server.baseURL,
            browser,
            member,
            team.name,
            readOnlySpace,
            readOnlyTitle,
            readOnlyBody,
            server.adminUsername,
            server.adminPassword,
        );

        // * An explicit per-space value still overrides the site template and lets the invited
        // member complete a real authoring journey.
        const overridden = await createSpace(page, team.id, 'Explicit contribute', contributePermissions);
        expect(overridden.default_permissions).toHaveLength(contributePermissions.length);
        expect(overridden.default_permissions).toEqual(expect.arrayContaining(contributePermissions));
        expect((await getSpace(page, overridden.id)).default_permissions).toEqual(expect.arrayContaining(contributePermissions));
        await addSpaceMember(page, overridden.id, member.id);
        await expectMemberCanPublish(
            server.baseURL,
            browser,
            member,
            team.name,
            overridden,
            server.adminUsername,
            server.adminPassword,
        );

        // # Select Contribute as the site template and save it.
        await pluginSettings.goto();
        await pluginSettings.choosePreset('Contribute');
        await pluginSettings.save();
        await pluginSettings.expectPreset('Contribute');

        // * The third configured preset survives scheme-backed readback and grants effective
        // authoring through the rendered editor.
        const contributeSpace = await createSpaceThroughUI(page, team.id, team.name, 'Contribute default');
        expect((await getSpace(page, contributeSpace.id)).default_permissions).toEqual(expect.arrayContaining(contributePermissions));
        await addSpaceMember(page, contributeSpace.id, member.id);
        await expectMemberCanPublish(
            server.baseURL,
            browser,
            member,
            team.name,
            contributeSpace,
            server.adminUsername,
            server.adminPassword,
        );
    });
});
