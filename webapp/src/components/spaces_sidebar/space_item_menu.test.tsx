// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {copyToClipboard} from 'utils/clipboard';

import {makeSpace, makeTeam} from 'store/test_fixtures';

import SpaceItemMenu from './space_item_menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('utils/clipboard', () => ({
    copyToClipboard: jest.fn(),
}));

// The settings modal reads its state over HTTP on mount; this menu only owns the
// decision to open it, so the reads are stubbed rather than exercised here.
jest.mock('client/space_permissions', () => {
    const actual = jest.requireActual('client/space_permissions');
    return {
        ...actual,
        getSpaceAccess: jest.fn(),
        getSpaceMembers: jest.fn(),
        getMemberProfiles: jest.fn(),
    };
});

const api = jest.requireMock('client/space_permissions');

const space = makeSpace('docs', 'Docs');

function renderMenu(props: Partial<React.ComponentProps<typeof SpaceItemMenu>> = {}) {
    return renderWithContext(
        <SpaceItemMenu
            space={space}
            {...props}
        />,
        {state: {currentTeam: makeTeam('team1', 'myteam')}},
    );
}

async function openMenu() {
    fireEvent.click(screen.getByRole('button', {name: 'Space options for Docs'}));
    await screen.findByText('Copy link');
}

describe('SpaceItemMenu', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        api.getSpaceAccess.mockResolvedValue({
            id: space.id,
            default_capabilities: [],
            capabilities: ['read_page'],
        });
        api.getSpaceMembers.mockResolvedValue({items: [], page: 0, per_page: 100, has_more: false});
        api.getMemberProfiles.mockResolvedValue([]);
    });

    it('copies the team-scoped space link', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Copy link'));

        await waitFor(() => expect(copyToClipboard).toHaveBeenCalledWith('http://localhost:8065/myteam/spaces/docs'));
    });

    it('opens the permissions modal from the menu, scoped to this space', async () => {
        renderMenu();

        await openMenu();
        expect(screen.queryByRole('heading', {name: 'Permissions for Docs'})).not.toBeInTheDocument();

        fireEvent.click(screen.getByText('Space permissions'));

        expect(await screen.findByRole('heading', {name: 'Permissions for Docs'})).toBeInTheDocument();
        await waitFor(() => expect(api.getSpaceAccess).toHaveBeenCalledWith(space.id));
    });

    it('opens the leave confirmation from the menu', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Leave space'));

        expect(await screen.findByRole('heading', {name: 'Leave Docs'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Yes, leave space'})).toBeInTheDocument();
    });
});
