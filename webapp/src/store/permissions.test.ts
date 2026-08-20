// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Permission} from 'types/permissions';

import {getCanCreatePage, getCanManageSpaceMembers} from './permissions';
import {makeSpace} from './test_fixtures';

import {makeTestState} from '../../tests/react_testing_utils';

describe('getCanManageSpaceMembers', () => {
    // The manage tier rides in the caller's effective permission set, so a space resolved without
    // it is a space the caller may not manage.
    const spaceWithManage = (canManage?: boolean) => ({
        ...makeSpace('space-1', 'Engineering'),
        ...(canManage === undefined ? {} : {permissions: canManage ? ['read_page', 'manage_space'] as Permission[] : ['read_page'] as Permission[]}),
    });

    it('offers member management when the server says the caller may', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWithManage(true)}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(true);
    });

    it('withholds it when the server says the caller may not', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWithManage(false)}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(false);
    });

    // The rule this replaced was membership: "is the caller in the member list". That was wrong in
    // both directions — the roster routes refuse an ordinary member, and admit a team manage_space
    // holder or a sysadmin who need not appear in the list at all. Membership is deliberately not
    // consulted here any more, so a space full of members answers false without the server's word.
    it('does not infer authority from membership', () => {
        const state = makeTestState({
            currentUser: {id: 'me'},
            docs: {
                spaces: {'space-1': spaceWithManage()},
                spaceMembers: {'space-1': ['me', 'other']},
            },
        });

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(false);
    });

    // Unlike page creation, an unresolved space withholds this: it gates an administrative surface,
    // so offering it before the server has answered would advertise authority the caller may lack.
    it('withholds it for a space the server has not answered for', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWithManage()}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(false);
        expect(getCanManageSpaceMembers(state, 'unloaded')).toBe(false);
    });
});

describe('getCanCreatePage', () => {
    const spaceWith = (permissions?: Permission[]) => ({
        ...makeSpace('space-1', 'Engineering'),
        ...(permissions === undefined ? {} : {permissions}),
    });

    it('offers page creation when the space default grants it', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page', 'create_page'])}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(true);
    });

    // The guest case, and equally a member of a space whose default has had create_page
    // revoked: the server refuses the write, so the affordance must not be offered.
    it('withholds it when the resolved set omits create_page', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page'])}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(false);
    });

    // A space seen only in a team listing carries no permissions. That is "not resolved yet",
    // not "holds nothing" — withholding here would hide the action from a legitimate author.
    it('offers it when permissions have not been resolved', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(undefined)}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(true);
        expect(getCanCreatePage(state, 'never-loaded')).toBe(true);
    });
});
