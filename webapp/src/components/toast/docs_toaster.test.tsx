// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen} from '@testing-library/react';
import React from 'react';

import DocsToaster from './docs_toaster';
import {toast} from './toast';

import {renderWithContext} from '../../../tests/react_testing_utils';

// Base UI mirrors each toast's text into a visually hidden live region and marks
// the visual toast aria-hidden while the window is unfocused (always, in jsdom),
// so queries here match all copies and cannot go through accessible roles.
const closeButton = () => screen.getByLabelText('Close');

describe('DocsToaster', () => {
    it('renders a toast raised through the imperative API', () => {
        renderWithContext(<DocsToaster/>);

        act(() => {
            toast.success('Space created');
        });

        expect(screen.getAllByText('Space created').length).toBeGreaterThan(0);
    });

    it('renders the description', () => {
        renderWithContext(<DocsToaster/>);

        act(() => {
            toast.error('Could not leave space', {description: 'Add another member first.'});
        });

        expect(screen.getAllByText('Could not leave space').length).toBeGreaterThan(0);
        expect(screen.getAllByText('Add another member first.').length).toBeGreaterThan(0);
    });

    it('dismisses via the close button', () => {
        renderWithContext(<DocsToaster/>);

        act(() => {
            toast.info('Heads up');
        });

        act(() => {
            fireEvent.click(closeButton());
        });

        expect(screen.queryByText('Heads up')).not.toBeInTheDocument();
    });

    it('dismisses by id', () => {
        renderWithContext(<DocsToaster/>);

        let id = '';
        act(() => {
            id = toast.warning('Almost out of space');
        });

        act(() => {
            toast.dismiss(id);
        });

        expect(screen.queryByText('Almost out of space')).not.toBeInTheDocument();
    });
});
