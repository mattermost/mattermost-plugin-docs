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
        setSpaceViewAccess: jest.fn(),
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
    view_access: 'open',
    update_at: 100,
};

const OPEN_VISIBILITY_LABEL = 'Open — Anyone on this team can find and read this space.';
const PRIVATE_VISIBILITY_LABEL = 'Private — Only members can find and read this space.';

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
const visibilitySection = () => within(screen.getByRole('group', {name: 'Space visibility'}));

describe('SpaceSettingsModal', () => {
    beforeEach(() => {
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

    it('shows the current space visibility', async () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        expect(visibilitySection().getByLabelText(OPEN_VISIBILITY_LABEL)).toBeChecked();
        expect(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL)).not.toBeChecked();
    });

    it('flips visibility from open to private, sending expected_update_at, and reflects the response', async () => {
        api.setSpaceViewAccess.mockResolvedValue({...adminAccess, view_access: 'private', update_at: 101});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        fireEvent.click(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL));
        fireEvent.click(screen.getByRole('button', {name: 'Save visibility'}));

        await waitFor(() => expect(api.setSpaceViewAccess).toHaveBeenCalledWith('space1', 'private', 100));
        await waitFor(() => expect(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL)).toBeChecked());
        expect(screen.getByRole('button', {name: 'Save visibility'})).toBeDisabled();
    });

    it('renders the visibility section read-only for a caller without space-admin', async () => {
        api.getSpaceAccess.mockResolvedValue({...adminAccess, capabilities: ['read_page', 'create_page']});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        expect(visibilitySection().getByLabelText(OPEN_VISIBILITY_LABEL)).toBeDisabled();
        expect(await screen.findByText('Only a space administrator can change the space visibility.')).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Save visibility'})).not.toBeInTheDocument();
    });

    it('surfaces the server message when a visibility save conflicts', async () => {
        api.setSpaceViewAccess.mockRejectedValue(new RestError('http://localhost/x', 409, 'The space changed since you loaded it.', undefined));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        fireEvent.click(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL));
        fireEvent.click(screen.getByRole('button', {name: 'Save visibility'}));

        expect(await screen.findByRole('alert')).toHaveTextContent('The space changed since you loaded it.');
    });

    it('sends the refreshed update_at baseline on a second save', async () => {
        api.setSpaceViewAccess.mockResolvedValueOnce({...adminAccess, view_access: 'private', update_at: 101});
        api.setSpaceViewAccess.mockResolvedValueOnce({...adminAccess, view_access: 'open', update_at: 102});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        fireEvent.click(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL));
        fireEvent.click(screen.getByRole('button', {name: 'Save visibility'}));
        await waitFor(() => expect(screen.getByRole('button', {name: 'Save visibility'})).toBeDisabled());

        fireEvent.click(visibilitySection().getByLabelText(OPEN_VISIBILITY_LABEL));
        fireEvent.click(screen.getByRole('button', {name: 'Save visibility'}));

        await waitFor(() => expect(api.setSpaceViewAccess).toHaveBeenNthCalledWith(2, 'space1', 'open', 101));
    });

    it('re-baselines from the server after a conflict so a retry can succeed', async () => {
        api.setSpaceViewAccess.mockRejectedValueOnce(new RestError('http://localhost/x', 409, 'The space changed since you loaded it.', undefined));
        api.setSpaceViewAccess.mockResolvedValueOnce({...adminAccess, view_access: 'private', update_at: 151});
        api.getSpaceAccess.mockResolvedValueOnce(adminAccess);
        api.getSpaceAccess.mockResolvedValueOnce({...adminAccess, update_at: 150});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        fireEvent.click(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL));
        fireEvent.click(screen.getByRole('button', {name: 'Save visibility'}));

        expect(await screen.findByRole('alert')).toHaveTextContent('The space changed since you loaded it.');
        await waitFor(() => expect(screen.getByRole('button', {name: 'Save visibility'})).not.toBeDisabled());

        fireEvent.click(screen.getByRole('button', {name: 'Save visibility'}));

        await waitFor(() => expect(api.setSpaceViewAccess).toHaveBeenNthCalledWith(2, 'space1', 'private', 150));
        await waitFor(() => expect(screen.getByRole('button', {name: 'Save visibility'})).toBeDisabled());
    });

    it('loads an already-private space with no pending change and no privatize note', async () => {
        api.getSpaceAccess.mockResolvedValue({...adminAccess, view_access: 'private'});

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        expect(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL)).toBeChecked();
        expect(visibilitySection().getByLabelText(OPEN_VISIBILITY_LABEL)).not.toBeChecked();
        expect(screen.queryByText(/Members keep their access/)).not.toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Save visibility'})).toBeDisabled();
    });

    it('shows the privatize note only while the pending selection is private and the saved value is open', async () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Space visibility'});
        expect(screen.queryByText(/Members keep their access/)).not.toBeInTheDocument();

        fireEvent.click(visibilitySection().getByLabelText(PRIVATE_VISIBILITY_LABEL));
        expect(screen.getByText(/Members keep their access/)).toBeInTheDocument();

        fireEvent.click(visibilitySection().getByLabelText(OPEN_VISIBILITY_LABEL));
        expect(screen.queryByText(/Members keep their access/)).not.toBeInTheDocument();
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

    it('shows an error and hides the defaults and members sections when the initial load fails', async () => {
        api.getSpaceAccess.mockRejectedValue(new RestError('http://localhost/x', 500, 'Something went wrong.', undefined));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong.');
        expect(screen.queryByRole('group', {name: 'Default permissions'})).not.toBeInTheDocument();
        expect(screen.queryByText('Individual members')).not.toBeInTheDocument();
    });

    it('surfaces the server message when saving defaults is refused, without losing the panel', async () => {
        api.setDefaultCapabilities.mockRejectedValue(new RestError('http://localhost/x', 409, 'Defaults could not be saved.', undefined));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Default permissions'});
        fireEvent.click(defaultSection().getByLabelText('Comment on pages'));
        fireEvent.click(screen.getByRole('button', {name: 'Save defaults'}));

        expect(await screen.findByRole('alert')).toHaveTextContent('Defaults could not be saved.');
        expect(defaultSection().getByLabelText('Create pages')).toBeInTheDocument();
    });

    it('keeps the toggled input enabled and focused while its save is in flight', async () => {
        let resolveSave: (value: unknown) => void = () => {};
        api.setMemberCapabilities.mockImplementation(() => new Promise((resolve) => {
            resolveSave = resolve;
        }));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        const checkbox = memberSection('bob').getByLabelText('Administer space');
        checkbox.focus();
        fireEvent.click(checkbox);

        expect(checkbox).not.toBeDisabled();
        expect(checkbox).toHaveFocus();

        resolveSave({...ordinaryMember, granted_capabilities: ['admin_space']});
        await waitFor(() => expect(checkbox).toBeChecked());
    });

    it('persists both capabilities when a member is toggled twice before the first save settles', async () => {
        let resolveFirst: (value: unknown) => void = () => {};
        const firstSave = new Promise((resolve) => {
            resolveFirst = resolve;
        });
        api.setMemberCapabilities.
            mockImplementationOnce(() => firstSave).
            mockImplementationOnce(() => Promise.resolve({...ordinaryMember, granted_capabilities: ['delete_page', 'admin_space']}));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Administer space'));
        fireEvent.click(memberSection('bob').getByLabelText('Delete any page'));

        expect(api.setMemberCapabilities).toHaveBeenCalledTimes(1);
        expect(memberSection('bob').getByLabelText('Delete any page')).toBeChecked();

        resolveFirst({...ordinaryMember, granted_capabilities: ['admin_space']});

        await waitFor(() => expect(api.setMemberCapabilities).toHaveBeenCalledTimes(2));
        expect(api.setMemberCapabilities).toHaveBeenLastCalledWith('space1', 'user2', ['delete_page', 'admin_space']);
        await waitFor(() => {
            expect(memberSection('bob').getByLabelText('Administer space')).toBeChecked();
            expect(memberSection('bob').getByLabelText('Delete any page')).toBeChecked();
        });
    });

    it('reconciles to the last server-confirmed set when the coalesced save fails', async () => {
        let resolveFirst: (value: unknown) => void = () => {};
        const firstSave = new Promise((resolve) => {
            resolveFirst = resolve;
        });
        api.setMemberCapabilities.
            mockImplementationOnce(() => firstSave).
            mockImplementationOnce(() => Promise.reject(new RestError('http://localhost/x', 409, 'This is the last administrator of the space.', undefined)));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Administer space'));
        fireEvent.click(memberSection('bob').getByLabelText('Delete any page'));

        resolveFirst({...ordinaryMember, granted_capabilities: ['admin_space']});

        expect(await screen.findByRole('alert')).toHaveTextContent('This is the last administrator of the space.');
        await waitFor(() => {
            expect(memberSection('bob').getByLabelText('Administer space')).toBeChecked();
            expect(memberSection('bob').getByLabelText('Delete any page')).not.toBeChecked();
        });
    });

    it('gives the coalesced toggle its own attempt after a failed save, and surfaces success if it lands', async () => {
        let rejectFirst: (reason?: unknown) => void = () => {};
        const firstSave = new Promise((_resolve, reject) => {
            rejectFirst = reject;
        });
        api.setMemberCapabilities.
            mockImplementationOnce(() => firstSave).
            mockImplementationOnce(() => Promise.resolve({...ordinaryMember, granted_capabilities: ['delete_page', 'admin_space']}));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Administer space'));
        fireEvent.click(memberSection('bob').getByLabelText('Delete any page'));

        rejectFirst(new RestError('http://localhost/x', 409, 'This is the last administrator of the space.', undefined));

        await waitFor(() => expect(api.setMemberCapabilities).toHaveBeenCalledTimes(2));
        expect(api.setMemberCapabilities).toHaveBeenLastCalledWith('space1', 'user2', ['delete_page', 'admin_space']);
        await waitFor(() => {
            expect(memberSection('bob').getByLabelText('Administer space')).toBeChecked();
            expect(memberSection('bob').getByLabelText('Delete any page')).toBeChecked();
        });
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('reverts to the original state and surfaces the error when both the initial and coalesced saves fail', async () => {
        let rejectFirst: (reason?: unknown) => void = () => {};
        const firstSave = new Promise((_resolve, reject) => {
            rejectFirst = reject;
        });
        api.setMemberCapabilities.
            mockImplementationOnce(() => firstSave).
            mockImplementationOnce(() => Promise.reject(new RestError('http://localhost/x', 409, 'This is the last administrator of the space.', undefined)));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Administer space'));
        fireEvent.click(memberSection('bob').getByLabelText('Delete any page'));

        rejectFirst(new RestError('http://localhost/x', 500, 'Something went wrong.', undefined));

        expect(await screen.findByRole('alert')).toHaveTextContent('This is the last administrator of the space.');
        await waitFor(() => {
            expect(memberSection('bob').getByLabelText('Administer space')).not.toBeChecked();
            expect(memberSection('bob').getByLabelText('Delete any page')).not.toBeChecked();
        });
    });

    it('lets two members save concurrently without one clearing the other in-flight state', async () => {
        const memberTwo = {
            user_id: 'user3',
            capabilities: ['read_page', 'create_page', 'edit_page'],
            granted_capabilities: [],
            is_admin: false,
            is_guest: false,
        };
        api.getSpaceMembers.mockResolvedValue(memberPage([ordinaryMember, memberTwo]));
        api.getMemberProfiles.mockResolvedValue([
            {id: 'user2', username: 'bob'},
            {id: 'user3', username: 'ann'},
        ]);

        let resolveFirst: (value: unknown) => void = () => {};
        const firstSave = new Promise((resolve) => {
            resolveFirst = resolve;
        });
        api.setMemberCapabilities.mockImplementation((_spaceId: string, userId: string) => {
            if (userId === 'user2') {
                return firstSave;
            }
            return Promise.resolve({...memberTwo, granted_capabilities: ['delete_page']});
        });

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        await screen.findByRole('group', {name: 'Permissions for bob'});
        fireEvent.click(memberSection('bob').getByLabelText('Administer space'));
        fireEvent.click(memberSection('ann').getByLabelText('Delete any page'));

        await waitFor(() => expect(memberSection('ann').getByLabelText('Delete any page')).toBeChecked());
        expect(memberSection('bob').getByLabelText('Administer space')).toBeChecked();

        resolveFirst({...ordinaryMember, granted_capabilities: ['admin_space']});
        await waitFor(() => expect(memberSection('bob').getByLabelText('Administer space')).toBeChecked());
    });

    it('shows an automatic-join note for a member added without an explicit grant', async () => {
        api.getSpaceMembers.mockResolvedValue(memberPage([{...ordinaryMember, auto_joined: true}]));

        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(await screen.findByText('Joined automatically')).toBeInTheDocument();
    });
});
