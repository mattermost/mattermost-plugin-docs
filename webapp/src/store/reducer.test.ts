// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PageTypes, SpaceTypes} from './action_types';
import reducer from './reducer';
import {makePage, makeSpace} from './test_fixtures';

describe('spaces', () => {
    const initialState = reducer(undefined, {type: '@@INIT'});

    it('RECEIVED_SPACES merges into byId and appends new ids to order', () => {
        const spaceA = makeSpace('a', 'Space A');
        const spaceB = makeSpace('b', 'Space B');

        const afterFirst = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA]});
        expect(afterFirst.spaces.byId).toEqual({a: spaceA});
        expect(afterFirst.spaces.order).toEqual(['a']);

        const afterSecond = reducer(afterFirst, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceB]});
        expect(afterSecond.spaces.byId).toEqual({a: spaceA, b: spaceB});
        expect(afterSecond.spaces.order).toEqual(['a', 'b']);
    });

    it('CREATED_SPACE adds to byId and prepends to order', () => {
        const spaceA = makeSpace('a', 'Space A');
        const spaceB = makeSpace('b', 'Space B');

        const afterReceive = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA]});
        const afterCreate = reducer(afterReceive, {type: SpaceTypes.CREATED_SPACE, space: spaceB});

        expect(afterCreate.spaces.byId).toEqual({a: spaceA, b: spaceB});
        expect(afterCreate.spaces.order).toEqual(['b', 'a']);
    });

    it('DELETED_SPACE prunes byId and order', () => {
        const spaceA = makeSpace('a', 'Space A');
        const spaceB = makeSpace('b', 'Space B');

        const afterReceive = reducer(initialState, {type: SpaceTypes.RECEIVED_SPACES, spaces: [spaceA, spaceB]});
        const afterDelete = reducer(afterReceive, {type: SpaceTypes.DELETED_SPACE, spaceId: 'a'});

        expect(afterDelete.spaces.byId).toEqual({b: spaceB});
        expect(afterDelete.spaces.order).toEqual(['b']);
    });
});

describe('pages', () => {
    const initialState = reducer(undefined, {type: '@@INIT'});

    it('RECEIVED_PAGES merges byId and indexes bySpace', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-a', 'Page 2');
        const page3 = makePage('p3', 'space-b', 'Page 3');

        const afterFirst = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [page1, page2]});
        expect(afterFirst.pages.byId).toEqual({p1: page1, p2: page2});
        expect(afterFirst.pages.bySpace).toEqual({'space-a': ['p1', 'p2']});

        const afterSecond = reducer(afterFirst, {type: PageTypes.RECEIVED_PAGES, pages: [page3]});
        expect(afterSecond.pages.byId).toEqual({p1: page1, p2: page2, p3: page3});
        expect(afterSecond.pages.bySpace).toEqual({'space-a': ['p1', 'p2'], 'space-b': ['p3']});
    });

    it('DELETED_SPACE removes that space\'s pages from byId and bySpace', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-b', 'Page 2');

        const afterReceive = reducer(initialState, {type: PageTypes.RECEIVED_PAGES, pages: [page1, page2]});
        const afterDelete = reducer(afterReceive, {type: SpaceTypes.DELETED_SPACE, spaceId: 'space-a'});

        expect(afterDelete.pages.byId).toEqual({p2: page2});
        expect(afterDelete.pages.bySpace).toEqual({'space-b': ['p2']});
    });
});
