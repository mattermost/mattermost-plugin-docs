// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor, within} from '@testing-library/react';
import {RestError} from 'client/rest';
import React from 'react';

import type {Space} from 'types/docs';

import SpaceSettingsModal from './space_settings_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('client/space_permissions', () => {
    const actual = jest.requireActual('client/space_permissions');
    return {
        ...actual,
        getSpaceAccess: jest.fn(),
        getSpaceMembers: jest.fn(),
        getMemberProfiles: jest.fn(),
        setDefaultCapabilities: jest.fn(),
        setMemberCapabilities: jest.fn(),
    };
});

const api = jest.requireMock('client/space_permissions');

const space = {
    id: 'space1',
    team_id: 'team1',
    creator_id: 'user1',
    title: 'Handbook',
    props: {},
    create_at: 0,
    update_at: 0,
    delete_at: 0,
    sort_order: 0,
} as Space;

const adminAccess = {
    id: 'space1',
    default_capabilities: ['create_page', 'edit_page'],
    capabilities: ['read_page', 'create_page', 'edit_page', 'admin_space'],
};

const memberPage = (members: unknown[], hasMore = false) => ({
    items: members,
    page: 0,
    per_page: 100,
    has_more: hasMore,
});

const ordinaryMember = {
    user_id: 'user2',
    capabilities: ['read_page', 'create_page', 'edit_page'],
    granted_capabilities: [],
    is_admin: false,
    is_guest: false,
};

// The two capability sets on this screen carry the same labels, so every query
// is scoped to the fieldset it belongs to.
const defaultSection = () => within(screen.getByRole('group', {name: 'Default permissions'}));
const memberSection = (name: string) => within(screen.getByRole('group', {name: `Permissions for ${name}`}));

describe('SpaceSettingsModal', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        api.getSpaceAccess.mockResolvedValue(adminAccess);
        api.getSpaceMembers.mockResolvedValue(memberPage([ordinaryMember]));
        api.getMemberProfiles.mockResolvedValue([{id: 'user2', username: 'bob'}]);
    });

    it('shows the space default with the granted capabilities checked', async () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Default permissions'});
        expect(defaultSection().getByLabelText('Create pages')).toBeChecked();
        expect(defaultSection().getByLabelText('Edit pages')).toBeChecked();
        expect(defaultSection().getByLabelText('Comment on pages')).not.toBeChecked();
    });

    it('sends the whole default set, not just the toggled capability', async () => {
        api.setDefaultCapabilities.mockResolvedValue({...adminAccess, default_capabilities: ['create_page', 'comment_page', 'edit_page']});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Default permissions'});
        fireEvent.click(defaultSection().getByLabelText('Comment on pages'));
        fireEvent.click(screen.getByRole('button', {name: 'Save defaults'}));

        await waitFor(() => expect(api.setDefaultCapabilities).toHaveBeenCalledWith('space1', ['create_page', 'comment_page', 'edit_page']));
    });

    it('does not offer admin_space as a space default', async () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Default permissions'});
        expect(defaultSection().queryByLabelText('Administer space')).not.toBeInTheDocument();
        expect(memberSection('bob').getByLabelText('Administer space')).toBeInTheDocument();
    });

    it('clears a member grant with an empty array rather than an omitted field', async () => {
        api.getSpaceMembers.mockResolvedValue(memberPage([{...ordinaryMember, granted_capabilities: ['delete_page']}]));
        api.setMemberCapabilities.mockResolvedValue({...ordinaryMember, granted_capabilities: []});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Delete any page'));

        await waitFor(() => expect(api.setMemberCapabilities).toHaveBeenCalledWith('space1', 'user2', []));
    });

    it('renders a guest as read-only with no capability checkboxes', async () => {
        api.getSpaceMembers.mockResolvedValue(memberPage([{
            user_id: 'guest1',
            capabilities: ['read_page'],
            granted_capabilities: [],
            is_admin: false,
            is_guest: true,
        }]));
        api.getMemberProfiles.mockResolvedValue([{id: 'guest1', username: 'carl'}]);

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByText('Guests can only view pages, and cannot be granted more.')).toBeInTheDocument();
        expect(screen.queryByRole('group', {name: 'Permissions for carl'})).not.toBeInTheDocument();
    });

    it('renders the default section read-only without admin_space', async () => {
        api.getSpaceAccess.mockResolvedValue({...adminAccess, capabilities: ['read_page', 'create_page']});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByText('Only a space administrator can change the default permissions.')).toBeInTheDocument();
        expect(defaultSection().getByLabelText('Create pages')).toBeDisabled();
        expect(screen.queryByRole('button', {name: 'Save defaults'})).not.toBeInTheDocument();
    });

    it('hides the member list when the caller may read but not manage the space', async () => {
        api.getSpaceMembers.mockRejectedValue(new RestError('http://localhost/x', 403, 'Forbidden', undefined));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Default permissions'});
        expect(screen.queryByText('Individual members')).not.toBeInTheDocument();
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('surfaces the server message when a save is refused', async () => {
        api.setMemberCapabilities.mockRejectedValue(new RestError('http://localhost/x', 409, 'This is the last administrator of the space.', undefined));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Administer space'));

        expect(await screen.findByRole('alert')).toHaveTextContent('This is the last administrator of the space.');
    });

    it('falls back to the user id when the profile lookup fails', async () => {
        api.getMemberProfiles.mockRejectedValue(new Error('network'));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByText('user2')).toBeInTheDocument();
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
});
