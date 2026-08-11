// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
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

    // Both handlers run after the modal has animated out, not on the click: the
    // button dismisses the modal through its own close so the exit transition can
    // play, and the handler — which typically unmounts the modal — runs once it has.
    it('fires onConfirm from the confirm button, after the modal closes', async () => {
        const onConfirm = jest.fn();
        renderWithContext(
            <ConfirmModal
                {...baseProps}
                onConfirm={onConfirm}
            >
                <p>{'Body'}</p>
            </ConfirmModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Confirm'}));

        await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    });

    it('fires onCancel from the cancel button, after the modal closes', async () => {
        const onCancel = jest.fn();
        renderWithContext(
            <ConfirmModal
                {...baseProps}
                onCancel={onCancel}
            >
                <p>{'Body'}</p>
            </ConfirmModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));

        await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1));
    });

    it('stays open when confirmation must succeed before closing', async () => {
        const onConfirm = jest.fn().mockRejectedValue(new Error('nope'));
        const onCancel = jest.fn();
        renderWithContext(
            <ConfirmModal
                {...baseProps}
                closeAfterConfirm={true}
                onConfirm={onConfirm}
                onCancel={onCancel}
            >
                <p>{'Body'}</p>
            </ConfirmModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Confirm'}));

        await waitFor(() => expect(screen.getByRole('button', {name: 'Confirm'})).toBeEnabled());
        expect(onCancel).not.toHaveBeenCalled();
        expect(screen.getByRole('heading', {name: 'Leave space?'})).toBeInTheDocument();
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
