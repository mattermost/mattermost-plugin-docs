// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {buildDescendantMap, buildPageTree, publishedCount} from './page_tree';
import {makeDraft, makePage} from './test_fixtures';

const child = (id: string, parentId: string, title: string, sortOrder = 0) => ({
    ...makePage(id, 'space-a', title, sortOrder),
    parent_id: parentId,
});

describe('buildPageTree', () => {
    it('nests children under parents and orders each group by sort_order', () => {
        const root1 = makePage('r1', 'space-a', 'Root 1', 1);
        const root2 = makePage('r2', 'space-a', 'Root 2', 0);
        const c1 = child('c1', 'r1', 'Child 1', 1);
        const c2 = child('c2', 'r1', 'Child 2', 0);
        const g1 = child('g1', 'c2', 'Grandchild', 0);

        const tree = buildPageTree([root1, root2, c1, c2, g1]);

        expect(tree.map((node) => node.id)).toEqual(['r2', 'r1']);

        const r1Node = tree.find((node) => node.id === 'r1')!;
        expect(r1Node.depth).toBe(0);
        expect(r1Node.children.map((node) => node.id)).toEqual(['c2', 'c1']);

        const c2Node = r1Node.children.find((node) => node.id === 'c2')!;
        expect(c2Node.depth).toBe(1);
        expect(c2Node.children.map((node) => node.id)).toEqual(['g1']);
        expect(c2Node.children[0].depth).toBe(2);
    });

    it('treats pages with a missing parent as roots', () => {
        const orphan = child('o1', 'gone', 'Orphan', 0);
        const tree = buildPageTree([orphan]);
        expect(tree.map((node) => node.id)).toEqual(['o1']);
    });
});

describe('buildPageTree with drafts', () => {
    const draftUnder = (pageId: string, parentId: string, title: string, createAt = 0) => ({
        ...makeDraft(pageId, 'space-a', title, 0, 0),
        parent_id: parentId,
        create_at: createAt,
    });

    // Drafts have no SortOrder column, so they cannot be interleaved — and publishing
    // appends, so last is also where the page will land.
    it('puts drafts after every published sibling in their group', () => {
        const root = makePage('r1', 'space-a', 'Root', 1);
        const other = makePage('r2', 'space-a', 'Other', 2);
        const draft = draftUnder('d1', '', 'Unpublished');

        const tree = buildPageTree([root, other], [draft]);

        expect(tree.map((node) => node.id)).toEqual(['r1', 'r2', 'd1']);
        expect(tree[2].draft).toEqual(draft);
        expect(tree[2].page).toBeUndefined();
        expect(tree[2].title).toBe('Unpublished');
    });

    it('nests a draft under the published page it is parented to', () => {
        const root = makePage('r1', 'space-a', 'Root', 1);
        const published = child('c1', 'r1', 'Child', 1);
        const draft = draftUnder('d1', 'r1', 'Unpublished');

        const tree = buildPageTree([root, published], [draft]);

        expect(tree[0].children.map((node) => node.id)).toEqual(['c1', 'd1']);
        expect(tree[0].children[1].depth).toBe(1);
    });

    // create_at, not update_at: update_at advances while the author types, which
    // would move the row mid-edit.
    it('orders drafts among themselves by creation time', () => {
        const later = draftUnder('d2', '', 'Second', 200);
        const earlier = draftUnder('d1', '', 'First', 100);

        expect(buildPageTree([], [later, earlier]).map((node) => node.id)).toEqual(['d1', 'd2']);
    });

    // A draft cannot be a parent: its children could not be published until it was,
    // and a published page cannot be parented to one at all. Falling back to the root
    // keeps it visible instead of nesting or dropping it.
    it('treats a draft parented to another draft as a root', () => {
        const parent = draftUnder('d1', '', 'Parent', 100);
        const nested = draftUnder('d2', 'd1', 'Nested', 200);

        const tree = buildPageTree([], [parent, nested]);

        expect(tree.map((node) => node.id)).toEqual(['d1', 'd2']);
        expect(tree[0].children).toEqual([]);
    });

    // Guards the duplicate-row trap: unpublished edits to a page already in the tree
    // are a marker on its row, never a second row.
    it('skips a draft whose page is published', () => {
        const published = makePage('p1', 'space-a', 'Published', 1);
        const edits = draftUnder('p1', '', 'Edited');

        expect(buildPageTree([published], [edits]).map((node) => node.id)).toEqual(['p1']);
    });
});

describe('publishedCount', () => {
    it('counts only the published rows, which is where the boundary sits', () => {
        const tree = buildPageTree(
            [makePage('p1', 'space-a', 'One', 1), makePage('p2', 'space-a', 'Two', 2)],
            [{...makeDraft('d1', 'space-a', 'Draft'), parent_id: ''}],
        );

        expect(publishedCount(tree)).toBe(2);
        expect(publishedCount([])).toBe(0);
    });
});

describe('buildDescendantMap', () => {
    it('collects the full subtree id set for each node', () => {
        const root = makePage('r1', 'space-a', 'Root', 0);
        const c1 = child('c1', 'r1', 'Child', 0);
        const g1 = child('g1', 'c1', 'Grandchild', 0);

        const map = buildDescendantMap(buildPageTree([root, c1, g1]));

        expect(map.get('r1')).toEqual(new Set(['c1', 'g1']));
        expect(map.get('c1')).toEqual(new Set(['g1']));
        expect(map.get('g1')).toEqual(new Set());
    });
});
