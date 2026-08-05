// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DraftTypes, PageTypes, SpaceTypes} from './action_types';
import reducer, {collectSubtreeIds, reindexAfterMove} from './entities';
import {makeDraft, makePage, makeSpace} from './test_fixtures';

const withParent = (id: string, parentId: string, title: string, sortOrder: number) => ({
    ...makePage(id, 'space-a', title, sortOrder),
    parent_id: parentId,
});

describe('spaces', () => {
    const initialState = reducer(undefined, {type: '@@INIT'});

    it('RECEIVED_SPACES merges byId and indexes ids by team', () => {
        const spaceA = makeSpace('a', 'Space A', 't1');
        const spaceB = makeSpace('b', 'Space B', 't2');

        const afterFirst = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA]});
        expect(afterFirst.spaces).toEqual({a: spaceA});
        expect(afterFirst.spacesInTeam.t1).toEqual(new Set(['a']));

        const afterSecond = reducer(afterFirst, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceB]});
        expect(afterSecond.spaces).toEqual({a: spaceA, b: spaceB});
        expect(afterSecond.spacesInTeam.t1).toEqual(new Set(['a']));
        expect(afterSecond.spacesInTeam.t2).toEqual(new Set(['b']));
    });

    it('RECEIVED_SPACES with a teamId marks the team loaded even when it has none', () => {
        const afterEmpty = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [], teamId: 't1'});

        // The entry's presence is what distinguishes "no spaces" from "not loaded".
        expect('t1' in afterEmpty.spacesInTeam).toBe(true);
        expect(afterEmpty.spacesInTeam.t1).toEqual(new Set());

        // An empty list without a teamId carries no such claim.
        expect('t2' in reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: []}).spacesInTeam).toBe(false);
    });

    it('CREATED_SPACE adds to byId and its team set', () => {
        const spaceA = makeSpace('a', 'Space A', 't1');
        const spaceB = makeSpace('b', 'Space B', 't1');

        const afterReceive = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA]});
        const afterCreate = reducer(afterReceive, {type: SpaceTypes.CREATED_SPACE, space: spaceB});

        expect(afterCreate.spaces).toEqual({a: spaceA, b: spaceB});
        expect(afterCreate.spacesInTeam.t1).toEqual(new Set(['a', 'b']));
    });

    it('DELETED_SPACE prunes byId and the team set', () => {
        const spaceA = makeSpace('a', 'Space A', 't1');
        const spaceB = makeSpace('b', 'Space B', 't1');

        const afterReceive = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA, spaceB]});
        const afterDelete = reducer(afterReceive, {type: SpaceTypes.DELETED_SPACE, spaceId: 'a'});

        expect(afterDelete.spaces).toEqual({b: spaceB});
        expect(afterDelete.spacesInTeam.t1).toEqual(new Set(['b']));
    });
});

