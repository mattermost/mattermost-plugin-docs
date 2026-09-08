// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Permission} from 'types/permissions';

import {getCanCreatePage, getCanDeletePage, getCanDeleteSpace, getCanEditPage, getCanManageSpaceMembers, getCustomDefaultsAvailable, getMustJoinSpace} from './permissions';
import {makeSpace} from './test_fixtures';

import {makeTestState} from '../../tests/react_testing_utils';

describe('getCanManageSpaceMembers', () => {
    // The manage tier is part of the caller's effective permission set, so a space resolved without
    // it is a space the caller may not manage.
    const spaceWithManage = (canManage?: boolean) => ({
        ...makeSpace('space-1', 'Engineering'),
        ...(canManage === undefined ? {} : {permissions: canManage ? ['read_page', 'manage_space'] as Permission[] : ['read_page'] as Permission[]}),
    });

    it('offers member management when the server says the caller may', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWithManage(true)}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(true);
    });

    it('offers member management to a space administrator without a separate manage tier', () => {
        const adminSpace = {
            ...makeSpace('space-1', 'Engineering'),
            permissions: ['read_page', 'admin_space'] as Permission[],
        };
        const state = makeTestState({docs: {spaces: {'space-1': adminSpace}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(true);
    });

    it('withholds it when the server says the caller may not', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWithManage(false)}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(false);
    });

    // Membership alone is not authority: the roster routes refuse an ordinary member and admit a
    // team manage_space holder or a sysadmin who need not appear in the list at all. Membership is
    // not consulted here, so a space full of members answers false without the server's word.
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

    // A space seen only in a team listing carries no permissions. Offering the action on that
    // record showed create to readers for as long as useResolveSpacePermissions took to answer,
    // so an unresolved set withholds it and the affordance appears once the server has replied.
    it('withholds it when permissions have not been resolved', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(undefined)}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(false);
        expect(getCanCreatePage(state, 'never-loaded')).toBe(false);
    });
});

