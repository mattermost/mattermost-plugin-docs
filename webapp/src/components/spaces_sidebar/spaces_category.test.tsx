// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import SpacesCategory from './spaces_category';

describe('SpacesCategory', () => {
    it('renders the title and children when expanded', () => {
        render(
            <SpacesCategory title='Spaces'>
                <div>{'child'}</div>
            </SpacesCategory>,
        );

        expect(screen.getByText('Spaces')).toBeInTheDocument();
        expect(screen.getByText('child')).toBeInTheDocument();
    });

    it('hides children when collapsed', () => {
        render(
            <SpacesCategory
                title='Spaces'
                collapsed={true}
            >
                <div>{'child'}</div>
            </SpacesCategory>,
        );

        expect(screen.queryByText('child')).not.toBeInTheDocument();
    });

    it('toggles via the header when collapsible', () => {
        const onToggle = jest.fn();
        render(
            <SpacesCategory
                title='Spaces'
                onToggle={onToggle}
            >
                <div>{'child'}</div>
            </SpacesCategory>,
        );

        fireEvent.click(screen.getByRole('button', {name: /Spaces/}));
        expect(onToggle).toHaveBeenCalledTimes(1);
    });

    it('does not toggle when not collapsible', () => {
        const onToggle = jest.fn();
        render(
            <SpacesCategory
                title='Favorites'
                collapsible={false}
                onToggle={onToggle}
            >
                <div>{'child'}</div>
            </SpacesCategory>,
        );

        fireEvent.click(screen.getByRole('button', {name: /Favorites/}));
        expect(onToggle).not.toHaveBeenCalled();
    });
});
