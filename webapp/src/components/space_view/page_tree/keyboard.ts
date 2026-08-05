// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {publishedCount} from 'store/page_tree';
import type {PageNode} from 'store/page_tree';

import {MAX_PAGE_DEPTH} from './depth';

// A row as the keyboard sees it: the tree flattened to what's actually on screen,
// which is the order Up/Down move through.
export type VisibleRow = {
    node: PageNode;
    parentId: string;
};

export type ReorderIntent = 'up' | 'down' | 'indent' | 'outdent';

export type PageMove = {
    pageId: string;
    parentId: string;
    siblingIndex: number;
};

/**
 * Why a reorder couldn't happen. `boundary` means there's nowhere to go in that
 * direction (already first/last, no preceding sibling to nest under, already at
 * the root); `depth` means the nest would breach the level cap. They read very
 * differently to someone listening, so they stay distinct.
 */
export type ReorderOutcome =
    | {move: PageMove; blocked?: undefined}
    | {move?: undefined; blocked: 'boundary' | 'depth'};

const BOUNDARY: ReorderOutcome = {blocked: 'boundary'};

/**
 * The visible rows, top to bottom. A collapsed node contributes itself but not
 * its subtree, so this matches what the user can see and step through.
 */
export function flattenVisibleRows(roots: PageNode[], collapsed: Set<string>): VisibleRow[] {
    const rows: VisibleRow[] = [];

    const walk = (nodes: PageNode[], parentId: string) => {
        for (const node of nodes) {
            rows.push({node, parentId});
            if (!collapsed.has(node.id)) {
                walk(node.children, node.id);
            }
        }
    };

    walk(roots, '');
    return rows;
}

type Position = {
    node: PageNode;
    parentId: string;
    siblings: PageNode[];
    index: number;
};

// Where a page sits in the tree: its parent, its sibling group, and its index in
// that group. Returns null for an id the tree doesn't hold.
function findPosition(roots: PageNode[], pageId: string): Position | null {
    const search = (nodes: PageNode[], parentId: string): Position | null => {
        const index = nodes.findIndex((node) => node.id === pageId);
        if (index !== -1) {
            return {node: nodes[index], parentId, siblings: nodes, index};
        }
        for (const node of nodes) {
            const found = search(node.children, node.id);
            if (found) {
                return found;
            }
        }
        return null;
    };

    return search(roots, '');
}

/**
 * Resolves a keyboard reorder into the move the server expects, or the reason it
 * can't happen.
 *
 * `siblingIndex` is the final 0-based position within the new parent, counted with
 * the moved page removed — the same contract the drag resolver uses.
 */
export function resolveReorder(
    roots: PageNode[],
    subtreeHeights: Map<string, number>,
    pageId: string,
    intent: ReorderIntent,
): ReorderOutcome {
    const position = findPosition(roots, pageId);
    if (!position) {
        return BOUNDARY;
    }

    const {node, parentId, siblings, index} = position;

    // An unpublished page has no stored order to write, and reordering it would have
    // to move a published sibling to make room. It reports as a boundary so the
    // gesture is announced as refused rather than silently doing nothing.
    if (!node.page) {
        return BOUNDARY;
    }

    // Drafts occupy the tail of the group, and a published page may not be moved past
    // them: it would claim a position the server cannot express, and publishing a
    // draft appends anyway. So the last position available to a page is the last
    // published one.
    const lastPublished = publishedCount(siblings) - 1;

    switch (intent) {
    case 'up':
        return index === 0 ? BOUNDARY : {move: {pageId, parentId, siblingIndex: index - 1}};

    case 'down':
        return index === lastPublished ? BOUNDARY : {move: {pageId, parentId, siblingIndex: index + 1}};

    // Nest under the preceding sibling, as its last child. A draft cannot be a
    // parent, but drafts sort after every published sibling, so the row before a
    // page is always a page — the guard is here for the claim, not for a live case.
    case 'indent': {
        if (index === 0) {
            return BOUNDARY;
        }
        const newParent = siblings[index - 1];
        if (!newParent.page) {
            return BOUNDARY;
        }
        const deepest = newParent.depth + 1 + (subtreeHeights.get(pageId) ?? 0);
        if (deepest > MAX_PAGE_DEPTH) {
            return {blocked: 'depth'};
        }
        return {move: {pageId, parentId: newParent.id, siblingIndex: newParent.children.length}};
    }

    // Become the parent's next sibling. Always shallower, so the cap can't bite.
    case 'outdent': {
        if (parentId === '') {
            return BOUNDARY;
        }
        const parent = findPosition(roots, parentId);
        return parent ? {move: {pageId, parentId: parent.parentId, siblingIndex: parent.index + 1}} : BOUNDARY;
    }

    default:
        return BOUNDARY;
    }
}
