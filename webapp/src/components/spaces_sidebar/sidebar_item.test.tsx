// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import SidebarItem from './sidebar_item';

import {renderWithContext} from '../../../tests/react_testing_utils';

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

// Rows that navigate are anchors, so they support new-tab, copy and middle-click;
// rows that act (a toggle, a menu) stay buttons.
describe('SidebarItem as a link', () => {
    it('renders an anchor when given a destination', () => {
        renderWithContext(
            <SidebarItem
                leading={null}
                label='Engineering'
                to='/myteam/spaces/eng'
            />,
        );

        expect(screen.getByRole('link', {name: 'Engineering'})).toHaveAttribute('href', '/myteam/spaces/eng');
        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('stays a button without one', () => {
        renderWithContext(
            <SidebarItem
                leading={null}
                label='Engineering'
                onClick={jest.fn()}
            />,
        );

        expect(screen.getByRole('button', {name: 'Engineering'})).toBeInTheDocument();
    });
});