describe('pages', () => {
    const initialState = reducer(undefined, {type: '@@INIT'});

    it('RECEIVED_PAGES merges byId and indexes ids by space', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-a', 'Page 2');
        const page3 = makePage('p3', 'space-b', 'Page 3');

        const afterFirst = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [page1, page2]});
        expect(afterFirst.pages).toEqual({p1: page1, p2: page2});
        expect(afterFirst.pagesInSpace['space-a']).toEqual(new Set(['p1', 'p2']));

        const afterSecond = reducer(afterFirst, {type: PageTypes.RECEIVED_PAGES, pages: [page3]});
        expect(afterSecond.pages).toEqual({p1: page1, p2: page2, p3: page3});
        expect(afterSecond.pagesInSpace['space-a']).toEqual(new Set(['p1', 'p2']));
        expect(afterSecond.pagesInSpace['space-b']).toEqual(new Set(['p3']));
    });

    it('DELETED_SPACE removes that space\'s pages from byId and its space index', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-b', 'Page 2');

        const afterReceive = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [page1, page2]});
        const afterDelete = reducer(afterReceive, {type: SpaceTypes.DELETED_SPACE, spaceId: 'space-a'});

        expect(afterDelete.pages).toEqual({p2: page2});
        expect(afterDelete.pagesInSpace['space-a']).toBeUndefined();
        expect(afterDelete.pagesInSpace['space-b']).toEqual(new Set(['p2']));
    });

    it('MOVED_PAGE reindexes the moved page within the store', () => {
        const a = withParent('a', '', 'A', 0);
        const b = withParent('b', '', 'B', 1);
        const c = withParent('c', '', 'C', 2);

        const afterReceive = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [a, b, c]});
        const afterMove = reducer(afterReceive, {
            type: PageTypes.MOVED_PAGE,
            pageId: 'c',
            spaceId: 'space-a',
            parentId: '',
            siblingIndex: 0,
        });

        expect(afterMove.pages.c.sort_order).toBe(1);
        expect(afterMove.pages.a.sort_order).toBe(2);
        expect(afterMove.pages.b.sort_order).toBe(3);
    });

    // The move response upserts only the moved page, carrying the server's
    // sort_order. Numbered on a different base from its siblings it tied with the
    // one below it, and the title tiebreak put it back a slot — a drop between two
    // roots landed one position too low.
    it('MOVED_PAGE survives the server echoing the moved page back', () => {
        const a = withParent('a', '', 'A', 1);
        const b = withParent('b', '', 'B', 2);
        const c = withParent('c', '', 'C', 3);

        const afterReceive = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [a, b, c]});

        // Drop C between A and B.
        const afterMove = reducer(afterReceive, {
            type: PageTypes.MOVED_PAGE,
            pageId: 'c',
            spaceId: 'space-a',
            parentId: '',
            siblingIndex: 1,
        });

        // What the server's reindex assigns C for that position, 1-based.
        const echoed = {...afterMove.pages.c, sort_order: 2};
        const afterEcho = reducer(afterMove, {type: PageTypes.RECEIVED_PAGES, pages: [echoed]});

        const rootOrder = ['a', 'b', 'c'].
            map((id) => afterEcho.pages[id]).
            sort((x, y) => x.sort_order - y.sort_order || x.title.localeCompare(y.title)).
            map((page) => page.id);

        expect(rootOrder).toEqual(['a', 'c', 'b']);
    });

    it('DELETED_PAGE prunes the page and its subtree from byId and the space index', () => {
        const parent = withParent('parent', '', 'Parent', 0);
        const child = withParent('child', 'parent', 'Child', 0);
        const grandchild = withParent('grandchild', 'child', 'Grandchild', 0);
        const sibling = withParent('sibling', '', 'Sibling', 1);

        const afterReceive = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [parent, child, grandchild, sibling]});
        const afterDelete = reducer(afterReceive, {
            type: PageTypes.DELETED_PAGE,
            pageId: 'parent',
            spaceId: 'space-a',
            pageIds: [...collectSubtreeIds(afterReceive.pages, 'parent')],
        });

        expect(afterDelete.pages).toEqual({sibling});
        expect(afterDelete.pagesInSpace['space-a']).toEqual(new Set(['sibling']));
    });
});

