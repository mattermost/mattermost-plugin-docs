// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import {DocsModalController, closeAllDocsModals} from 'components/modals';

import ShareSpaceModal from './share_space_modal';

import {renderWithContext, type TestStateOptions} from '../../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();
const mockLeave = jest.fn();
const mockSetDefaults = jest.fn();
const mockSetViewAccess = jest.fn();
const mockSetMemberGrants = jest.fn();
let mockPermissionsState: Record<string, unknown>;

jest.mock('hooks/space_permissions', () => ({
    useSpacePermissions: () => mockPermissionsState,
}));

jest.mock('hooks/space_members', () => ({
    useManageSpaceMembers: () => ({
        addMembers: mockAddMembers,
        removeMember: mockRemoveMember,
        leave: mockLeave,
        busy: false,
    }),
}));

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => [
        {id: 'me', displayName: 'Caleb', username: 'caleb', avatarUrl: ''},
        {id: 'u2', displayName: 'Ada', username: 'ada', avatarUrl: ''},
    ],
}));

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({paths: {space: (id: string) => `/team/spaces/${id}`}}),
}));

// AddMembersField renders the real people picker, which pulls in mattermost-redux's
// user search actions (published ESM that jest doesn't transform). Stub at the hook
// boundary, as people_picker.test.tsx does.
jest.mock('hooks/user_search', () => ({
    useUserSearch: () => ({results: [], loading: false}),
}));

const space = makeSpace('space-1', 'Engineering');
const state = {currentUser: {id: 'me', username: 'caleb'}};

// Removing and leaving confirm first, and the confirmation is rendered by the
// imperative modal layer rather than inline, so it needs the controller mounted.
const renderModal = (onClose = jest.fn(), stateOverrides: TestStateOptions = {}) => renderWithContext(
    <>
        <ShareSpaceModal
            space={space}
            onClose={onClose}
        />
        <DocsModalController/>
    </>,
    {
        state: {
            ...state,
            license: {CustomPermissionsSchemes: 'true'},
            ...stateOverrides,
        },
    },
);

const confirm = async (name: RegExp) => {
    fireEvent.click(await screen.findByRole('button', {name}));
};

