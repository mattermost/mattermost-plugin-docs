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
    await screen.findByRole('menu', {name: /Ada/});
}

describe('MemberRowMenu', () => {
    it.each([
        ['admin', 'Admin'],
        ['member', 'Member'],
        ['guest', 'Guest'],
    ] as const)('renders the resolved %s role in the neutral actions trigger', (role, visibleLabel) => {
        renderMenu({role});

        const trigger = screen.getByRole('button', {name: 'More actions for Ada'});
        expect(trigger).toHaveTextContent(visibleLabel);
    });

    it('uses an icon-only action menu when the caller has no role data', async () => {
        renderMenu();

        const trigger = screen.getByRole('button', {name: 'More actions for Ada'});
        expect(trigger).toHaveTextContent('');

        fireEvent.click(trigger);
        await screen.findByRole('menuitem', {name: 'Remove from space'});
        expect(screen.queryByRole('menuitem', {name: 'Admin'})).not.toBeInTheDocument();
        expect(screen.queryByRole('menuitem', {name: 'Can edit'})).not.toBeInTheDocument();
    });

    it('offers Remove and edits granular grants without closing the menu', async () => {
        const onRemove = jest.fn();
        const onChange = jest.fn();
        renderMenu({
            role: 'member',
            onRemove,
            permissionMenu: {
                options: ['create_page', 'edit_page'],
                selected: ['edit_page'],
                effective: ['read_page', 'edit_page'],
                disabled: false,
                onChange,
            },
        });

        await openMenu();

        expect(screen.getByRole('menuitemcheckbox', {name: 'Create pages'})).toHaveAttribute('aria-checked', 'false');
        expect(screen.getByRole('menuitemcheckbox', {name: 'Edit pages'})).toHaveAttribute('aria-checked', 'true');

        fireEvent.click(screen.getByRole('menuitemcheckbox', {name: 'Create pages'}));
        expect(onChange).toHaveBeenCalledWith(['create_page', 'edit_page']);
        expect(screen.getByRole('menu', {name: /Ada/})).toBeInTheDocument();

        fireEvent.click(screen.getByRole('menuitem', {name: 'Remove from space'}));
        expect(onRemove).toHaveBeenCalled();
    });

    it('locks the capability matrix for a guest', async () => {
        const onChange = jest.fn();
        renderMenu({
            role: 'guest',
            permissionMenu: {
                options: ['comment_page'],
                selected: [],
                effective: ['read_page'],
                disabled: true,
                onChange,
            },
        });

        await openMenu();

        const comment = screen.getByRole('menuitemcheckbox', {name: 'Comment on pages'});
        expect(comment).toHaveAttribute('aria-disabled', 'true');
        fireEvent.click(comment);
        expect(onChange).not.toHaveBeenCalled();
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

        fireEvent.click(screen.getByRole('menuitem', {name: 'Remove from space'}));
        expect(onRemove).not.toHaveBeenCalled();
    });
});
