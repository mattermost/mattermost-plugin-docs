// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// ***************************************************************
// - [#] indicates a test step (e.g. # Go to a page)
// - [*] indicates an assertion (e.g. * Check the title)
// ***************************************************************

// API-driven: comments have no webapp surface yet, so these specs exercise the plugin's
// comment routes end-to-end against the real server — the seam the client will sit on.

import type {Page} from '@playwright/test';

import {expect, test} from '../fixtures';
import {loginAs} from '../helpers/auth';
import {readJsonOrThrow, requestedWith, uniqueSuffix} from '../helpers/client';
import {
    addSpaceMember,
    createComment,
    createCommentReply,
    createCommentResponse,
    createPage,
    createSpace,
    deleteCommentResponse,
    getComment,
    getCommentResponse,
    listComments,
    listCommentReplies,
    listCommentsResponse,
    movePageToSpace,
    patchComment,
    patchCommentResponse,
    resolveComment,
    resolveCommentResponse,
    type DocsPage,
    type Space,
} from '../helpers/docs';
import {addUserToTeam, createTeam, type Team} from '../helpers/team';
import {createUser} from '../helpers/user';

// Each test seeds its own team, space, and page, so a retry never inherits another
// attempt's comments and the tests stay independent.
async function seedPage(page: Page, teamPrefix: string): Promise<{team: Team; space: Space; docsPage: DocsPage}> {
    const team = await createTeam(page, teamPrefix);
    const space = await createSpace(page, team.id, `PW Comments ${uniqueSuffix()}`);
    const docsPage = await createPage(page, space.id, `PW Commented Page ${uniqueSuffix()}`);

    return {team, space, docsPage};
}

