// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

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

describe('PermissionsTab', () => {
    beforeEach(() => jest.clearAllMocks());

    // The tab used to fake this with an aria-disabled div; it is a real control now.
    it('offers a working add control', () => {
        renderWithContext(
            <PermissionsTab
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(screen.getByRole('button', {name: 'Add'})).toBeInTheDocument();
    });

    it('removes a member through the hook', async () => {
        renderWithContext(
            <PermissionsTab
                space={space}
                onClose={jest.fn()}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));

        expect(mockRemoveMember).toHaveBeenCalledWith('u2');
    });

    it('keeps the space-access scaffolding in place', () => {
        renderWithContext(
            <PermissionsTab
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(screen.getByText('Public')).toBeInTheDocument();
        expect(screen.getByText('External sharing')).toBeInTheDocument();
    });

    // Leaving destroys your access to what is behind this tab, so the settings
    // modal must close too rather than sit open on a space you just left.
    it('leaves and closes the modal from your own row', async () => {
        mockLeave.mockResolvedValue(undefined);
        const onClose = jest.fn();

        renderWithContext(
            <PermissionsTab
                space={space}
                onClose={onClose}
            />,
            {state: {currentUser: {id: 'me', username: 'caleb'}}},
        );

        fireEvent.click(screen.getByRole('button', {name: /Caleb/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Leave space'}));

        expect(mockLeave).toHaveBeenCalled();
        await waitFor(() => expect(onClose).toHaveBeenCalled());
    });
});
