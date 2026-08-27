// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import MemberAvatarsFallback from './member_avatars_fallback';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => mockMembers,
}));

type Member = {id: string; displayName: string; username: string; avatarUrl: string};

const member = (id: string): Member => ({
    id,
    displayName: id,
    username: id,
    avatarUrl: `/api/v4/users/${id}/image`,
});

let mockMembers: Member[] = [];

const hostAvatar = ({username}: {username?: string}) => (
    <span
        data-testid='host-avatar'
        data-username={username}
    />
);

beforeEach(() => {
    mockMembers = [];
    (window as unknown as {Components?: Record<string, unknown>}).Components = {Avatar: hostAvatar};
});

describe('MemberAvatarsFallback', () => {
    it('renders nothing when the space has no members', () => {
        renderWithContext(<MemberAvatarsFallback spaceId='eng'/>);

        expect(screen.queryByTestId('host-avatar')).not.toBeInTheDocument();
    });

    it('renders an avatar per member with no overflow chip while they fit', () => {
        mockMembers = ['alice', 'bob', 'carol'].map(member);

        renderWithContext(<MemberAvatarsFallback spaceId='eng'/>);

        expect(screen.getAllByTestId('host-avatar')).toHaveLength(3);
        expect(screen.queryByText('+1')).not.toBeInTheDocument();
    });

    it('caps the stack and counts the remainder in an overflow chip', () => {
        mockMembers = ['alice', 'bob', 'carol', 'dave', 'erin'].map(member);

        renderWithContext(<MemberAvatarsFallback spaceId='eng'/>);

        expect(screen.getAllByTestId('host-avatar')).toHaveLength(3);
        expect(screen.getByText('+2')).toBeInTheDocument();
    });
});
