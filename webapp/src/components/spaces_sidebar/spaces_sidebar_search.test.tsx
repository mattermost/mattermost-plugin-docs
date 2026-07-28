// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import SpacesSidebarSearch from './spaces_sidebar_search';

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('SpacesSidebarSearch', () => {
    it('opens the switcher on click and advertises the keyboard shortcut', () => {
        const onOpen = jest.fn();
        renderWithContext(<SpacesSidebarSearch onOpen={onOpen}/>);

        const button = screen.getByRole('button', {name: /Find docs/});
        expect(button).toHaveAttribute('aria-haspopup', 'dialog');
        expect(button.getAttribute('aria-keyshortcuts')).toMatch(/^(Meta|Control)\+K$/);

        fireEvent.click(button);
        expect(onOpen).toHaveBeenCalledTimes(1);
    });
});
