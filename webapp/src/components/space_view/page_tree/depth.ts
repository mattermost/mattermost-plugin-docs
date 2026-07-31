// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {PageNode} from 'store/page_tree';

// Hard cap on nesting: 10 levels total, so the deepest allowed node depth is 9.
export const MAX_PAGE_LEVELS = 10;
export const MAX_PAGE_DEPTH = MAX_PAGE_LEVELS - 1;

// Maps each page id to the height of its subtree: the number of levels below it
// (a leaf is 0). Backs the drag guard that rejects a move whose deepest
// descendant would land past `MAX_PAGE_DEPTH`.
export function buildSubtreeHeightMap(roots: PageNode[]): Map<string, number> {
    const map = new Map<string, number>();

    const measure = (node: PageNode): number => {
        let height = 0;
        for (const child of node.children) {
            height = Math.max(height, measure(child) + 1);
        }
        map.set(node.page.id, height);
        return height;
    };

    roots.forEach(measure);
    return map;
}
