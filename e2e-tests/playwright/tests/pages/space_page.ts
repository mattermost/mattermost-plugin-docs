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

        // "Add page" appears both in the page header and in the page tree panel, so
        // it is scoped to the first match rather than left ambiguous.
        this.addPageButton = page.getByRole('button', {name: 'Add page'}).first();
        this.pageTree = page.getByRole('tree', {name: 'Pages'});
        this.pageTitleInput = page.getByLabel('Page title');

        // The host's editor tags its own contenteditable with these ids, naming the
        // draft and published surfaces separately. Both also carry role=textbox, which
        // the page title shares — hence the id rather than the role.
        this.draftEditor = page.getByTestId('docs-draft-editor');
        this.publishedEditor = page.getByTestId('docs-page-editor');

        // The body surface in either mode, so format assertions read the same whether
        // the page is still a draft or already published.
        this.bodySurface = page.locator('[data-testid="docs-draft-editor"], [data-testid="docs-page-editor"]');

        // data-status belongs to the autosave indicator alone; three other elements on
        // the page use role=status.
        this.autosaveStatus = page.locator('[role="status"][data-status]');

        // Exact, so a future "Publish changes"-style button cannot make this resolve
        // to two nodes and fail on strict mode instead of on the real condition.
        this.publishButton = page.getByRole('button', {name: 'Publish', exact: true});
        this.editButton = page.getByRole('button', {name: 'Edit', exact: true});
        this.shareButton = page.getByRole('button', {name: 'Share', exact: true});
    }

    async expectOpen(spaceTitle: string) {
        await expect(this.page.getByText(spaceTitle).first()).toBeVisible();
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

        // Commit the title so it is persisted to the draft before publishing.
        await this.pageTitleInput.blur();
    }

    async expectDraftRoute() {
        await expect(this.page).toHaveURL(/\/drafts\//);
    }

    // Typed as keystrokes rather than set with fill(): the body is a ProseMirror
    // surface, which builds its document from input events.
    async writeBody(text: string) {
        await this.draftEditor.click();
        await this.draftEditor.pressSequentially(text);
    }

    async expectDraftBody(text: string) {
        await expect(this.draftEditor).toContainText(text);
    }

    // Types a body covering the editor's text formats. The block order is deliberate:
    // structures that swallow Enter (lists, blockquote, code block) need an explicit
    // exit, and the code block — which only exits on triple Enter or arrow-down — is
    // written last so it needs none.
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

        // Enter inside a blockquote opens another paragraph within it; a second Enter
        // on that empty paragraph lifts back out.
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

        // The fence needs trailing whitespace to fire, and it reads whatever precedes
        // that whitespace as the language — so the space is pressed on its own. Typing
        // the code straight after the backticks would make its first word the language.
        await editor.pressSequentially('```');
        await editor.press('Space');
        await editor.pressSequentially(content.code);
    }

    // Each format as its rendered element, so a spec asserts on structure rather than
    // on the markdown that produced it.
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

    // The routed space and page, read back from the URL so a caller can address the
    // page over the API without having been told the ids.
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

    // A reader gets the content, not an editable surface.
    async expectBodyReadOnly() {
        await expect(this.publishedEditor).toHaveAttribute('aria-disabled', 'true');
    }

    async publish() {
        await this.publishButton.click();
    }

    // Asserts on positive signals of the published state — the page left the draft
    // route and now offers Edit — rather than on the absence of the Publish button,
    // which a missing or renamed locator would also satisfy.
    async expectPublished() {
        await expect(this.page).not.toHaveURL(/\/drafts\//);
        await expect(this.editButton).toBeVisible();
    }

    async openShare() {
        await this.shareButton.click();
    }

    // In read mode the title is a heading; the editable input exists only while
    // the page is being edited.
    async expectPageTitle(title: string) {
        await expect(this.page.getByRole('heading', {name: title, level: 1})).toBeVisible();
    }
}
