// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import Menu from './menu';
import type {MenuItemSpec} from './menu_types';

import {renderWithContext} from '../../../tests/react_testing_utils';

function renderMenu(items: MenuItemSpec[]) {
    return renderWithContext(
        <Menu
            ariaLabel='Space actions'
            items={items}
            trigger={<button type='button'>{'Open menu'}</button>}
        />,
    );
}

describe('Menu', () => {
    it('renders the trigger and keeps items closed initially', () => {
        renderMenu([{id: 'a', label: 'Rename', onClick: jest.fn()}]);

        expect(screen.getByRole('button', {name: 'Open menu'})).toBeInTheDocument();
        expect(screen.queryByText('Rename')).not.toBeInTheDocument();
    });

    it('opens on trigger click and fires the item handler', async () => {
        const onClick = jest.fn();
        renderMenu([
            {id: 'rename', label: 'Rename', onClick},
            {id: 'delete', label: 'Delete', onClick: jest.fn(), isDestructive: true, hasDivider: true},
        ]);

        fireEvent.click(screen.getByRole('button', {name: 'Open menu'}));

        const rename = await screen.findByText('Rename');
        expect(screen.getByText('Delete')).toBeInTheDocument();

        fireEvent.click(rename);
        await waitFor(() => expect(onClick).toHaveBeenCalledTimes(1));
    });
});
