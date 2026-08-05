// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import SpaceHeader from './space_header';

import {renderWithContext} from '../../../tests/react_testing_utils';

// Stubbed at the hook boundary: mattermost-redux's preferences actions are published
// as ESM that jest doesn't transform.
jest.mock('hooks/favorites', () => ({
    useSpaceFavoriteState: () => 'off',
    useToggleFavorite: () => jest.fn(),
}));

jest.mock('hooks/leave_space', () => ({useLeaveSpace: () => jest.fn()}));

// Both reach mattermost-redux's user actions, which ship as untransformed ESM. The
// header only needs them as menu destinations, which the menu isn't opened to test.
jest.mock('components/share_space_modal/share_space_modal', () => () => null);
jest.mock('components/space_settings_modal/space_settings_modal', () => () => null);
jest.mock('store/actions', () => ({deleteSpace: () => async () => {}}));

const SPACE = makeSpace('eng', 'Engineering');
const PAGE_URL = '/myteam/spaces/eng/runbook';

const renderHeader = (route = PAGE_URL) =>
    renderWithContext(
        <SpaceHeader
            space={SPACE}
            memberCount={3}
            infoOpen={false}
            onToggleInfo={jest.fn()}
            onShowMembers={jest.fn()}
        />,
        {route},
    );

// In fullscreen the spaces sidebar is gone, so the header carries the only way back
// to the list.
describe('SpaceHeader all-spaces control', () => {
    it('offers no back control while the sidebar is showing', () => {
        renderHeader();

        expect(screen.queryByRole('button', {name: 'All spaces'})).not.toBeInTheDocument();
    });

    it('offers it in fullscreen', () => {
        renderHeader(`${PAGE_URL}?fs=1`);

        expect(screen.getByRole('button', {name: 'All spaces'})).toBeInTheDocument();
    });

    // Fullscreen is a query on the page URL, so leaving for the product home drops
    // it — the list of spaces is not something fullscreen can show.
    it('leaves fullscreen behind on the way', () => {
        const {history} = renderHeader(`${PAGE_URL}?fs=1`);

        fireEvent.click(screen.getByRole('button', {name: 'All spaces'}));

        expect(history.location.pathname).toBe('/myteam/spaces');
        expect(history.location.search).toBe('');
    });
});