describe('ShareSpaceModal', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockLeave.mockResolvedValue(true);
        mockSetDefaults.mockResolvedValue(undefined);
        mockSetViewAccess.mockResolvedValue(undefined);
        mockSetMemberGrants.mockResolvedValue(undefined);
        mockPermissionsState = {
            defaults: ['create_page', 'comment_page', 'edit_page', 'delete_own_page'],
            viewAccess: 'open',
            members: new Map([
                ['me', {user_id: 'me', permissions: ['read_page'], granted_permissions: ['admin_space'], is_admin: true, is_guest: false, auto_joined: false}],
                ['u2', {user_id: 'u2', permissions: ['read_page', 'comment_page'], granted_permissions: [], is_admin: false, is_guest: false, auto_joined: false}],
            ]),
            canAdminister: true,
            canManageMembers: true,
            loading: false,
            loadFailed: false,
            busy: false,
            setDefaults: mockSetDefaults,
            setViewAccess: mockSetViewAccess,
            setMemberGrants: mockSetMemberGrants,
        };
    });

    afterEach(() => act(() => {
        closeAllDocsModals();
    }));

    it('lists the members with the current user marked', () => {
        renderModal();

        expect(screen.getByRole('dialog', {name: "Share 'Engineering'"})).toBeInTheDocument();
        expect(screen.getByText('Caleb')).toBeInTheDocument();
        expect(screen.getByText('(You)')).toBeInTheDocument();
        expect(screen.getByText('Ada')).toBeInTheDocument();
    });

    it('shows compact permission summaries and exposes granular capabilities in the existing row menu', async () => {
        renderModal();

        expect(screen.getByRole('button', {name: 'Permissions for Caleb'})).toHaveTextContent('Admin');
        expect(screen.getByRole('button', {name: 'Permissions for Ada'})).toHaveTextContent('Can comment');

        fireEvent.click(screen.getByRole('button', {name: 'Permissions for Ada'}));

        expect(await screen.findByRole('menuitemcheckbox', {name: 'Create pages'})).not.toBeChecked();
        expect(screen.getByRole('menuitemcheckbox', {name: 'Administer space'})).not.toBeChecked();
        expect(await screen.findByRole('menuitem', {name: 'Remove from space'})).toBeInTheDocument();
    });

    it('changes a member grant from the row capability menu', async () => {
        renderModal();

        fireEvent.click(screen.getByRole('button', {name: 'Permissions for Ada'}));
        fireEvent.click(await screen.findByRole('menuitemcheckbox', {name: 'Edit pages'}));

        expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', ['edit_page']);
        expect(screen.getByRole('menu', {name: /Ada/})).toBeInTheDocument();
    });

    it('changes visibility and granular defaults from the Share footer', async () => {
        renderModal();

        fireEvent.click(screen.getByRole('button', {name: /Public/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Private'}));
        expect(mockSetViewAccess).toHaveBeenCalledWith('private');

        fireEvent.click(screen.getByRole('button', {name: /Can edit/}));
        fireEvent.click(await screen.findByRole('menuitemcheckbox', {name: 'Delete any page'}));
        expect(mockSetDefaults).toHaveBeenCalledWith([
            'create_page',
            'comment_page',
            'edit_page',
            'delete_own_page',
            'delete_page',
        ]);
    });

    it('uses the included presets in the same default-permission menu without a custom-schemes license', async () => {
        renderModal(jest.fn(), {license: {}});

        fireEvent.click(screen.getByRole('button', {name: /Can edit/}));
        expect(await screen.findByRole('menuitem', {name: /Contribute/})).toBeInTheDocument();
        expect(screen.queryByRole('menuitemcheckbox', {name: 'Create pages'})).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole('menuitem', {name: /Comment/}));
        expect(mockSetDefaults).toHaveBeenCalledWith(['comment_page']);
    });

    it('removes another member through the hook once confirmed', async () => {
        renderModal();

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));

        // The mutation waits on the confirmation.
        expect(mockRemoveMember).not.toHaveBeenCalled();

        await confirm(/Yes, remove/);

        await waitFor(() => expect(mockRemoveMember).toHaveBeenCalledWith('u2'));
    });

    it('does not remove a member when the confirmation is cancelled', async () => {
        renderModal();

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));
        await confirm(/Cancel/);

        expect(mockRemoveMember).not.toHaveBeenCalled();
    });

    // Leaving destroys your access to what is behind the modal, so the modal goes too.
    it('leaves and closes the modal from your own row once confirmed', async () => {
        mockPermissionsState = {
            ...mockPermissionsState,
            members: new Map(),
            canAdminister: false,
            canManageMembers: false,
        };
        const onClose = jest.fn();

        renderModal(onClose);

        expect(screen.queryByRole('button', {name: 'Add'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: /Ada/})).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', {name: /Caleb/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Leave space'}));

        expect(mockLeave).not.toHaveBeenCalled();

        await confirm(/Yes, leave space/);

        await waitFor(() => expect(mockLeave).toHaveBeenCalled());
        await waitFor(() => expect(onClose).toHaveBeenCalled());
    });

    it('keeps the modal open when leaving fails', async () => {
        mockPermissionsState = {
            ...mockPermissionsState,
            members: new Map(),
            canAdminister: false,
            canManageMembers: false,
        };
        mockLeave.mockResolvedValue(false);
        const onClose = jest.fn();
        renderModal(onClose);

        fireEvent.click(screen.getByRole('button', {name: /Caleb/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Leave space'}));
        await confirm(/Yes, leave space/);

        await waitFor(() => expect(mockLeave).toHaveBeenCalled());
        expect(onClose).not.toHaveBeenCalled();
    });
});
