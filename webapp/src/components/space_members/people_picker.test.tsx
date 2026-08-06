// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import type {MemberProfile} from 'hooks/members';
import {useUserSearch} from 'hooks/user_search';
import React from 'react';

import PeoplePicker from './people_picker';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('hooks/user_search', () => ({
    useUserSearch: jest.fn(),
}));

const mockUseUserSearch = useUserSearch as jest.MockedFunction<typeof useUserSearch>;

const alice: MemberProfile = {id: 'user-a', displayName: 'Alice Adams', username: 'alice', avatarUrl: '/a.png'};
const bob: MemberProfile = {id: 'user-b', displayName: 'Bob Barker', username: 'bob', avatarUrl: '/b.png'};

const baseProps = {
    selected: [],
    excludeIds: [],
    onChange: jest.fn(),
};

describe('PeoplePicker', () => {
    beforeEach(() => {
        mockUseUserSearch.mockReturnValue({results: [alice, bob], loading: false});
    });

    it('selects a person when the row is clicked', async () => {
        const onChange = jest.fn();
        renderWithContext(
            <PeoplePicker
                {...baseProps}
                onChange={onChange}
            />,
        );

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'a'}});

        const row = await screen.findByRole('option', {name: /Alice Adams/});
        fireEvent.click(row);

        expect(onChange.mock.calls[0][0]).toEqual([alice]);
    });

    it('renders a chip per selected person rather than a serialized profile', () => {
        renderWithContext(
            <PeoplePicker
                {...baseProps}
                selected={[alice]}
            />,
        );

        expect(screen.getByText('Alice Adams')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Remove Alice Adams'})).toBeInTheDocument();

        const input = screen.getByRole('combobox') as HTMLInputElement;
        expect(input.value).toBe('');
        expect(screen.queryByText(/"id"/)).not.toBeInTheDocument();
    });

    it('removes a person via the chip remove button', async () => {
        const onChange = jest.fn();
        renderWithContext(
            <PeoplePicker
                {...baseProps}
                selected={[alice, bob]}
                onChange={onChange}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Remove Alice Adams'}));

        await waitFor(() => expect(onChange.mock.calls[0][0]).toEqual([bob]));
    });
});
