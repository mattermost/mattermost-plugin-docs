// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen} from '@testing-library/react';
import React from 'react';
import {copyToClipboard} from 'utils/clipboard';

import {makeSpace} from 'store/test_fixtures';

import SpaceInfoMenu from './space_info_menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({paths: {space: (id: string) => `/team/spaces/${id}`}}),
}));

jest.mock('hooks/permissions', () => ({
    useCanManageSpaceMembers: () => true,
}));

jest.mock('utils/clipboard', () => ({copyToClipboard: jest.fn(() => Promise.resolve(true))}));

const space = makeSpace('space-1', 'Engineering');

const renderMenu = () => renderWithContext(
    <SpaceInfoMenu
        space={space}
        memberCount={3}
        onShowMembers={jest.fn()}
    />,
);

describe('SpaceInfoMenu', () => {
    beforeEach(() => jest.clearAllMocks());

    it('copies the space link', async () => {
        renderMenu();

        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Copy link'}));
        });

        expect(copyToClipboard).toHaveBeenCalledWith('/team/spaces/space-1');
    });

    // MM-70344: the click gave no sign that anything happened.
    it('confirms the copy on the item itself', async () => {
        renderMenu();

        fireEvent.click(screen.getByRole('button', {name: 'Copy link'}));

        expect(await screen.findByRole('button', {name: 'Copied'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Copy link'})).not.toBeInTheDocument();
    });
});
