// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import {DocsModalController, closeAllDocsModals} from 'components/modals';

import ShareSpaceModal from './share_space_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();
const mockLeave = jest.fn();
let mockCanManageMembers = true;

jest.mock('hooks/permissions', () => ({
    useCanManageSpaceMembers: () => mockCanManageMembers,
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
const renderModal = (onClose = jest.fn()) => renderWithContext(
    <>
        <ShareSpaceModal
            space={space}
            onClose={onClose}
        />
        <DocsModalController/>
    </>,
    {state},
);

const confirm = async (name: RegExp) => {
    fireEvent.click(await screen.findByRole('button', {name}));
};

describe('ShareSpaceModal', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockCanManageMembers = true;
        mockLeave.mockResolvedValue(true);
    });

    afterEach(() => act(() => {
        closeAllDocsModals();
    }));

    it('lists the members with the current user marked', () => {
        renderModal();

        expect(screen.getByText('Caleb')).toBeInTheDocument();
        expect(screen.getByText('(You)')).toBeInTheDocument();
        expect(screen.getByText('Ada')).toBeInTheDocument();
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
        mockCanManageMembers = false;
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
        mockCanManageMembers = false;
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
