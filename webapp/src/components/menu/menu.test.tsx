// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import Menu from './menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

function renderMenu(onRename: () => void, onPermissionChange = jest.fn()) {
    return renderWithContext(
        <Menu
            ariaLabel='Space actions'
            trigger={<button type='button'>{'Open menu'}</button>}
        >
            <Menu.Item onClick={onRename}>{'Rename'}</Menu.Item>
            <Menu.CheckboxItem
                checked={true}
                onCheckedChange={onPermissionChange}
            >
                {'Edit pages'}
            </Menu.CheckboxItem>
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

    it('renders an accessible radio group and exposes the selected option', async () => {
        const onValueChange = jest.fn();
        renderWithContext(
            <Menu
                ariaLabel='Space visibility'
                trigger={<button type='button'>{'Open menu'}</button>}
            >
                <Menu.RadioGroup
                    value='open'
                    onValueChange={onValueChange}
                >
                    <Menu.RadioItem value='open'>{'Public'}</Menu.RadioItem>
                    <Menu.RadioItem value='private'>{'Private'}</Menu.RadioItem>
                </Menu.RadioGroup>
            </Menu>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Open menu'}));

        const publicOption = await screen.findByRole('menuitemradio', {name: 'Public'});
        expect(publicOption).toHaveAttribute('aria-checked', 'true');
        expect(screen.getByRole('menuitemradio', {name: 'Private'})).toHaveAttribute('aria-checked', 'false');

        fireEvent.click(screen.getByRole('menuitemradio', {name: 'Private'}));
        expect(onValueChange).toHaveBeenCalledWith('private');
    });

    it('renders an accessible checkbox item and keeps the menu open when toggled', async () => {
        const onPermissionChange = jest.fn();
        renderMenu(jest.fn(), onPermissionChange);

        fireEvent.click(screen.getByRole('button', {name: 'Open menu'}));
        const editPages = await screen.findByRole('menuitemcheckbox', {name: 'Edit pages'});
        expect(editPages).toHaveAttribute('aria-checked', 'true');

        fireEvent.click(editPages);

        expect(onPermissionChange).toHaveBeenCalledWith(false);
        expect(screen.getByRole('menu', {name: 'Open menu'})).toBeInTheDocument();
    });
});
