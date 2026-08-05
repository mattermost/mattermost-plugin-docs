// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import SpacesSidebarNav from './spaces_sidebar_nav';

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('SpacesSidebarNav', () => {
    // A link, not a button: Home is an address, so it has to support opening in a
    // new tab and copying like the rest of the product.
    it('renders Home as a link to the product home', () => {
        renderWithContext(
            <SpacesSidebarNav
                active={null}
                homeHref='/myteam/spaces'
            />,
        );

        expect(screen.getByRole('link', {name: 'Home'})).toHaveAttribute('href', '/myteam/spaces');
    });

    it('marks Home active', () => {
        const {container} = renderWithContext(
            <SpacesSidebarNav
                active='home'
                homeHref='/myteam/spaces'
            />,
        );

        expect(container.querySelector('.active')).toBeInTheDocument();
    });
});
