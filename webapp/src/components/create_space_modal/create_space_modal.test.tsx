// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import CreateSpaceModal from './create_space_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const takenSpaceState = {
    docs: {spaces: {byId: {taken: makeSpace('taken', 'Taken')}, order: ['taken']}, pages: {byId: {}, bySpace: {}}},
    currentTeam: {id: 'team1', name: 'myteam'},
};

function typeName(value: string) {
    fireEvent.change(screen.getByLabelText('Space name'), {target: {value}});
}

describe('CreateSpaceModal', () => {
    it('renders the form fields and disables Create until a name is entered', () => {
        renderWithContext(<CreateSpaceModal onClose={jest.fn()}/>, {state: {currentTeam: {id: 'team1', name: 'myteam'}}});

        expect(screen.getByLabelText('Space name')).toBeInTheDocument();
        expect(screen.getByRole('radiogroup', {name: 'Space visibility'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Create'})).toBeDisabled();

        typeName('My Space');
        expect(screen.getByRole('button', {name: 'Create'})).toBeEnabled();
    });

    it('focuses the URL field and shows the error when the slug is already taken', async () => {
        renderWithContext(<CreateSpaceModal onClose={jest.fn()}/>, {state: takenSpaceState});

        // Typing the name auto-derives the slug ("Taken" -> "taken"), which
        // collides with the existing space.
        typeName('Taken');
        fireEvent.click(screen.getByRole('button', {name: 'Create'}));

        const urlInput = await screen.findByLabelText('Space URL');

        await waitFor(() => expect(urlInput).toHaveFocus());
        expect(screen.getByText('That URL is already taken')).toBeInTheDocument();
    });

    it('creates the space and closes on a valid submit', async () => {
        const onClose = jest.fn();
        const onCreated = jest.fn();

        renderWithContext(
            <CreateSpaceModal
                onClose={onClose}
                onCreated={onCreated}
            />,
            {state: {currentTeam: {id: 'team1', name: 'myteam'}}},
        );

        typeName('Fresh Space');
        fireEvent.click(screen.getByRole('button', {name: 'Create'}));

        await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
        expect(onCreated.mock.calls[0][0]).toMatchObject({title: 'Fresh Space'});
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('invokes onClose from the Cancel button', () => {
        const onClose = jest.fn();
        renderWithContext(<CreateSpaceModal onClose={onClose}/>, {state: {currentTeam: {id: 'team1', name: 'myteam'}}});

        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));
        expect(onClose).toHaveBeenCalledTimes(1);
    });
});
