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

    describe('adminLocked / rosterLocked', () => {
        it('is unlocked for an administrator once the read resolves', () => {
            const {hook} = render();

            expect(hook.current.adminLocked).toBe(false);
            expect(hook.current.rosterLocked).toBe(false);
        });

        it('locks admin controls for a caller who cannot administer', () => {
            mockPermissionsState = {...mockPermissionsState, canAdminister: false};
            const {hook} = render();

            expect(hook.current.adminLocked).toBe(true);
        });

        it('locks roster controls for a caller who cannot manage members', () => {
            mockPermissionsState = {...mockPermissionsState, canManageMembers: false};
            const {hook} = render();

            expect(hook.current.rosterLocked).toBe(true);
        });

        it('locks both while the permission read is loading', () => {
            mockPermissionsState = {...mockPermissionsState, loading: true};
            const {hook} = render();

            expect(hook.current.adminLocked).toBe(true);
            expect(hook.current.rosterLocked).toBe(true);
        });

        it('locks the roster, but not the admin controls, while a roster mutation is in flight', () => {
            mockManageMembersBusy = true;
            const {hook} = render();

            expect(hook.current.rosterLocked).toBe(true);
            expect(hook.current.adminLocked).toBe(false);
        });
    });

    describe('adminLockedReason', () => {
        it('is undefined while the read is in flight, so a real denial is not implied', () => {
            mockPermissionsState = {...mockPermissionsState, loading: true};
            const {hook} = render();

            expect(hook.current.adminLockedReason).toBeUndefined();
        });

        it('is undefined while a write is in flight', () => {
            mockPermissionsState = {...mockPermissionsState, busy: true};
            const {hook} = render();

            expect(hook.current.adminLockedReason).toBeUndefined();
        });

        it('names the load failure ahead of the admin-only reason', () => {
            mockPermissionsState = {...mockPermissionsState, loadFailed: true, loading: true};
            const {hook} = render();

            expect(hook.current.adminLockedReason).toBe("Couldn't load this space's permissions. Close and reopen to try again.");
        });

        it('names the admin-only reason once the read has resolved', () => {
            const {hook} = render();

            expect(hook.current.adminLockedReason).toBe('Only a space administrator can change this');
        });
    });

    describe('memberLockedReason / isMemberLocked', () => {
        it('locks and names a guest even when the roster itself is unlocked', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([['u2', member('u2', {is_guest: true})]]),
            };
            const {hook} = render();
            const ada = profile('u2');

            expect(hook.current.isMemberLocked(ada)).toBe(true);
            expect(hook.current.memberLockedReason(ada)).toBe('Guests can only view pages');
        });

        it("locks and names the caller's own row when they cannot administer", () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                canAdminister: false,
                members: new Map([['me', member('me', {is_admin: true})]]),
            };
            const {hook} = render();
            const caleb = profile('me');

            expect(hook.current.isMemberLocked(caleb)).toBe(true);
            expect(hook.current.memberLockedReason(caleb)).toBe('Only a space administrator can change their own permissions');
        });

        it('names the admin lock reason for another member once the roster is locked', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                canManageMembers: false,
                members: new Map([['u2', member('u2')]]),
            };
            const {hook} = render();
            const ada = profile('u2');

            expect(hook.current.isMemberLocked(ada)).toBe(true);
            expect(hook.current.memberLockedReason(ada)).toBe('Only a space administrator can change this');
        });

        it('leaves an ordinary member unlocked with no reason', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([['u2', member('u2')]]),
            };
            const {hook} = render();
            const ada = profile('u2');

            expect(hook.current.isMemberLocked(ada)).toBe(false);
            expect(hook.current.memberLockedReason(ada)).toBeUndefined();
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

        it('disables actions when the roster is locked', () => {
            mockPermissionsState = {...mockPermissionsState, canManageMembers: false};
            const {hook} = render();

            expect(hook.current.actions.disabled).toBe(true);
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