describe('drafts', () => {
    const initialState = reducer(undefined, {type: '@@INIT'});

    it('RECEIVED_DRAFTS keys drafts by page id and indexes them by space', () => {
        const one = makeDraft('p1', 'space-a', 'One');
        const two = makeDraft('p2', 'space-a', 'Two');

        const next = reducer(initialState, {type: DraftTypes.RECEIVED_DRAFTS, drafts: [one, two], spaceId: 'space-a'});

        expect(next.drafts).toEqual({p1: one, p2: two});
        expect(next.draftsInSpace['space-a']).toEqual(new Set(['p1', 'p2']));
    });

    it('RECEIVED_DRAFTS marks a space loaded even when it has none', () => {
        const next = reducer(initialState, {type: DraftTypes.RECEIVED_DRAFTS, drafts: [], spaceId: 'space-a'});

        expect(next.draftsInSpace['space-a']).toEqual(new Set());
    });

    // The space list is a metadata projection with no body, so a refresh must not
    // blank a body already fetched for that page.
    it('RECEIVED_DRAFTS keeps a body the summary does not carry', () => {
        const full = {...makeDraft('p1', 'space-a', 'One'), body: 'written'};
        const loaded = reducer(initialState, {type: DraftTypes.RECEIVED_DRAFT, draft: full});

        const summary = {...makeDraft('p1', 'space-a', 'One', 5)};
        delete (summary as Partial<typeof summary>).body;
        const next = reducer(loaded, {type: DraftTypes.RECEIVED_DRAFTS, drafts: [summary], spaceId: 'space-a'});

        expect(next.drafts.p1.body).toBe('written');
        expect(next.drafts.p1.update_at).toBe(5);
    });

    it('DELETED_DRAFT removes it from byId and the space index', () => {
        const draft = makeDraft('p1', 'space-a', 'One');
        const loaded = reducer(initialState, {type: DraftTypes.RECEIVED_DRAFTS, drafts: [draft], spaceId: 'space-a'});

        const next = reducer(loaded, {type: DraftTypes.DELETED_DRAFT, spaceId: 'space-a', pageId: 'p1'});

        expect(next.drafts).toEqual({});
        expect(next.draftsInSpace['space-a']).toEqual(new Set());
    });

    // One action, so no render sees the draft and its page at once.
    it('PUBLISHED_DRAFT removes the draft and adds the page together', () => {
        const draft = makeDraft('p1', 'space-a', 'One');
        const loaded = reducer(initialState, {type: DraftTypes.RECEIVED_DRAFTS, drafts: [draft], spaceId: 'space-a'});
        const page = makePage('p1', 'space-a', 'One');

        const next = reducer(loaded, {type: DraftTypes.PUBLISHED_DRAFT, spaceId: 'space-a', pageId: 'p1', page});

        expect(next.drafts).toEqual({});
        expect(next.draftsInSpace['space-a']).toEqual(new Set());
        expect(next.pages).toEqual({p1: page});
        expect(next.pagesInSpace['space-a']).toEqual(new Set(['p1']));
    });

    // Unpublished edits to a page that no longer exists can never be published.
    it('DELETED_PAGE discards drafts for the deleted subtree', () => {
        const parent = makePage('parent', 'space-a', 'Parent');
        const child = {...makePage('child', 'space-a', 'Child'), parent_id: 'parent'};
        const withPages = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [parent, child], spaceId: 'space-a'});
        const loaded = reducer(withPages, {
            type: DraftTypes.RECEIVED_DRAFTS,
            drafts: [makeDraft('child', 'space-a', 'Child edits', 0, 7)],
            spaceId: 'space-a',
        });

        const next = reducer(loaded, {
            type: PageTypes.DELETED_PAGE,
            pageId: 'parent',
            spaceId: 'space-a',
            pageIds: [...collectSubtreeIds(loaded.pages, 'parent')],
        });

        expect(next.drafts).toEqual({});
        expect(next.draftsInSpace['space-a']).toEqual(new Set());
    });

    it('DELETED_SPACE discards that space\'s drafts', () => {
        const mine = makeDraft('p1', 'space-a', 'One');
        const other = makeDraft('p2', 'space-b', 'Two');
        const loaded = reducer(initialState, {type: DraftTypes.RECEIVED_DRAFTS, drafts: [mine, other]});

        const next = reducer(loaded, {type: SpaceTypes.DELETED_SPACE, spaceId: 'space-a'});

        expect(next.drafts).toEqual({p2: other});
        expect(next.draftsInSpace['space-a']).toBeUndefined();
    });
});

describe('collectSubtreeIds', () => {
    it('collects the root and every descendant, excluding unrelated pages', () => {
        const byId = {
            root: withParent('root', '', 'Root', 0),
            a: withParent('a', 'root', 'A', 0),
            b: withParent('b', 'a', 'B', 0),
            other: withParent('other', '', 'Other', 1),
        };

        expect(collectSubtreeIds(byId, 'root')).toEqual(new Set(['root', 'a', 'b']));
        expect(collectSubtreeIds(byId, 'a')).toEqual(new Set(['a', 'b']));
    });
});

describe('reindexAfterMove', () => {
    it('reorders siblings within the same parent, numbered 1-based like the server', () => {
        const byId = {
            a: withParent('a', '', 'A', 1),
            b: withParent('b', '', 'B', 2),
            c: withParent('c', '', 'C', 3),
        };

        // Move A to the end.
        const next = reindexAfterMove(byId, 'a', 'space-a', '', 2);

        expect(next.b.sort_order).toBe(1);
        expect(next.c.sort_order).toBe(2);
        expect(next.a.sort_order).toBe(3);
        expect(next.a.parent_id).toBe('');
    });

    it('reparents a page and reindexes the destination group only', () => {
        const byId = {
            p: withParent('p', '', 'Parent', 1),
            a: withParent('a', '', 'A', 2),
            b: withParent('b', '', 'B', 3),
            x: withParent('x', 'p', 'X', 1),
        };

        // Move B under P as its first child.
        const next = reindexAfterMove(byId, 'b', 'space-a', 'p', 0);

        expect(next.b.parent_id).toBe('p');
        expect(next.b.sort_order).toBe(1);
        expect(next.x.sort_order).toBe(2);

        // The old group keeps its numbering, gap included, as the server does —
        // closing it up here would disagree with any page later refetched from it.
        expect(next.p.sort_order).toBe(1);
        expect(next.a.sort_order).toBe(2);
    });
});
