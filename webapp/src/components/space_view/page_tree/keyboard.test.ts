// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {buildPageTree} from 'store/page_tree';
import {makeDraft, makePage} from 'store/test_fixtures';

import type {Page} from 'types/docs';

import {MAX_PAGE_LEVELS, buildSubtreeHeightMap} from './depth';
import {flattenVisibleRows, resolveReorder} from './keyboard';

const child = (id: string, parentId: string, sortOrder = 0): Page => ({
    ...makePage(id, 'space1', id, sortOrder),
    parent_id: parentId,
});

// a
//   a1
//   a2
// b
const pages: Page[] = [
    makePage('a', 'space1', 'a', 0),
    child('a1', 'a', 0),
    child('a2', 'a', 1),
    makePage('b', 'space1', 'b', 1),
];

const treeOf = (list: Page[]) => {
    const roots = buildPageTree(list);
    return {roots, heights: buildSubtreeHeightMap(roots)};
};

// A chain of `levels` pages, each nested in the previous one.
const chain = (levels: number): Page[] =>
    Array.from({length: levels}, (_, i) => (i === 0 ? makePage('p0', 'space1', 'p0') : child(`p${i}`, `p${i - 1}`)));

describe('flattenVisibleRows', () => {
    it('lists rows in visual order with their parent', () => {
        const {roots} = treeOf(pages);

        expect(flattenVisibleRows(roots, new Set()).map((row) => [row.node.id, row.parentId])).toEqual([
            ['a', ''],
            ['a1', 'a'],
            ['a2', 'a'],
            ['b', ''],
        ]);
    });

    it('omits the subtree of a collapsed node', () => {
        const {roots} = treeOf(pages);

        expect(flattenVisibleRows(roots, new Set(['a'])).map((row) => row.node.id)).toEqual(['a', 'b']);
    });
});

describe('resolveReorder', () => {
    it('moves a page up and down within its sibling group', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'a2', 'up')).toEqual({move: {pageId: 'a2', parentId: 'a', siblingIndex: 0}});
        expect(resolveReorder(roots, heights, 'a1', 'down')).toEqual({move: {pageId: 'a1', parentId: 'a', siblingIndex: 1}});
    });

    it('refuses to move past the ends of the sibling group', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'a1', 'up')).toEqual({blocked: 'boundary'});
        expect(resolveReorder(roots, heights, 'a2', 'down')).toEqual({blocked: 'boundary'});
    });

    it('indents under the preceding sibling as its last child', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'b', 'indent')).toEqual({move: {pageId: 'b', parentId: 'a', siblingIndex: 2}});
    });

    it('refuses to indent the first of a sibling group', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'a', 'indent')).toEqual({blocked: 'boundary'});
        expect(resolveReorder(roots, heights, 'a1', 'indent')).toEqual({blocked: 'boundary'});
    });

    it('outdents to just after the former parent', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'a1', 'outdent')).toEqual({move: {pageId: 'a1', parentId: '', siblingIndex: 1}});
    });

    it('refuses to outdent a root page', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'a', 'outdent')).toEqual({blocked: 'boundary'});
    });

    it('refuses an indent that would push the moved subtree past the depth cap', () => {
        // A full-depth chain, plus a sibling of its root to indent under.
        const list = [...chain(MAX_PAGE_LEVELS), makePage('sibling', 'space1', 'sibling', -1)];
        const {roots, heights} = treeOf(list);

        expect(heights.get('p0')).toBe(MAX_PAGE_LEVELS - 1);
        expect(resolveReorder(roots, heights, 'p0', 'indent')).toEqual({blocked: 'depth'});
    });

    it('allows an indent that lands exactly on the depth cap', () => {
        const list = [...chain(MAX_PAGE_LEVELS - 1), makePage('sibling', 'space1', 'sibling', -1)];
        const {roots, heights} = treeOf(list);

        expect(resolveReorder(roots, heights, 'p0', 'indent')).toEqual({move: {pageId: 'p0', parentId: 'sibling', siblingIndex: 0}});
    });

    it('reports a boundary for a page the tree does not hold', () => {
        const {roots, heights} = treeOf(pages);

        expect(resolveReorder(roots, heights, 'nope', 'up')).toEqual({blocked: 'boundary'});
    });
});

describe('resolveReorder with drafts in the group', () => {
    const draftRow = (pageId: string, parentId = '') => ({
        ...makeDraft(pageId, 'space1', pageId),
        parent_id: parentId,
    });

    const treeWithDrafts = (list: Page[], drafts: Array<ReturnType<typeof draftRow>>) => {
        const roots = buildPageTree(list, drafts);
        return {roots, heights: buildSubtreeHeightMap(roots)};
    };

    // Drafts hold the tail of the group. Letting a page move past one would claim a
    // position the server cannot store, and publishing appends anyway.
    it('stops a page moving down past the drafts', () => {
        const {roots, heights} = treeWithDrafts(pages, [draftRow('d1')]);

        // 'b' is the last published root, with a draft rendered after it.
        expect(resolveReorder(roots, heights, 'b', 'down')).toEqual({blocked: 'boundary'});

        // Moving up is unaffected — that stays inside the published run.
        expect(resolveReorder(roots, heights, 'b', 'up')).toEqual({
            move: {pageId: 'b', parentId: '', siblingIndex: 0},
        });
    });

    // No stored order to write, and making room would move a published sibling.
    // Reported as a boundary so the gesture is announced rather than ignored.
    it('refuses to reorder a draft at all', () => {
        const {roots, heights} = treeWithDrafts(pages, [draftRow('d1')]);

        for (const intent of ['up', 'down', 'indent', 'outdent'] as const) {
            expect(resolveReorder(roots, heights, 'd1', intent)).toEqual({blocked: 'boundary'});
        }
    });

    it('leaves a group with no drafts reordering as before', () => {
        const {roots, heights} = treeWithDrafts(pages, []);

        expect(resolveReorder(roots, heights, 'a', 'down')).toEqual({
            move: {pageId: 'a', parentId: '', siblingIndex: 1},
        });
    });
});
