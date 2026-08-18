// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import {DocsModalController, closeAllDocsModals} from 'components/modals';

import PermissionsTab from './permissions_tab';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();
const mockLeave = jest.fn();

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

// AddMembersField renders the real people picker, which pulls in mattermost-redux's
// user search actions (published ESM that jest doesn't transform). Stub at the hook
// boundary, as share_space_modal.test.tsx does.
jest.mock('hooks/user_search', () => ({
    useUserSearch: () => ({results: [], loading: false}),
}));

const space = makeSpace('space-1', 'Engineering');

// Removing and leaving confirm first, and the confirmation is rendered by the
// imperative modal layer rather than inline, so it needs the controller mounted.
const renderTab = (onClose = jest.fn(), state?: Record<string, unknown>) => renderWithContext(
    <>
        <PermissionsTab
            space={space}
            onClose={onClose}
        />
        <DocsModalController/>
    </>,
    state ? {state} : undefined,
);

const confirm = async (name: RegExp) => {
    fireEvent.click(await screen.findByRole('button', {name}));
};

describe('PermissionsTab', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockLeave.mockResolvedValue(true);
    });

    afterEach(() => act(() => {
        closeAllDocsModals();
    }));

    // The tab used to fake this with an aria-disabled div; it is a real control now.
    it('offers a working add control', () => {
        renderTab();

        expect(screen.getByRole('button', {name: 'Add'})).toBeInTheDocument();
    });

    it('removes a member through the hook once confirmed', async () => {
        renderTab();

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));

        // The mutation waits on the confirmation.
        expect(mockRemoveMember).not.toHaveBeenCalled();

        await confirm(/Yes, remove/);

        await waitFor(() => expect(mockRemoveMember).toHaveBeenCalledWith('u2'));
    });

    it('does not remove a member when the confirmation is cancelled', async () => {
        renderTab();

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));
        await confirm(/Cancel/);

        expect(mockRemoveMember).not.toHaveBeenCalled();
    });

    it('keeps the space-access scaffolding in place', () => {
        renderTab();

        expect(screen.getByText('Public')).toBeInTheDocument();
        expect(screen.getByText('External sharing')).toBeInTheDocument();
    });

    // Leaving destroys your access to what is behind this tab, so the settings
    // modal must close too rather than sit open on a space you just left.
    it('leaves and closes the modal from your own row once confirmed', async () => {
        const onClose = jest.fn();

        renderTab(onClose, {currentUser: {id: 'me', username: 'caleb'}});

        fireEvent.click(screen.getByRole('button', {name: /Caleb/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Leave space'}));

        expect(mockLeave).not.toHaveBeenCalled();

        await confirm(/Yes, leave space/);

        await waitFor(() => expect(mockLeave).toHaveBeenCalled());
        await waitFor(() => expect(onClose).toHaveBeenCalled());
    });
});
