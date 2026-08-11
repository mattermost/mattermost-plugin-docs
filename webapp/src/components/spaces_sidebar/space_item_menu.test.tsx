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

jest.mock('client/rest', () => ({
    ...jest.requireActual('client/rest'),
    siteRoot: () => 'http://localhost:8065/mattermost',
}));

// Stubbed at the hook boundary: mattermost-redux's preferences actions are
// published as ESM that jest doesn't transform.
jest.mock('hooks/favorites', () => ({
    useSpaceFavoriteState: () => 'off',
    useToggleFavorite: () => jest.fn(),
}));

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
    it('copies the team-scoped space link', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Copy link'));

        await waitFor(() => expect(copyToClipboard).toHaveBeenCalledWith('http://localhost:8065/mattermost/myteam/spaces/docs'));
    });

    it('opens the leave confirmation from the menu', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Leave space'));

        expect(await screen.findByRole('heading', {name: 'Leave Docs'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Yes, leave space'})).toBeInTheDocument();
    });
});
