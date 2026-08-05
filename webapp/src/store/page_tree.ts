// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from 'types/docs';
import type {Draft} from 'types/drafts';

/**
 * A row in the page tree: a published page, or one of the caller's own unpublished
 * ones, plus its ordered children and its depth from the roots (roots are depth 0).
 *
 * `id` and `title` are what a row is read by, whichever backs it — a draft reserves
 * its page id at creation, so both share one id namespace and callers can key,
 * compare and focus on `id` without caring which kind they hold.
 *
 * Exactly one of `page`/`draft` is set. A draft row differs in ways the tree has to
 * respect: only its author can see it, it has no stored sort order, and it is never
 * a parent (see buildPageTree).
 */
export type PageNode = {
    id: string;
    title: string;
    page?: Page;
    draft?: Draft;
    children: PageNode[];
    depth: number;
};

const bySortOrder = (a: Page, b: Page): number =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title);

// Drafts have no stored order, so creation order stands in. Deliberately not
// update_at: that advances as the author types — and the server bumps it for
// maintenance writes too — which would move a row while it was being edited.
const byCreateAt = (a: Draft, b: Draft): number =>
    a.create_at - b.create_at || a.page_id.localeCompare(b.page_id);

const group = <T>(map: Map<string, T[]>, key: string, value: T) => {
    const existing = map.get(key);
    if (existing) {
        existing.push(value);
    } else {
        map.set(key, [value]);
    }
};

/**
 * Builds the page tree from a flat page list, with the caller's unpublished pages
 * merged in. Roots have parent_id === ''. Pages whose parent isn't present are
 * treated as roots so nothing is silently dropped.
 *
 * `drafts` should be the space's *orphan* drafts (getOrphanDraftsForSpace):
 * unpublished edits to a page already in the tree are a marker on that page's row,
 * not a row of their own. A draft naming a published page is skipped here as well,
 * so passing the wrong set cannot render a page twice.
 *
 * Two placement rules, both consequences of the server's model rather than choices:
 *
 *  - **Drafts come last in their group.** There is no SortOrder column for drafts,
 *    so one cannot be interleaved with its published siblings. This also matches
 *    what publishing does — append to the group — so the position shown is the
 *    position the page will land in.
 *  - **A draft is never a parent.** Its children could not be published until it was
 *    published first, and a published page cannot be parented to one at all (server
 *    validateParentExists). A draft whose own parent is another draft therefore
 *    falls back to the root group rather than nesting or vanishing.
 */
export function buildPageTree(pages: Page[], drafts: Draft[] = []): PageNode[] {
    const pageIds = new Set(pages.map((page) => page.id));

    const pagesByParent = new Map<string, Page[]>();
    for (const page of pages) {
        group(pagesByParent, pageIds.has(page.parent_id) ? page.parent_id : '', page);
    }

    const draftsByParent = new Map<string, Draft[]>();
    for (const draft of drafts) {
        if (pageIds.has(draft.page_id)) {
            continue;
        }
        group(draftsByParent, pageIds.has(draft.parent_id) ? draft.parent_id : '', draft);
    }

    const build = (parentId: string, depth: number): PageNode[] => {
        const published = (pagesByParent.get(parentId) ?? []).
            slice().
            sort(bySortOrder).
            map((page): PageNode => ({
                id: page.id,
                title: page.title,
                page,
                children: build(page.id, depth + 1),
                depth,
            }));

        const unpublished = (draftsByParent.get(parentId) ?? []).
            slice().
            sort(byCreateAt).
            map((draft): PageNode => ({
                id: draft.page_id,
                title: draft.title,
                draft,
                children: [],
                depth,
            }));

        return [...published, ...unpublished];
    };

    return build('', 0);
}

// How many of a sibling group are published — also the boundary the drafts sit
// after. A page may not be reordered past it and a draft may not be reordered before
// it, so every move has to land inside the published region.
export const publishedCount = (siblings: PageNode[]): number =>
    siblings.filter((node) => node.page).length;

// Maps each row id to the set of its descendant ids (excluding itself). Backs the
// drag guard that forbids dropping a page into its own subtree.
export function buildDescendantMap(roots: PageNode[]): Map<string, Set<string>> {
    const map = new Map<string, Set<string>>();

    const collect = (node: PageNode): Set<string> => {
        const descendants = new Set<string>();
        for (const child of node.children) {
            descendants.add(child.id);
            for (const id of collect(child)) {
                descendants.add(id);
            }
        }
        map.set(node.id, descendants);
        return descendants;
    };

    roots.forEach(collect);
    return map;
}
