// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {copyToClipboard} from 'utils/clipboard';

import {Client4} from 'mattermost-redux/client';

import {makeSpace, makeTeam} from 'store/test_fixtures';

import SpaceItemMenu from './space_item_menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('utils/clipboard', () => ({
    copyToClipboard: jest.fn(),
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
        const previousUrl = Client4.getUrl();
        try {
            Client4.setUrl('http://localhost:8065/mattermost');
            renderMenu();
            await openMenu();
            fireEvent.click(screen.getByText('Copy link'));

            await waitFor(() => expect(copyToClipboard).toHaveBeenCalledWith('http://localhost:8065/mattermost/myteam/spaces/docs'));
        } finally {
            Client4.setUrl(previousUrl);
        }
    });

    // Importing into a Space that already exists decides which of its pages the bundle adopts and whose edits an
    // overwrite discards, so it is offered from the Space itself — and it navigates, because where you are in an
    // import belongs in the URL rather than in whichever component happened to open it.
    it('navigates to the import route for this Space', async () => {
        const {history} = renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Import from Confluence'));

        await waitFor(() => expect(history.location.pathname).toBe('/myteam/spaces/docs/_import'));
    });

    it('opens the leave confirmation from the menu', async () => {
        renderMenu();

        await openMenu();
        fireEvent.click(screen.getByText('Leave space'));

        expect(await screen.findByRole('heading', {name: 'Leave Docs'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Yes, leave space'})).toBeInTheDocument();
    });
});
