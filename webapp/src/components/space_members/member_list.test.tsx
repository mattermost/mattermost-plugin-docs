// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import type {MemberProfile} from 'hooks/members';
import React from 'react';

import MemberList from './member_list';

import {renderWithContext} from '../../../tests/react_testing_utils';

const profile = (id: string, displayName: string): MemberProfile => ({
    id,
    displayName,
    username: displayName.toLowerCase(),
    avatarUrl: '',
});

const members = [profile('u1', 'Ada'), profile('u2', 'Grace')];

describe('MemberList', () => {
    it('renders a row per member with name and handle', () => {
        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
            />,
        );

        expect(screen.getByText('Ada')).toBeInTheDocument();
        expect(screen.getByText('@ada')).toBeInTheDocument();
        expect(screen.getByText('Grace')).toBeInTheDocument();
        expect(screen.getByText('@grace')).toBeInTheDocument();
    });

    it('marks the current user only when asked to', () => {
        const state = {currentUser: {id: 'u1', username: 'ada'}};

        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
                showYouBadge={true}
            />,
            {state},
        );
        expect(screen.getByText('(You)')).toBeInTheDocument();

        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
            />,
            {state},
        );
        expect(screen.getAllByText('(You)')).toHaveLength(1);
    });

    it('renders nothing for an empty roster', () => {
        renderWithContext(
            <MemberList
                members={[]}
                avatarSize='sm'
            />,
        );

        expect(screen.queryByText('@ada')).not.toBeInTheDocument();
    });

    it('gives every row a menu when actions are supplied', () => {
        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
                actions={{onRemove: jest.fn(), onLeave: jest.fn(), disabled: false}}
            />,
        );

        expect(screen.getAllByRole('button', {name: /Ada|Grace/})).toHaveLength(2);
    });

    // Read-only is the absence of actions, not a flag — so there is no way to render
    // a menu with nothing behind it.
    it('renders no menu at all without actions', () => {
        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
            />,
        );

        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });
});
