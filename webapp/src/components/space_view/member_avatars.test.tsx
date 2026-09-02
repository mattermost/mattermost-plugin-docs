// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import MemberAvatars from './member_avatars';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('hooks/members', () => ({
    useSpaceMemberIds: () => mockMemberIds,
    useSpaceMemberProfiles: () => mockMemberIds.map((id: string) => ({
        id,
        displayName: id,
        username: id,
        avatarUrl: `/api/v4/users/${id}/image`,
    })),
}));

jest.mock('./member_avatars_fallback', () => () => <div data-testid='docs-fallback-avatars'/>);

let mockMemberIds: string[] = [];

const hostAvatars = ({userIds, size, canOpenOverflow}: {userIds: string[]; size?: string; canOpenOverflow?: boolean}) => (
    <div
        data-testid='host-avatars'
        data-user-ids={userIds.join(',')}
        data-size={size}
        data-can-open-overflow={String(Boolean(canOpenOverflow))}
    />
);

const setHostComponents = (components: Record<string, unknown>) => {
    (window as unknown as {Components?: Record<string, unknown>}).Components = components;
};

describe('MemberAvatars on a host that publishes Avatars', () => {
    beforeEach(() => {
        mockMemberIds = [];
        setHostComponents({Avatars: hostAvatars});
    });

    it('renders nothing when the space has no members', () => {
        renderWithContext(<MemberAvatars spaceId='eng'/>);

        expect(screen.queryByTestId('host-avatars')).not.toBeInTheDocument();
        expect(screen.queryByTestId('docs-fallback-avatars')).not.toBeInTheDocument();
    });

    it('hands the space\'s member ids to the host stack', () => {
        mockMemberIds = ['alice', 'bob', 'carol'];

        renderWithContext(<MemberAvatars spaceId='eng'/>);

        expect(screen.getByTestId('host-avatars')).toHaveAttribute('data-user-ids', 'alice,bob,carol');
        expect(screen.getByTestId('host-avatars')).toHaveAttribute('data-size', 'sm');
    });

    // The "+N" chip lists the members the stack could not show.
    it('opts the overflow chip into opening a member list', () => {
        mockMemberIds = ['alice', 'bob', 'carol', 'dave', 'erin'];

        renderWithContext(<MemberAvatars spaceId='eng'/>);

        expect(screen.getByTestId('host-avatars')).toHaveAttribute('data-can-open-overflow', 'true');
    });

    it('does not fall back when the host stack is available', () => {
        mockMemberIds = ['alice'];

        renderWithContext(<MemberAvatars spaceId='eng'/>);

        expect(screen.queryByTestId('docs-fallback-avatars')).not.toBeInTheDocument();
    });
});

// Hosts predating MM-70358 publish Avatar but not Avatars.
describe('MemberAvatars on a host without Avatars', () => {
    beforeEach(() => {
        mockMemberIds = ['alice'];
        setHostComponents({});
    });

    it('falls back to the plugin\'s own stack', () => {
        renderWithContext(<MemberAvatars spaceId='eng'/>);

        expect(screen.getByTestId('docs-fallback-avatars')).toBeInTheDocument();
        expect(screen.queryByTestId('host-avatars')).not.toBeInTheDocument();
    });

    it('still falls back when the space has no members, leaving the empty case to the fallback', () => {
        mockMemberIds = [];

        renderWithContext(<MemberAvatars spaceId='eng'/>);

        expect(screen.getByTestId('docs-fallback-avatars')).toBeInTheDocument();
    });
});
