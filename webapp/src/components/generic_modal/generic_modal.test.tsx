// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import GenericModal from './generic_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const baseProps = {
    title: 'My modal',
    onClose: jest.fn(),
};

describe('GenericModal', () => {
    it('renders the title, body, and footer', () => {
        renderWithContext(
            <GenericModal
                {...baseProps}
                footer={<button type='button'>{'Save'}</button>}
            >
                <p>{'Body content'}</p>
            </GenericModal>,
        );

        expect(screen.getByRole('heading', {name: 'My modal'})).toBeInTheDocument();
        expect(screen.getByText('Body content')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Save'})).toBeInTheDocument();
    });

    it('closes via the close button', () => {
        const onClose = jest.fn();
        renderWithContext(
            <GenericModal
                {...baseProps}
                onClose={onClose}
            >
                <p>{'Body'}</p>
            </GenericModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Close'}));
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('omits the close button when disabled', () => {
        renderWithContext(
            <GenericModal
                {...baseProps}
                showCloseButton={false}
            >
                <p>{'Body'}</p>
            </GenericModal>,
        );

        expect(screen.queryByRole('button', {name: 'Close'})).not.toBeInTheDocument();
    });
});