test.describe('page comments API', () => {
    /**
     * @objective Walk a comment thread through its whole life: create both kinds of root,
     * reply, filter, resolve and unresolve with attribution, and delete through the
     * live-replies guard.
     * @precondition A team, space, page, and a second space member are seeded via the API.
     */
    test('runs a comment thread from create to guarded delete', {tag: '@docs'}, async ({browser, page, server}) => {
        // # Seed a page as the admin and a second member of its space
        await loginAs(page, server.adminUsername, server.adminPassword);
        const admin = await readJsonOrThrow<{id: string}>(
            await page.request.get('/api/v4/users/me', requestedWith), 'Unable to read the admin user');
        const {team, space, docsPage} = await seedPage(page, 'docs-comments');
        const member = await createUser(page, 'docs-commenter');
        await addUserToTeam(page, team.id, member.id);
        await addSpaceMember(page, space.id, member.id);

        const memberContext = await browser.newContext({baseURL: server.baseURL});
        const memberPage = await memberContext.newPage();
        await loginAs(memberPage, member.username, member.password);

        try {
            // # Create a footer comment
            const footer = await createComment(memberPage, space.id, docsPage.id, {message: 'A footer comment'});

            // * It starts unresolved, reply-less, and typed as footer
            expect(footer.comment_type).toBe('footer');
            expect(footer.root_id).toBe('');
            expect(footer.reply_count).toBe(0);
            expect(footer.resolved).toBe(false);
            expect(footer.space_id).toBe(space.id);
            expect(footer.page_id).toBe(docsPage.id);
            expect(footer.user_id).toBe(member.id);

            // # Create an inline comment anchored to a document marker
            const anchor = `anchor-${uniqueSuffix()}`;
            const inline = await createComment(page, space.id, docsPage.id, {
                message: 'An inline comment',
                comment_type: 'inline',
                anchor_id: anchor,
            });

            // * The anchor is echoed back
            expect(inline.comment_type).toBe('inline');
            expect(inline.anchor_id).toBe(anchor);

            // * Each half-state of inline-vs-anchor is refused
            const anchorless = await createCommentResponse(page, space.id, docsPage.id, {message: 'x', comment_type: 'inline'});
            expect(anchorless.status()).toBe(400);
            const anchoredFooter = await createCommentResponse(page, space.id, docsPage.id, {message: 'x', anchor_id: anchor});
            expect(anchoredFooter.status()).toBe(400);
            const empty = await createCommentResponse(page, space.id, docsPage.id, {message: '   '});
            expect(empty.status()).toBe(400);

            // # Reply to the footer comment as the second member
            const reply = await createCommentReply(memberPage, space.id, docsPage.id, footer.id, 'A reply');

            // * The reply threads under the root and inherits its kind
            expect(reply.root_id).toBe(footer.id);
            expect(reply.comment_type).toBe('footer');
            expect(reply.user_id).toBe(member.id);

            // * The roots listing shows both roots, with the reply counted but not listed
            const roots = await listComments(page, space.id, docsPage.id);
            expect(roots.items.map((c) => c.id).sort()).toEqual([footer.id, inline.id].sort());
            expect(roots.items.find((c) => c.id === footer.id)?.reply_count).toBe(1);

            // * The replies listing carries the reply alone
            const replies = await listCommentReplies(page, space.id, docsPage.id, footer.id);
            expect(replies.items.map((c) => c.id)).toEqual([reply.id]);

            // * The comment_type filter narrows to the inline root
            const inlineOnly = await listComments(page, space.id, docsPage.id, {comment_type: 'inline'});
            expect(inlineOnly.items.map((c) => c.id)).toEqual([inline.id]);

            // # Resolve the footer thread as the second member
            const resolved = await resolveComment(memberPage, space.id, docsPage.id, footer.id, true);

            // * Resolution is attributed to the resolver
            expect(resolved.resolved).toBe(true);
            expect(resolved.resolved_by).toBe(member.id);
            expect(resolved.resolved_at).toBeGreaterThan(0);

            // * The resolved filter splits the two roots
            const resolvedRoots = await listComments(page, space.id, docsPage.id, {resolved: true});
            expect(resolvedRoots.items.map((c) => c.id)).toEqual([footer.id]);
            const openRoots = await listComments(page, space.id, docsPage.id, {resolved: false});
            expect(openRoots.items.map((c) => c.id)).toEqual([inline.id]);

            // # Unresolve it as the admin
            const unresolved = await resolveComment(page, space.id, docsPage.id, footer.id, false);

            // * Reopening is attributed too — to the actor who reopened, not the thread's author
            expect(unresolved.resolved).toBe(false);
            expect(unresolved.resolved_by).toBe(admin.id);
            expect(unresolved.resolved_at).toBeGreaterThan(0);

            // * A reply cannot be resolved
            const replyPatch = await resolveCommentResponse(page, space.id, docsPage.id, reply.id, true);
            expect(replyPatch.status()).toBe(400);

            // # The member edits their own reply
            const edited = await patchComment(memberPage, space.id, docsPage.id, reply.id, {message: 'An edited reply'});

            // * The new text stands, stamped as an edit, with the thread placement intact
            expect(edited.message).toBe('An edited reply');
            expect(edited.edit_at).toBeGreaterThan(0);
            expect(edited.root_id).toBe(footer.id);
            expect(edited.comment_type).toBe('footer');

            // * Someone else's comment cannot be rewritten, even by the space's other members
            const foreignEdit = await patchCommentResponse(page, space.id, docsPage.id, reply.id, {message: 'hijack'});
            expect(foreignEdit.status()).toBe(403);

            // * The listing serves the edited text exactly once
            const editedReplies = await listCommentReplies(page, space.id, docsPage.id, footer.id);
            expect(editedReplies.items.map((c) => c.message)).toEqual(['An edited reply']);

            // # The author tries to delete the root while its reply is live
            const guarded = await deleteCommentResponse(memberPage, space.id, docsPage.id, footer.id);

            // * The delete is refused, naming how many replies it would destroy
            expect(guarded.status()).toBe(409);
            const conflict = await guarded.json() as {reply_count: number};
            expect(conflict.reply_count).toBe(1);

            // # The space creator moderates the same root
            const forced = await deleteCommentResponse(page, space.id, docsPage.id, footer.id);

            // * Creator moderation goes through and takes the reply with it
            expect(forced.ok()).toBe(true);
            expect((await getCommentResponse(page, space.id, docsPage.id, footer.id)).status()).toBe(404);
            expect((await getCommentResponse(page, space.id, docsPage.id, reply.id)).status()).toBe(404);

            // * Only the inline root remains listed
            const remaining = await listComments(page, space.id, docsPage.id);
            expect(remaining.items.map((c) => c.id)).toEqual([inline.id]);

            // # The author deletes the reply-less inline root
            const last = await deleteCommentResponse(page, space.id, docsPage.id, inline.id);

            // * An author delete without live replies needs no guard
            expect(last.ok()).toBe(true);
            expect((await listComments(page, space.id, docsPage.id)).items).toEqual([]);
        } finally {
            await memberContext.close();
        }
    });

    /**
     * @objective Page through comment roots with the keyset cursor.
     * @precondition A team, space, and page are seeded via the API.
     */
    test('walks the roots listing with the after cursor', {tag: '@docs'}, async ({page, server}) => {
        // # Seed a page and three root comments
        await loginAs(page, server.adminUsername, server.adminPassword);
        const {space, docsPage} = await seedPage(page, 'docs-comment-cursor');
        const created: string[] = [];
        for (const n of [1, 2, 3]) {
            created.push((await createComment(page, space.id, docsPage.id, {message: `Root ${n}`})).id);
        }

        // # Read the first window of two
        const first = await listComments(page, space.id, docsPage.id, {per_page: 2});

        // * It is full and points at the next window
        expect(first.items).toHaveLength(2);
        expect(first.has_more).toBe(true);
        expect(first.next_after).toBeTruthy();

        // # Follow the cursor
        const second = await listComments(page, space.id, docsPage.id, {per_page: 2, after: first.next_after});

        // * The walk ends after one more root
        expect(second.items).toHaveLength(1);
        expect(second.has_more).toBe(false);
        expect(second.next_after).toBeUndefined();

        // * Together the windows return each created root exactly once, ordered oldest-first.
        // Two roots may share a CreateAt millisecond (the id breaks the tie), so the order
        // asserted is the listing's, not the creation sequence's.
        const walked = [...first.items, ...second.items];
        expect(walked.map((c) => c.id).sort()).toEqual([...created].sort());
        for (let i = 1; i < walked.length; i++) {
            expect(walked[i].create_at).toBeGreaterThanOrEqual(walked[i - 1].create_at);
        }

        // * A malformed cursor is refused rather than treated as the first window
        const badCursor = await listCommentsResponse(page, space.id, docsPage.id, {after: 'not-a-cursor'});
        expect(badCursor.status()).toBe(400);
    });

    /**
     * @objective Move a commented page to another space and keep its thread reachable
     * there — and only there.
     * @precondition A team with two spaces and a commented page is seeded via the API.
     */
    test('keeps comments with a page moved to another space', {tag: '@docs'}, async ({page, server}) => {
        // # Seed a commented page in one space and a second space beside it
        await loginAs(page, server.adminUsername, server.adminPassword);
        const {team, space: source, docsPage} = await seedPage(page, 'docs-comment-move');
        const target = await createSpace(page, team.id, `PW Move Target ${uniqueSuffix()}`);
        const root = await createComment(page, source.id, docsPage.id, {message: 'Travels with the page'});
        await createCommentReply(page, source.id, docsPage.id, root.id, 'So does the reply');

        // # Move the page to the second space
        const moved = await movePageToSpace(page, source.id, docsPage.id, target.id);

        // * The page now lives in the target space
        expect(moved.space_id).toBe(target.id);

        // * The thread is served under the target space, reply count intact
        const listed = await listComments(page, target.id, docsPage.id);
        expect(listed.items.map((c) => c.id)).toEqual([root.id]);
        expect(listed.items[0].reply_count).toBe(1);
        expect(listed.items[0].space_id).toBe(target.id);
        expect((await getComment(page, target.id, docsPage.id, root.id)).id).toBe(root.id);

        // * The source space no longer serves the page's comments
        expect((await listCommentsResponse(page, source.id, docsPage.id)).status()).toBe(404);
    });
});
