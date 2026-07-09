// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import ConfirmModal from './confirm_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const baseProps = {
    title: 'Leave space?',
    onConfirm: jest.fn(),
    onCancel: jest.fn(),
};

describe('ConfirmModal', () => {
    it('renders the title, body, and default button labels', () => {
        renderWithContext(
            <ConfirmModal {...baseProps}>
                <p>{'Are you sure?'}</p>
            </ConfirmModal>,
        );

        expect(screen.getByRole('heading', {name: 'Leave space?'})).toBeInTheDocument();
        expect(screen.getByText('Are you sure?')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Confirm'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Cancel'})).toBeInTheDocument();
    });

    it('fires onConfirm and onCancel from the respective buttons', () => {
        const onConfirm = jest.fn();
        const onCancel = jest.fn();
        renderWithContext(
            <ConfirmModal
                {...baseProps}
                onConfirm={onConfirm}
                onCancel={onCancel}
            >
                <p>{'Body'}</p>
            </ConfirmModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Confirm'}));
        expect(onConfirm).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));
        expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it('renders custom button labels', () => {
        renderWithContext(
            <ConfirmModal
                {...baseProps}
                confirmButtonText='Leave'
                cancelButtonText='Stay'
                isConfirmDestructive={true}
            >
                <p>{'Body'}</p>
            </ConfirmModal>,
        );

        expect(screen.getByRole('button', {name: 'Leave'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Stay'})).toBeInTheDocument();
    });
});
