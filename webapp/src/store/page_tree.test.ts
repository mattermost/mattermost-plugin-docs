// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {buildDescendantMap, buildPageTree} from './page_tree';
import {makePage} from './test_fixtures';

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

        expect(tree.map((node) => node.page.id)).toEqual(['r2', 'r1']);

        const r1Node = tree.find((node) => node.page.id === 'r1')!;
        expect(r1Node.depth).toBe(0);
        expect(r1Node.children.map((node) => node.page.id)).toEqual(['c2', 'c1']);

        const c2Node = r1Node.children.find((node) => node.page.id === 'c2')!;
        expect(c2Node.depth).toBe(1);
        expect(c2Node.children.map((node) => node.page.id)).toEqual(['g1']);
        expect(c2Node.children[0].depth).toBe(2);
    });

    it('treats pages with a missing parent as roots', () => {
        const orphan = child('o1', 'gone', 'Orphan', 0);
        const tree = buildPageTree([orphan]);
        expect(tree.map((node) => node.page.id)).toEqual(['o1']);
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