// The non-member of an open space: the server reports their present-tense permissions truthfully
// (read_page alone), and what they would hold as a member is the space's defaults. Keying the
// affordance on the permission set alone hid every authoring control from exactly the people an
// open space exists to admit — and since nothing but a write could have joined them, the join was
// then unreachable.
describe('authoring on a space the caller may join', () => {
    const joinable = (canJoin: boolean, defaults: Permission[]) => ({
        ...makeSpace('space-1', 'Engineering'),
        permissions: ['read_page'] as Permission[],
        default_permissions: defaults,
        can_join: canJoin,
    });

    it('offers authoring the space default would grant on joining', () => {
        const state = makeTestState({docs: {spaces: {'space-1': joinable(true, ['create_page', 'edit_page'])}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(true);
        expect(getCanEditPage(state, 'space-1')).toBe(true);
    });

    // Each permission is answered from the default set that carries it: a space defaulting to
    // create-only must not offer editing on the strength of being joinable.
    it('offers only what the default set actually carries', () => {
        const state = makeTestState({docs: {spaces: {'space-1': joinable(true, ['create_page'])}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(true);
        expect(getCanEditPage(state, 'space-1')).toBe(false);
    });

    // The guest case, and the reason can_join is the server's answer rather than something the
    // client derives: a guest member of this same open space carries the identical read_page
    // against the identical defaults, and is indistinguishable here. Only can_join separates them.
    it('withholds it from a reader the server will not admit', () => {
        const state = makeTestState({docs: {spaces: {'space-1': joinable(false, ['create_page', 'edit_page'])}}});

        expect(getCanCreatePage(state, 'space-1')).toBe(false);
        expect(getCanEditPage(state, 'space-1')).toBe(false);
    });
});

// The one page action whose answer depends on which page it is. delete_page covers everything in
// the space; delete_own_page covers only what the caller wrote, so the same member is offered it on
// their own page and refused on someone else's.
describe('getCanDeletePage', () => {
    const me = 'me';
    const someoneElse = 'someone-else';

    const stateWith = (permissions: Permission[]) => makeTestState({
        docs: {spaces: {'space-1': {...makeSpace('space-1', 'Engineering'), permissions}}},
        currentUser: {id: me} as never,
    });

    it('offers it on any page when the caller holds delete_page', () => {
        const state = stateWith(['read_page', 'delete_page']);

        expect(getCanDeletePage(state, 'space-1', someoneElse)).toBe(true);
        expect(getCanDeletePage(state, 'space-1', me)).toBe(true);
    });

    // The contribute default: delete_own_page and no wider grant, which is the case that makes this
    // per-page rather than per-space.
    it('offers it only on the caller\'s own page when they hold delete_own_page', () => {
        const state = stateWith(['read_page', 'delete_own_page']);

        expect(getCanDeletePage(state, 'space-1', me)).toBe(true);
        expect(getCanDeletePage(state, 'space-1', someoneElse)).toBe(false);
    });

    it('withholds it when the resolved set carries neither', () => {
        const state = stateWith(['read_page']);

        expect(getCanDeletePage(state, 'space-1', me)).toBe(false);
        expect(getCanDeletePage(state, 'space-1', someoneElse)).toBe(false);
    });

    // Unresolved is "not answered yet", as it is everywhere else, and every one of these selectors
    // withholds on it: a control the server would refuse is never shown ahead of its answer.
    it('withholds it when permissions have not been resolved', () => {
        const state = makeTestState({
            docs: {spaces: {'space-1': makeSpace('space-1', 'Engineering')}},
            currentUser: {id: me} as never,
        });

        expect(getCanDeletePage(state, 'space-1', someoneElse)).toBe(false);
        expect(getCanDeletePage(state, 'space-1', me)).toBe(false);
    });
});

describe('getMustJoinSpace', () => {
    // Gates the join that must precede the write the affordances above lead to. A space that never
    // resolved carries no answer, which is not a yes: the write path would send a join for a space
    // nobody said was joinable.
    it('reports the join only where the server offered one', () => {
        const state = makeTestState({docs: {spaces: {
            joinable: {...makeSpace('joinable', 'Open'), can_join: true},
            member: {...makeSpace('member', 'Joined'), can_join: false},
            unresolved: makeSpace('unresolved', 'Listed'),
        }}});

        expect(getMustJoinSpace(state, 'joinable')).toBe(true);
        expect(getMustJoinSpace(state, 'member')).toBe(false);
        expect(getMustJoinSpace(state, 'unresolved')).toBe(false);
        expect(getMustJoinSpace(state, 'never-loaded')).toBe(false);
    });
});

describe('getCanDeleteSpace', () => {
    const spaceWith = (permissions?: Permission[]) => ({
        ...makeSpace('space-1', 'Engineering'),
        ...(permissions === undefined ? {} : {permissions}),
    });

    it('offers archiving when the resolved set grants delete_space', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page', 'delete_space'])}}});

        expect(getCanDeleteSpace(state, 'space-1')).toBe(true);
    });

    // The two team permissions behind these tiers are independent, so neither stands in for the
    // other. Reading the manage tier here offered archive to a caller the delete route refuses.
    it('does not accept the manage tier in place of the delete tier', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page', 'manage_space'])}}});

        expect(getCanDeleteSpace(state, 'space-1')).toBe(false);
    });

    // The converse: a team delete_space holder who is not a space admin and holds no manage tier
    // is admitted by the route, so the affordance has to follow the permission rather than the tier
    // that happens to open the settings surface.
    it('offers it on the delete tier alone, without the manage tier', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page', 'delete_space'])}}});

        expect(getCanManageSpaceMembers(state, 'space-1')).toBe(false);
        expect(getCanDeleteSpace(state, 'space-1')).toBe(true);
    });

    // Unresolved is treated as not-permitted, unlike page creation: archiving is destructive, so a
    // listing that never carried the field must not advertise it.
    it('withholds it when permissions have not been resolved', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(undefined)}}});

        expect(getCanDeleteSpace(state, 'space-1')).toBe(false);
        expect(getCanDeleteSpace(state, 'never-loaded')).toBe(false);
    });
});

describe('getCanEditPage', () => {
    const spaceWith = (permissions?: Permission[]) => ({
        ...makeSpace('space-1', 'Engineering'),
        ...(permissions === undefined ? {} : {permissions}),
    });

    it('offers editing when the resolved set grants edit_page', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page', 'edit_page'])}}});

        expect(getCanEditPage(state, 'space-1')).toBe(true);
    });

    // The server splits the two: publishing a new page takes create_page, publishing over a live
    // one takes edit_page. An author who may only create must not be offered the edit entry.
    it('withholds it when the set grants create_page but not edit_page', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(['read_page', 'create_page'])}}});

        expect(getCanEditPage(state, 'space-1')).toBe(false);
    });

    it('withholds it when permissions have not been resolved', () => {
        const state = makeTestState({docs: {spaces: {'space-1': spaceWith(undefined)}}});

        expect(getCanEditPage(state, 'space-1')).toBe(false);
        expect(getCanEditPage(state, 'never-loaded')).toBe(false);
    });
});

describe('getCustomDefaultsAvailable', () => {
    // Mirrors the server: a custom scheme needs the custom-schemes entitlement (or the
    // Professional SKU, which carries it without the flag) and, because every Docs scheme also
    // defines a guest role, the guest account permissions entitlement.
    it.each([
        ['both entitlements', {CustomPermissionsSchemes: 'true', GuestAccountsPermissions: 'true'}, true],
        ['the Professional SKU with guest permissions', {SkuShortName: 'professional', GuestAccountsPermissions: 'true'}, true],
        ['custom schemes without guest permissions', {CustomPermissionsSchemes: 'true'}, false],
        ['guest permissions without custom schemes', {GuestAccountsPermissions: 'true'}, false],
        ['no license', {}, false],
    ])('is %s → %s', (_case, license, expected) => {
        const state = makeTestState({license});

        expect(getCustomDefaultsAvailable(state)).toBe(expected);
    });
});
