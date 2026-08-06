// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import type {MemberProfile} from 'hooks/members';
import React from 'react';

import MemberRowMenu from './member_row_menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

const member: MemberProfile = {id: 'u1', displayName: 'Ada', username: 'ada', avatarUrl: ''};

const renderMenu = (props: Partial<React.ComponentProps<typeof MemberRowMenu>> = {}) => renderWithContext(
    <MemberRowMenu
        member={member}
        isCurrentUser={false}
        disabled={false}
        onRemove={jest.fn()}
        onLeave={jest.fn()}
        {...props}
    />,
);

async function openMenu() {
    fireEvent.click(screen.getByRole('button', {name: /Ada/}));
    await screen.findByText('Can edit');
}

describe('MemberRowMenu', () => {
    it('offers Remove for another member, and the role items are disabled', async () => {
        const onRemove = jest.fn();
        renderMenu({onRemove});

        await openMenu();

        // Roles are PR #10 scaffolding: shown so the menu keeps its shape, but inert.
        expect(screen.getByRole('menuitem', {name: 'Admin'})).toHaveAttribute('aria-disabled', 'true');
        expect(screen.getByRole('menuitem', {name: 'Can edit'})).toHaveAttribute('aria-disabled', 'true');

        fireEvent.click(screen.getByRole('menuitem', {name: 'Remove from space'}));
        expect(onRemove).toHaveBeenCalled();
    });

    it('offers Leave space on your own row instead of Remove', async () => {
        const onLeave = jest.fn();
        renderMenu({isCurrentUser: true, onLeave});

        await openMenu();

        expect(screen.queryByRole('menuitem', {name: 'Remove from space'})).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Leave space'}));
        expect(onLeave).toHaveBeenCalled();
    });

    // The trigger still opens while busy, so the roles stay readable and the
    // unavailable action is visibly the thing that is unavailable.
    it('disables only the action while a mutation is in flight', async () => {
        const onRemove = jest.fn();
        renderMenu({disabled: true, onRemove});

        await openMenu();

        expect(screen.getByRole('menuitem', {name: 'Remove from space'})).toHaveAttribute('aria-disabled', 'true');
    });
});
