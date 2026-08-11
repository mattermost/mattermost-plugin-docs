// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import type {MemberProfile} from 'hooks/members';
import React from 'react';

import AddMembersField from './add_members_field';

import {renderWithContext} from '../../../tests/react_testing_utils';

const ada = {id: 'u1', displayName: 'Ada', username: 'ada', avatarUrl: ''};
const grace = {id: 'u2', displayName: 'Grace', username: 'grace', avatarUrl: ''};

// The picker's server search is out of scope here; stub it so the test drives
// selection directly and asserts this component's own behaviour.
const mockOnChange = {current: (() => {}) as (users: MemberProfile[]) => void};

jest.mock('./people_picker', () => ({
    __esModule: true,
    default: ({selected, onChange, disabled}: {selected: MemberProfile[]; onChange: (u: MemberProfile[]) => void; disabled?: boolean}) => {
        mockOnChange.current = disabled ? () => {} : onChange;
        return (
            <button
                type='button'
                data-testid='picker'
                disabled={disabled}
            >
                {selected.map((user) => user.displayName).join(',')}
            </button>
        );
    },
}));

const renderField = (onAdd: jest.Mock, disabled = false) => renderWithContext(
    <AddMembersField
        excludeIds={[]}
        onAdd={onAdd}
        disabled={disabled}
    />,
);

const pick = async (users: MemberProfile[]) => {
    await waitFor(() => expect(screen.getByTestId('picker')).toBeInTheDocument());
    mockOnChange.current(users);
};

describe('AddMembersField', () => {
    it('disables Add until something is picked', async () => {
        renderField(jest.fn());

        expect(screen.getByRole('button', {name: 'Add'})).toBeDisabled();

        await pick([ada]);
        await waitFor(() => expect(screen.getByRole('button', {name: 'Add'})).toBeEnabled());
    });

    it('disables the picker and Add while a mutation is in flight', () => {
        renderField(jest.fn(), true);

        expect(screen.getByTestId('picker')).toBeDisabled();
        expect(screen.getByRole('button', {name: 'Add'})).toBeDisabled();
    });

    it('hands the picked users to onAdd and clears them all on success', async () => {
        const onAdd = jest.fn().mockResolvedValue([]);
        renderField(onAdd);
        await pick([ada, grace]);

        fireEvent.click(await screen.findByRole('button', {name: 'Add'}));

        expect(onAdd).toHaveBeenCalledWith([ada, grace]);
        await waitFor(() => expect(screen.getByTestId('picker')).toHaveTextContent(''));
    });

    // Failed chips stay so the user can see which ones did not land, next to the
    // toast that says why.
    it('keeps only the failed users as chips', async () => {
        const onAdd = jest.fn().mockResolvedValue([grace]);
        renderField(onAdd);
        await pick([ada, grace]);

        fireEvent.click(await screen.findByRole('button', {name: 'Add'}));

        // A single waitFor: 'Grace' is a substring of the pre-update 'Ada,Grace',
        // so asserting it alone could pass before the update lands.
        await waitFor(() => {
            expect(screen.getByTestId('picker')).toHaveTextContent('Grace');
            expect(screen.getByTestId('picker')).not.toHaveTextContent('Ada');
        });
    });

    it('blocks picker changes while adding', async () => {
        let resolve: (failed: MemberProfile[]) => void = () => {};
        const onAdd = jest.fn(() => new Promise<MemberProfile[]>((done) => {
            resolve = done;
        }));
        renderField(onAdd);
        await pick([ada]);

        fireEvent.click(await screen.findByRole('button', {name: 'Add'}));
        await waitFor(() => expect(screen.getByTestId('picker')).toBeDisabled());
        await pick([ada, grace]);
        expect(screen.getByTestId('picker')).toHaveTextContent('Ada');
        expect(screen.getByTestId('picker')).not.toHaveTextContent('Grace');

        resolve([]);
        await waitFor(() => expect(screen.getByTestId('picker')).toHaveTextContent(''));
    });
});
