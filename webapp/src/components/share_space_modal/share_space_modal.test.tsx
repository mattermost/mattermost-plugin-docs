// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import ShareSpaceModal from './share_space_modal';

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

describe('ShareSpaceModal', () => {
    beforeEach(() => jest.clearAllMocks());

    it('lists the members with the current user marked', () => {
        renderWithContext(
            <ShareSpaceModal
                space={space}
                onClose={jest.fn()}
            />,
            {state},
        );

        expect(screen.getByText('Caleb')).toBeInTheDocument();
        expect(screen.getByText('(You)')).toBeInTheDocument();
        expect(screen.getByText('Ada')).toBeInTheDocument();
    });

    it('removes another member through the hook', async () => {
        renderWithContext(
            <ShareSpaceModal
                space={space}
                onClose={jest.fn()}
            />,
            {state},
        );

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));

        expect(mockRemoveMember).toHaveBeenCalledWith('u2');
    });

    // Leaving destroys your access to what is behind the modal, so the modal goes too.
    it('leaves and closes the modal from your own row', async () => {
        mockLeave.mockResolvedValue(undefined);
        const onClose = jest.fn();

        renderWithContext(
            <ShareSpaceModal
                space={space}
                onClose={onClose}
            />,
            {state},
        );

        fireEvent.click(screen.getByRole('button', {name: /Caleb/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Leave space'}));

        expect(mockLeave).toHaveBeenCalled();
        await waitFor(() => expect(onClose).toHaveBeenCalled());
    });
});
