// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import BasicInputModal from './basic_input_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const renderModal = (props: Partial<React.ComponentProps<typeof BasicInputModal>> = {}) => renderWithContext(
    <BasicInputModal
        title='Rename page'
        label='Page name'
        onConfirm={jest.fn()}
        onClose={jest.fn()}
        {...props}
    />,
);

describe('BasicInputModal', () => {
    it('seeds the field with initialValue and disables Save while it is blank', () => {
        renderModal({initialValue: '   '});

        expect(screen.getByRole('button', {name: 'Save'})).toBeDisabled();

        fireEvent.change(screen.getByLabelText('Page name'), {target: {value: 'Notes'}});
        expect(screen.getByRole('button', {name: 'Save'})).toBeEnabled();
    });

    it('confirms with the trimmed value and closes', async () => {
        const onConfirm = jest.fn();
        const onClose = jest.fn();
        renderModal({initialValue: 'Old', onConfirm, onClose});

        fireEvent.change(screen.getByLabelText('Page name'), {target: {value: '  New  '}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('New'));
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('submits on Enter in the field', async () => {
        const onConfirm = jest.fn();
        renderModal({initialValue: 'Old', onConfirm});

        fireEvent.keyDown(screen.getByLabelText('Page name'), {key: 'Enter'});

        await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('Old'));
    });

    it('keeps the modal open and shows the failure inline when onConfirm rejects', async () => {
        const onClose = jest.fn();
        renderModal({
            initialValue: 'Old',
            onConfirm: jest.fn().mockRejectedValue(new Error('Someone else edited this page')),
            onClose,
        });

        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(screen.getByText('Someone else edited this page')).toBeInTheDocument());
        expect(onClose).not.toHaveBeenCalled();
    });
});
