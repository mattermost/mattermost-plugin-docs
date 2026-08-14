// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import {docsDataSource} from 'data';
import React from 'react';

import {makeSpace, makeTeam} from 'store/test_fixtures';

import {toast} from 'components/toast';

import CreateSpaceModal from './create_space_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const team = makeTeam('team1', 'myteam');
let createSpaceSpy: jest.SpyInstance;
let toastErrorSpy: jest.SpyInstance;

function typeName(value: string) {
    fireEvent.change(screen.getByLabelText('Space name'), {target: {value}});
}

describe('CreateSpaceModal', () => {
    // Stub the API create so the form path doesn't hit the network; the server
    // assigns the opaque id.
    beforeEach(() => {
        jest.clearAllMocks();
        createSpaceSpy = jest.spyOn(docsDataSource, 'createSpace').mockImplementation(
            async (_teamId, input) => makeSpace('new-space-id', input.title.trim(), 'team1'),
        );
        toastErrorSpy = jest.spyOn(toast, 'error').mockReturnValue('toast-id');
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    it('renders the form fields and disables Create until a name is entered', () => {
        renderWithContext(<CreateSpaceModal onClose={jest.fn()}/>, {state: {currentTeam: team}});

        expect(screen.getByLabelText('Space name')).toBeInTheDocument();
        expect(screen.getByRole('radiogroup', {name: 'Space visibility'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Create'})).toBeDisabled();

        typeName('My Space');
        expect(screen.getByRole('button', {name: 'Create'})).toBeEnabled();
    });

    it('creates the space and closes on a valid submit', async () => {
        const onClose = jest.fn();
        const onCreated = jest.fn();

        renderWithContext(
            <CreateSpaceModal
                onClose={onClose}
                onCreated={onCreated}
            />,
            {state: {currentTeam: team}},
        );

        typeName('Fresh Space');
        fireEvent.click(screen.getByRole('button', {name: 'Create'}));

        await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
        expect(onCreated.mock.calls[0][0]).toMatchObject({title: 'Fresh Space'});
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('reports a failed create and keeps the modal open', async () => {
        const onClose = jest.fn();
        createSpaceSpy.mockRejectedValueOnce(new Error('boom'));
        jest.spyOn(console, 'error').mockImplementation(() => {});
        renderWithContext(<CreateSpaceModal onClose={onClose}/>, {state: {currentTeam: team}});

        typeName('Fresh Space');
        fireEvent.click(screen.getByRole('button', {name: 'Create'}));

        await waitFor(() => expect(toastErrorSpy).toHaveBeenCalledWith('Could not create the space. Please try again.'));
        expect(onClose).not.toHaveBeenCalled();
    });

    it('invokes onClose from the Cancel button', () => {
        const onClose = jest.fn();
        renderWithContext(<CreateSpaceModal onClose={onClose}/>, {state: {currentTeam: team}});

        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));
        expect(onClose).toHaveBeenCalledTimes(1);
    });
});
