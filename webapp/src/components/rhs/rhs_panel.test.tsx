// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import RhsPanel from './rhs_panel';

import {renderWithContext} from '../../../tests/react_testing_utils';

const renderPanel = (props: Partial<React.ComponentProps<typeof RhsPanel>> = {}) =>
    renderWithContext(
        <RhsPanel
            name='Space info'
            widthKey='test'
            onClose={jest.fn()}
            {...props}
        >
            <p>{'body'}</p>
        </RhsPanel>,
    );

describe('RhsPanel', () => {
    // The region's name is what a screen reader reads on the way in, and it stays
    // the panel's rather than the current screen's.
    it('names the region and heads it with the panel name', () => {
        renderPanel();

        expect(screen.getByRole('complementary', {name: 'Space info'})).toBeInTheDocument();
        expect(screen.getByRole('heading', {name: 'Space info'})).toBeInTheDocument();
        expect(screen.getByText('body')).toBeInTheDocument();
    });

    it('heads a drilled-in screen with its own title, keeping the region name', () => {
        renderPanel({title: 'Members', onBack: jest.fn()});

        expect(screen.getByRole('complementary', {name: 'Space info'})).toBeInTheDocument();
        expect(screen.getByRole('heading', {name: 'Members'})).toBeInTheDocument();
    });

    it('closes on request', () => {
        const onClose = jest.fn();
        renderPanel({onClose});

        fireEvent.click(screen.getByRole('button', {name: 'Close'}));

        expect(onClose).toHaveBeenCalledTimes(1);
    });

    // Back is what a drilled-in screen supplies; the root screen has nowhere to go.
    it('offers no back control at the root', () => {
        renderPanel();

        expect(screen.queryByRole('button', {name: 'Back to Space info'})).not.toBeInTheDocument();
    });

    it('goes back from a drilled-in screen', () => {
        const onBack = jest.fn();
        renderPanel({title: 'Members', onBack});

        fireEvent.click(screen.getByRole('button', {name: 'Back to Space info'}));

        expect(onBack).toHaveBeenCalledTimes(1);
    });
});
