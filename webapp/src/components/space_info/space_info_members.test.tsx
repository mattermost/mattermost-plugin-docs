// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import SpaceInfoMembers from './space_info_members';

import {renderWithContext} from '../../../tests/react_testing_utils';

// MemberList is re-exported from components/space_members alongside AddMembersField,
// which pulls in the people picker and mattermost-redux's user search actions
// (published ESM that jest doesn't transform). Stub at the hook boundary, as
// share_space_modal.test.tsx does.
jest.mock('hooks/user_search', () => ({
    useUserSearch: () => ({results: [], loading: false}),
}));

const members = [
    {id: 'u1', displayName: 'Ada', username: 'ada', avatarUrl: ''},
    {id: 'u2', displayName: 'Grace', username: 'grace', avatarUrl: ''},
];

describe('SpaceInfoMembers', () => {
    it('lists the members', () => {
        renderWithContext(<SpaceInfoMembers members={members}/>);

        expect(screen.getByText('Ada')).toBeInTheDocument();
        expect(screen.getByText('@grace')).toBeInTheDocument();
    });

    // Read-only: the panel supplies no actions, so no row can offer one.
    it('offers no membership actions', () => {
        renderWithContext(<SpaceInfoMembers members={members}/>);

        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });
});
