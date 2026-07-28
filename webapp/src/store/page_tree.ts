// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from 'types/docs';

// A node in the page tree: the page, its ordered children, and its depth from
// the roots (roots are depth 0).
export type PageNode = {
    page: Page;
    children: PageNode[];
    depth: number;
};

const bySortOrder = (a: Page, b: Page): number =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title);

// Builds the page tree from a flat page list. Roots have parent_id === ''.
// Children are ordered by sort_order (title as a stable tiebreak). Pages whose
// parent isn't present are treated as roots so nothing is silently dropped.
export function buildPageTree(pages: Page[]): PageNode[] {
    const byParent = new Map<string, Page[]>();
    const ids = new Set(pages.map((page) => page.id));

    for (const page of pages) {
        const parentId = ids.has(page.parent_id) ? page.parent_id : '';
        const group = byParent.get(parentId);
        if (group) {
            group.push(page);
        } else {
            byParent.set(parentId, [page]);
        }
    }

    const build = (parentId: string, depth: number): PageNode[] =>
        (byParent.get(parentId) ?? []).
            slice().
            sort(bySortOrder).
            map((page) => ({page, children: build(page.id, depth + 1), depth}));

    return build('', 0);
}

// Maps each page id to the set of its descendant ids (excluding itself). Backs
// the drag guard that forbids dropping a page into its own subtree.
export function buildDescendantMap(roots: PageNode[]): Map<string, Set<string>> {
    const map = new Map<string, Set<string>>();

    const collect = (node: PageNode): Set<string> => {
        const descendants = new Set<string>();
        for (const child of node.children) {
            descendants.add(child.page.id);
            for (const id of collect(child)) {
                descendants.add(id);
            }
        }
        map.set(node.page.id, descendants);
        return descendants;
    };

    roots.forEach(collect);
    return map;
}
