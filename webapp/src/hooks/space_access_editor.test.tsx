// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {makeSpace} from 'store/test_fixtures';

import type {Space} from 'types/docs';
import type {SpaceMember} from 'types/permissions';
import {DEFAULT_PERMISSION_ORDER, MEMBER_PERMISSION_ORDER} from 'types/permissions';

import {useSpaceAccessEditor} from './space_access_editor';
import type {SpacePermissions} from './space_permissions';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();
const mockLeave = jest.fn();
let mockManageMembersBusy = false;

jest.mock('hooks/space_members', () => ({
    useManageSpaceMembers: () => ({
        addMembers: mockAddMembers,
        removeMember: mockRemoveMember,
        leave: mockLeave,
        busy: mockManageMembersBusy,
    }),
}));

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => [
        {id: 'me', displayName: 'Caleb', username: 'caleb', avatarUrl: ''},
        {id: 'u2', displayName: 'Ada', username: 'ada', avatarUrl: ''},
    ],
}));

let mockPermissionsState: SpacePermissions;

jest.mock('hooks/space_permissions', () => ({
    useSpacePermissions: () => mockPermissionsState,
}));

const space: Space = makeSpace('space-1', 'Engineering');

const member = (userId: string, overrides: Partial<SpaceMember> = {}): SpaceMember => ({
    user_id: userId,
    permissions: ['read_page'],
    granted_permissions: [],
    is_admin: false,
    is_guest: false,
    is_auto_joined: false,
    ...overrides,
});

const profile = (id: string): MemberProfile => ({id, displayName: id, username: id, avatarUrl: ''});

// currentUserId is 'me', matching the id used by the self-lock cases below.
const render = (onClose = jest.fn()) => {
    const store = makeTestStore({currentUser: {id: 'me'}});
    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                {children}
            </IntlProvider>
        </Provider>
    );

    return {hook: renderHook(() => useSpaceAccessEditor(space, {onClose}), {wrapper}).result, onClose};
};

describe('useSpaceAccessEditor', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockManageMembersBusy = false;
        mockLeave.mockResolvedValue(true);
        mockPermissionsState = {
            defaults: [],
            viewAccess: 'open',
            members: new Map(),
            canAdminister: true,
            canManageMembers: true,
            loading: false,
            loadFailed: false,
            busy: false,
            setDefaults: jest.fn(),
            setMemberGrants: jest.fn(),
            setViewAccess: jest.fn(),
        };
    });

    // Authority and availability are separate: a caller who lacks the authority gets no control
    // rendered at all, where one who owns it loses it only for as long as a write is in flight.
    describe('canEditAccess', () => {
        it('follows the administer authority alone, not the in-flight state', () => {
            const {hook} = render();
            expect(hook.current.canEditAccess).toBe(true);

            mockPermissionsState = {...mockPermissionsState, busy: true};
            expect(render().hook.current.canEditAccess).toBe(true);

            mockPermissionsState = {...mockPermissionsState, busy: false, canAdminister: false};
            expect(render().hook.current.canEditAccess).toBe(false);
        });
    });

    describe('accessBusy / rosterBusy', () => {
        it('are clear for an administrator once the read resolves', () => {
            const {hook} = render();

            expect(hook.current.accessBusy).toBe(false);
            expect(hook.current.rosterBusy).toBe(false);
        });

        it('are both set while the permission read is loading', () => {
            mockPermissionsState = {...mockPermissionsState, loading: true};
            const {hook} = render();

            expect(hook.current.accessBusy).toBe(true);
            expect(hook.current.rosterBusy).toBe(true);
        });

        it('sets the roster, but not the access controls, while a roster mutation is in flight', () => {
            mockManageMembersBusy = true;
            const {hook} = render();

            expect(hook.current.rosterBusy).toBe(true);
            expect(hook.current.accessBusy).toBe(false);
        });
    });

    describe('busyReason / loadFailedReason', () => {
        it('names the in-flight write only while one is in flight', () => {
            expect(render().hook.current.busyReason).toBeUndefined();

            mockPermissionsState = {...mockPermissionsState, busy: true};
            expect(render().hook.current.busyReason).toBe('Saving…');
        });

        it('names a failed read so the surface can say so, and nothing otherwise', () => {
            expect(render().hook.current.loadFailedReason).toBeUndefined();

            mockPermissionsState = {...mockPermissionsState, loadFailed: true};
            expect(render().hook.current.loadFailedReason).toBe("Couldn't load this space's permissions. Close and reopen to try again.");
        });
    });

    describe('grantOptionsFor', () => {
        it('offers a guest nothing, since the server refuses every grant to one', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([['u2', member('u2', {is_guest: true})]]),
            };
            const {hook} = render();

            expect(hook.current.grantOptionsFor(profile('u2'))).toEqual([]);
        });

        it('offers the caller nothing on their own row unless they administer the space', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                canAdminister: false,
                members: new Map([['me', member('me', {is_admin: true})]]),
            };
            const {hook} = render();

            expect(hook.current.grantOptionsFor(profile('me'))).toEqual([]);
        });

        it('offers nothing to a caller who cannot manage members', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                canManageMembers: false,
                members: new Map([['u2', member('u2')]]),
            };
            const {hook} = render();

            expect(hook.current.grantOptionsFor(profile('u2'))).toEqual([]);
        });

        // admin_space is a space administrator's to give: a manager without it never sees the row.
        it('withholds admin_space from a manager who does not administer the space', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                canAdminister: false,
                members: new Map([['u2', member('u2')]]),
            };
            const {hook} = render();

            expect(hook.current.grantOptionsFor(profile('u2'))).toEqual(DEFAULT_PERMISSION_ORDER);
        });

        it('offers the whole vocabulary to an administrator', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([['u2', member('u2')]]),
            };
            const {hook} = render();

            expect(hook.current.grantOptionsFor(profile('u2'))).toEqual(MEMBER_PERMISSION_ORDER);
        });
    });

    describe('actions', () => {
        it('omits onRemove for a caller who cannot manage members', () => {
            mockPermissionsState = {...mockPermissionsState, canManageMembers: false};
            const {hook} = render();

            expect(hook.current.actions.onRemove).toBeUndefined();
        });

        it('includes onRemove that reaches the hook for a caller who can manage members', () => {
            const {hook} = render();

            hook.current.actions.onRemove?.('u2');

            expect(mockRemoveMember).toHaveBeenCalledWith('u2');
        });

        // Authority is expressed by omitting onRemove, so what remains for `disabled` to say is
        // that a write is already in flight — not that the caller may not act at all.
        it('disables actions only while a roster write is in flight', () => {
            mockPermissionsState = {...mockPermissionsState, canManageMembers: false};
            expect(render().hook.current.actions.disabled).toBe(false);

            mockManageMembersBusy = true;
            expect(render().hook.current.actions.disabled).toBe(true);
        });

        it('closes via onClose after a successful leave', async () => {
            const {hook, onClose} = render();

            await act(async () => {
                await hook.current.actions.onLeave();
            });

            expect(mockLeave).toHaveBeenCalled();
            expect(onClose).toHaveBeenCalled();
        });

        it('does not close when leave fails', async () => {
            mockLeave.mockResolvedValue(false);
            const {hook, onClose} = render();

            await act(async () => {
                await hook.current.actions.onLeave();
            });

            expect(mockLeave).toHaveBeenCalled();
            expect(onClose).not.toHaveBeenCalled();
        });
    });
});
