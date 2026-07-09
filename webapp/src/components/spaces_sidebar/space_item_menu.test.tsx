// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {copyToClipboard} from 'utils/clipboard';

import {makeSpace} from 'store/test_fixtures';

import SpaceItemMenu from './space_item_menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('utils/clipboard', () => ({
    copyToClipboard: jest.fn(),
}));

const space = makeSpace('docs', 'Docs');

function renderMenu(props: Partial<React.ComponentProps<typeof SpaceItemMenu>> = {}) {
    return renderWithContext(
        <SpaceItemMenu
            space={space}
            favorite={false}
            onToggleFavorite={jest.fn()}
            {...props}
        />,
        {state: {currentTeam: {id: 'team1', name: 'myteam'}}},
    );
}

async function openMenu() {
    fireEvent.click(screen.getByRole('button', {name: 'Space options for Docs'}));
    await screen.findByText('Add to favorites');
}

describe('SpaceItemMenu', () => {
    it('toggles favorite from the menu', async () => {
        const onToggleFavorite = jest.fn();
        renderMenu({onToggleFavorite});

        await openMenu();
        fireEvent.click(screen.getByText('Add to favorites'));

        await waitFor(() => expect(onToggleFavorite).toHaveBeenCalledWith('docs'));
    });

    it('copies the team-scoped space link', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Copy link'));

        await waitFor(() => expect(copyToClipboard).toHaveBeenCalledWith('http://localhost:8065/myteam/spaces/docs'));
    });

    it('opens the leave confirmation from the menu', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Leave space'));

        expect(await screen.findByRole('heading', {name: 'Leave Docs'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Yes, leave space'})).toBeInTheDocument();
    });
});
