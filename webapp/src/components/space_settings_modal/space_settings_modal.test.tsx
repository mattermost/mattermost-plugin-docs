// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {deleteSpace, updateSpace} from 'store/actions';
import {makeSpace} from 'store/test_fixtures';

import {DocsModalController, closeAllDocsModals} from 'components/modals';

import {Permissions} from 'types/permissions';
import type {Permission} from 'types/permissions';

import SpaceSettingsModal from './space_settings_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockGoHome = jest.fn();

jest.mock('store/actions', () => ({
    updateSpace: jest.fn(() => () => Promise.resolve()),
    deleteSpace: jest.fn(() => () => Promise.resolve()),
    fetchPages: jest.fn(() => () => Promise.resolve([])),
}));

const mockUpdateSpace = updateSpace as jest.Mock;
const mockDeleteSpace = deleteSpace as jest.Mock;

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({
        goHome: mockGoHome,
        paths: {space: (id: string) => `/team/spaces/${id}`},
    }),
}));

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => [],
}));

jest.mock('hooks/space_members', () => ({
    useManageSpaceMembers: () => ({
        addMembers: jest.fn(),
        removeMember: jest.fn(),
        leave: jest.fn(),
        busy: false,
    }),
}));

// AddMembersField (rendered by the extracted PermissionsTab) pulls in the real
// PeoplePicker, which reaches mattermost-redux's ESM user-search actions that Jest
// can't transform. Stub at the hook boundary, as share_space_modal.test.tsx does.
jest.mock('hooks/user_search', () => ({
    useUserSearch: () => ({results: [], loading: false}),
}));

const space = makeSpace('space-1', 'Project Avalanche');

// The archive tab reads the caller's delete tier out of the stored space record, so every case
// that expects to see it has to render against a store that carries one.
const stateWithPermissions = (permissions: Permission[]) => ({
    docs: {spaces: {[space.id]: {...space, permissions}}},
});

const canArchive = stateWithPermissions([Permissions.DELETE_SPACE]);

describe('SpaceSettingsModal', () => {
    afterEach(() => {
        closeAllDocsModals();
    });

    it('renders the title and section tabs, with the initial tab selected', () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
            {state: canArchive},
        );

        expect(screen.getByRole('heading', {name: 'Space Settings'})).toBeInTheDocument();
        expect(screen.getByRole('tab', {name: /Info/, selected: true})).toBeInTheDocument();
        expect(screen.getByRole('tab', {name: /Permissions/, selected: false})).toBeInTheDocument();
        expect(screen.getByRole('tab', {name: /Configuration/})).toBeInTheDocument();
        expect(screen.getByRole('tab', {name: /Archive space/})).toBeInTheDocument();
    });

    // Opening these settings takes the manage tier; archiving takes the delete tier, which is a
    // separate team permission. A caller holding only the former reaches this modal and must not
    // be offered an action the archive route would refuse.
    it('withholds the archive tab from a caller without the delete tier', () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
            {state: stateWithPermissions([Permissions.MANAGE_SPACE])},
        );

        expect(screen.getByRole('tab', {name: /Configuration/})).toBeInTheDocument();
        expect(screen.queryByRole('tab', {name: /Archive space/})).not.toBeInTheDocument();
    });

    it('surfaces the save bar after a change, then dispatches updateSpace', async () => {
        const onClose = jest.fn();
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={onClose}
            />,
        );

        // The floating save bar only appears once there are unsaved changes.
        expect(screen.queryByRole('button', {name: 'Save'})).not.toBeInTheDocument();

        fireEvent.change(screen.getByLabelText('Space name'), {target: {value: 'Renamed'}});

        const save = screen.getByRole('button', {name: 'Save'});
        expect(save).toBeEnabled();

        fireEvent.click(save);

        // Props are sent merged (they replace the map server-side); the landing
        // page is unset, so it saves as the space front door.
        expect(mockUpdateSpace).toHaveBeenCalledWith('space-1', {
            title: 'Renamed',
            description: '',
            props: {default_page_id: ''},
        });
        await waitFor(() => expect(save).toBeEnabled());
    });

    it('renders a failed save once in the save bar', async () => {
        mockUpdateSpace.mockImplementationOnce(() => () => Promise.reject(new Error('Save failed')));
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        fireEvent.change(screen.getByLabelText('Space name'), {target: {value: 'Renamed'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(screen.getAllByText('Save failed')).toHaveLength(1));
    });

    it('archives the space through a confirm dialog', async () => {
        const onClose = jest.fn();
        renderWithContext(
            <>
                <SpaceSettingsModal
                    space={space}
                    onClose={onClose}
                    initialTab='archive'
                />
                <DocsModalController/>
            </>,
        );

        // The nav is a tab (role="tab"); the in-card action is the only button
        // named "Archive space". It opens a confirm dialog through the modal
        // controller, whose confirm button reads "Archive".
        fireEvent.click(screen.getByRole('button', {name: 'Archive space'}));
        fireEvent.click(await screen.findByRole('button', {name: 'Archive'}));

        expect(mockDeleteSpace).toHaveBeenCalledWith('space-1');
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        await waitFor(() => expect(mockGoHome).toHaveBeenCalled());
    });
});
