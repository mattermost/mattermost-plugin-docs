// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

import type {RichText} from '../data/rich_text';

export class SpacePage {
    readonly page: Page;
    readonly addPageButton: Locator;
    readonly pageTree: Locator;
    readonly pageTitleInput: Locator;
    readonly draftEditor: Locator;
    readonly publishedEditor: Locator;
    readonly bodySurface: Locator;
    readonly autosaveStatus: Locator;
    readonly publishButton: Locator;
    readonly editButton: Locator;
    readonly shareButton: Locator;

    constructor(page: Page) {
        this.page = page;

        // Appears in both the page header and the tree panel.
        this.addPageButton = page.getByRole('button', {name: 'Add page'}).first();
        this.pageTree = page.getByRole('tree', {name: 'Pages'});
        this.pageTitleInput = page.getByLabel('Page title');

        // By id, not role: the page title is also a textbox.
        this.draftEditor = page.getByTestId('docs-draft-editor');
        this.publishedEditor = page.getByTestId('docs-page-editor');

        // Either mode, so format assertions read the same before and after publishing.
        this.bodySurface = page.locator('[data-testid="docs-draft-editor"], [data-testid="docs-page-editor"]');

        // data-status is unique to the autosave indicator; role=status is not.
        this.autosaveStatus = page.locator('[role="status"][data-status]');

        // Exact, so a future "Publish changes" button cannot make this ambiguous.
        this.publishButton = page.getByRole('button', {name: 'Publish', exact: true});
        this.editButton = page.getByRole('button', {name: 'Edit', exact: true});
        this.shareButton = page.getByRole('button', {name: 'Share', exact: true});
    }

    // The space header's title trigger, scoped to main and matched exactly: the sidebar row, the
    // switcher result just clicked and a toast all carry the same title, so an unscoped match
    // resolves to whichever paints first — reporting the space as open before the space view had
    // mounted and leaving every later action racing an unready page.
    spaceTitleTrigger(spaceTitle: string): Locator {
        return this.page.getByRole('main').getByRole('button', {name: spaceTitle, exact: true});
    }

    async expectOpen(spaceTitle: string) {
        await expect(this.page).toHaveURL(/\/spaces\/[^/?#]+/);
        await expect(this.spaceTitleTrigger(spaceTitle)).toBeVisible();
    }

    pageTreeLink(title: string): Locator {
        return this.pageTree.getByRole('link', {name: title});
    }

    async openPageFromTree(title: string) {
        await this.pageTreeLink(title).click();
    }

    async addPage(title: string) {
        await this.addPageButton.click();
        await this.pageTitleInput.waitFor();
        await this.pageTitleInput.fill(title);

        // Commit the title before publishing.
        await this.pageTitleInput.blur();
    }

    async expectDraftRoute() {
        await expect(this.page).toHaveURL(/\/drafts\//);
    }

    // Keystrokes, not fill(): ProseMirror builds its document from input events.
    async writeBody(text: string) {
        await this.draftEditor.click();
        await this.draftEditor.pressSequentially(text);
    }

    async expectDraftBody(text: string) {
        await expect(this.draftEditor).toContainText(text);
    }

    // Block order matters: lists and blockquote swallow Enter and need an explicit exit,
    // and the code block goes last because it only exits on triple Enter or arrow-down.
    async writeRichBody(content: RichText) {
        const editor = this.draftEditor;
        await editor.click();

        await editor.pressSequentially(`# ${content.heading1}`);
        await editor.press('Enter');

        await editor.pressSequentially(`## ${content.heading2}`);
        await editor.press('Enter');

        await editor.pressSequentially(`**${content.bold}**`);
        await editor.press('Enter');

        await editor.pressSequentially(`*${content.italic}*`);
        await editor.press('Enter');

        await editor.pressSequentially(`~~${content.strike}~~`);
        await editor.press('Enter');

        await editor.pressSequentially(`\`${content.inlineCode}\``);
        await editor.press('Enter');

        // Second Enter lifts out of the blockquote.
        await editor.pressSequentially(`> ${content.quote}`);
        await editor.press('Enter');
        await editor.press('Enter');

        await editor.pressSequentially(`- ${content.bullets[0]}`);
        await editor.press('Enter');
        await editor.pressSequentially(content.bullets[1]);
        await editor.press('Enter');
        await editor.press('Enter');

        await editor.pressSequentially(`1. ${content.ordered[0]}`);
        await editor.press('Enter');
        await editor.pressSequentially(content.ordered[1]);
        await editor.press('Enter');
        await editor.press('Enter');

        await editor.pressSequentially('---');

        // The fence fires on trailing whitespace and reads what precedes it as the
        // language, so typing the code directly would make its first word the language.
        await editor.pressSequentially('```');
        await editor.press('Space');
        await editor.pressSequentially(content.code);
    }

    // Rendered elements, so specs assert structure rather than the markdown behind it.
    bodyFormats() {
        return {
            heading1: this.bodySurface.locator('h1'),
            heading2: this.bodySurface.locator('h2'),
            bold: this.bodySurface.locator('strong'),
            italic: this.bodySurface.locator('em'),
            strike: this.bodySurface.locator('s'),
            inlineCode: this.bodySurface.locator('p code'),
            quote: this.bodySurface.locator('blockquote'),
            bulletItems: this.bodySurface.locator('ul li'),
            orderedItems: this.bodySurface.locator('ol li'),
            rule: this.bodySurface.locator('hr'),
            codeBlock: this.bodySurface.locator('pre code'),
        };
    }

    async expectDraftSaved() {
        await expect(this.autosaveStatus).toHaveAttribute('data-status', 'saved');
    }

    // Read from the URL so a caller can address the page over the API.
    routedIds(): {spaceId: string; pageId: string} {
        const match = (/\/spaces\/([^/]+)\/(?:drafts\/)?([^/?#]+)/).exec(this.page.url());

        if (!match) {
            throw new Error(`Not on a page route: ${this.page.url()}`);
        }

        return {spaceId: match[1], pageId: match[2]};
    }

    async expectBody(text: string) {
        await expect(this.publishedEditor).toContainText(text);
    }

    async expectBodyReadOnly() {
        await expect(this.publishedEditor).toHaveAttribute('aria-disabled', 'true');
    }

    async publish() {
        await this.publishButton.click();
    }

    // Positive signals: a missing or renamed Publish button would satisfy its absence.
    async expectPublished() {
        await expect(this.page).not.toHaveURL(/\/drafts\//);
        await expect(this.editButton).toBeVisible();
    }

    async openShare() {
        await this.shareButton.click();
    }

    // Read mode renders the title as a heading; the input exists only while editing.
    async expectPageTitle(title: string) {
        await expect(this.page.getByRole('heading', {name: title, level: 1})).toBeVisible();
    }
}
