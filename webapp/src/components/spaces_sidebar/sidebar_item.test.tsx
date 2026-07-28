// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import SidebarItem from './sidebar_item';

describe('SidebarItem', () => {
    it('renders leading, label, and trailing content', () => {
        render(
            <SidebarItem
                leading={<span>{'📄'}</span>}
                label='My Space'
                trailing={<button type='button'>{'menu'}</button>}
            />,
        );

        expect(screen.getByText('📄')).toBeInTheDocument();
        expect(screen.getByText('My Space')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'menu'})).toBeInTheDocument();
    });

    it('fires onClick and applies the title', () => {
        const onClick = jest.fn();
        render(
            <SidebarItem
                leading={<span/>}
                label='Home'
                title='Home'
                onClick={onClick}
            />,
        );

        const button = screen.getByRole('button', {name: 'Home'});
        expect(button).toHaveAttribute('title', 'Home');
        fireEvent.click(button);
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('marks the active state on the container', () => {
        const {container} = render(
            <SidebarItem
                leading={<span/>}
                label='Active'
                active={true}
            />,
        );

        expect(container.firstChild).toHaveClass('active');
    });
});
