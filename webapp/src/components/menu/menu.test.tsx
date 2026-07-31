// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import Menu from './menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

function renderMenu(onRename: () => void) {
    return renderWithContext(
        <Menu
            ariaLabel='Space actions'
            trigger={<button type='button'>{'Open menu'}</button>}
        >
            <Menu.Item onClick={onRename}>{'Rename'}</Menu.Item>
            <Menu.Separator/>
            <Menu.Item
                destructive={true}
                onClick={jest.fn()}
            >
                {'Delete'}
            </Menu.Item>
        </Menu>,
    );
}

describe('Menu', () => {
    it('renders the trigger and keeps items closed initially', () => {
        renderMenu(jest.fn());

        expect(screen.getByRole('button', {name: 'Open menu'})).toBeInTheDocument();
        expect(screen.queryByText('Rename')).not.toBeInTheDocument();
    });

    it('opens on trigger click and fires the item handler', async () => {
        const onClick = jest.fn();
        renderMenu(onClick);

        fireEvent.click(screen.getByRole('button', {name: 'Open menu'}));

        const rename = await screen.findByText('Rename');
        expect(screen.getByText('Delete')).toBeInTheDocument();

        fireEvent.click(rename);
        await waitFor(() => expect(onClick).toHaveBeenCalledTimes(1));
    });
});
