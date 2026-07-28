// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import SpacesSidebarNav from './spaces_sidebar_nav';

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('SpacesSidebarNav', () => {
    it('renders the Home entry and reports selection', () => {
        const onSelect = jest.fn();
        renderWithContext(
            <SpacesSidebarNav
                active={null}
                onSelect={onSelect}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Home'}));
        expect(onSelect).toHaveBeenCalledWith('home');
    });

    it('marks Home active', () => {
        const {container} = renderWithContext(
            <SpacesSidebarNav
                active='home'
                onSelect={jest.fn()}
            />,
        );

        expect(container.querySelector('.active')).toBeInTheDocument();
    });
});
