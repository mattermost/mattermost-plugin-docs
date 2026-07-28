// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {deleteSpace, updateSpace} from 'store/actions';
import {makeSpace} from 'store/test_fixtures';

import SpaceSettingsModal from './space_settings_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockGoHome = jest.fn();

jest.mock('store/actions', () => ({
    updateSpace: jest.fn(() => () => Promise.resolve()),
    deleteSpace: jest.fn(() => () => Promise.resolve()),
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

const space = makeSpace('space-1', 'Project Avalanche');

describe('SpaceSettingsModal', () => {
    it('renders the title, space subtitle, and section tabs', () => {
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={jest.fn()}
            />,
        );

        expect(screen.getByRole('heading', {name: 'Space Settings'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: /Info/})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: /Permissions/})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: /Configuration/})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: /Archive space/})).toBeInTheDocument();
    });

    it('keeps Save disabled until a field changes, then dispatches updateSpace', () => {
        const onClose = jest.fn();
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={onClose}
            />,
        );

        const save = screen.getByRole('button', {name: 'Save'});
        expect(save).toBeDisabled();

        fireEvent.change(screen.getByLabelText('Space name'), {target: {value: 'Renamed'}});
        expect(save).toBeEnabled();

        fireEvent.click(save);
        expect(mockUpdateSpace).toHaveBeenCalledWith('space-1', {title: 'Renamed', description: ''});
    });

    it('archives the space through a confirm dialog', async () => {
        const onClose = jest.fn();
        renderWithContext(
            <SpaceSettingsModal
                space={space}
                onClose={onClose}
                initialTab='archive'
            />,
        );

        // The left-nav tab and the in-card action both read "Archive space";
        // the card button is the later one in DOM order.
        const archiveButtons = screen.getAllByRole('button', {name: 'Archive space'});
        fireEvent.click(archiveButtons[archiveButtons.length - 1]);
        fireEvent.click(screen.getByRole('button', {name: 'Archive'}));

        expect(mockDeleteSpace).toHaveBeenCalledWith('space-1');
        await Promise.resolve();
        expect(mockGoHome).toHaveBeenCalled();
    });
});
