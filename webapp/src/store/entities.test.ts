// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PageTypes, SpaceTypes} from './action_types';
import reducer, {collectSubtreeIds, reindexAfterMove} from './entities';
import {makePage, makeSpace} from './test_fixtures';

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

        expect(afterMove.pages.c.sort_order).toBe(0);
        expect(afterMove.pages.a.sort_order).toBe(1);
        expect(afterMove.pages.b.sort_order).toBe(2);
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
    it('reorders siblings within the same parent (0-based)', () => {
        const byId = {
            a: withParent('a', '', 'A', 0),
            b: withParent('b', '', 'B', 1),
            c: withParent('c', '', 'C', 2),
        };

        // Move A to the end.
        const next = reindexAfterMove(byId, 'a', 'space-a', '', 2);

        expect(next.b.sort_order).toBe(0);
        expect(next.c.sort_order).toBe(1);
        expect(next.a.sort_order).toBe(2);
        expect(next.a.parent_id).toBe('');
    });

    it('reparents a page and reindexes both the old and new sibling groups', () => {
        const byId = {
            p: withParent('p', '', 'Parent', 0),
            a: withParent('a', '', 'A', 1),
            b: withParent('b', '', 'B', 2),
            x: withParent('x', 'p', 'X', 0),
        };

        // Move B under P as its first child.
        const next = reindexAfterMove(byId, 'b', 'space-a', 'p', 0);

        expect(next.b.parent_id).toBe('p');
        expect(next.b.sort_order).toBe(0);
        expect(next.x.sort_order).toBe(1);

        // Old root group renumbers to fill the gap.
        expect(next.p.sort_order).toBe(0);
        expect(next.a.sort_order).toBe(1);
    });
});
