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

        const trigger = screen.getByRole('button', {name: `${visibleLabel} — more actions for Ada`});
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

    // A tier names a seeded space-default scheme. A member's grant selects no scheme, so the
    // row names the member's standing and edits permission ids — never a tier.
    it('offers no named tier on a member row', async () => {
        renderMenu({
            role: 'member',
            permissionMenu: {
                options: ['create_page', 'comment_page', 'edit_page', 'delete_own_page', 'delete_page', 'admin_space'],
                selected: [],
                disabled: false,
                onChange: jest.fn(),
            },
        });

        expect(screen.getByRole('button', {name: 'Member — permissions for Ada'})).toHaveTextContent('Member');

        await openMenu();

        expect(screen.queryAllByRole('menuitemradio')).toHaveLength(0);
        expect(screen.getAllByRole('menuitemcheckbox').map((item) => item.textContent)).toEqual([
            expect.stringContaining('Create pages'),
            expect.stringContaining('Comment on pages'),
            expect.stringContaining('Edit pages'),
            expect.stringContaining('Delete own pages'),
            expect.stringContaining('Delete any page'),
            expect.stringContaining('Administer space'),
        ]);
    });

    // The caller decides what may be granted and passes only that; a permission it omits is
    // absent from the menu rather than present and refused.
    it('renders only the permissions the caller offers, and no grant editor without a menu', async () => {
        const {unmount} = renderMenu({
            role: 'member',
            permissionMenu: {
                options: ['comment_page'],
                selected: [],
                disabled: false,
                onChange: jest.fn(),
            },
        });

        await openMenu();

        expect(screen.getByRole('menuitemcheckbox', {name: 'Comment on pages'})).toBeInTheDocument();
        expect(screen.queryByRole('menuitemcheckbox', {name: /Administer space/})).not.toBeInTheDocument();

        unmount();

        renderMenu({role: 'guest'});
        await openMenu();

        expect(screen.queryAllByRole('menuitemcheckbox')).toHaveLength(0);
        expect(screen.getByRole('menuitem', {name: 'Remove from space'})).toBeInTheDocument();
    });

    it('disables the grants and says why while a write is in flight', async () => {
        const onChange = jest.fn();
        renderMenu({
            role: 'member',
            permissionMenu: {
                options: ['comment_page'],
                selected: [],
                disabled: true,
                disabledReason: 'Saving…',
                onChange,
            },
        });

        await openMenu();

        const comment = screen.getByRole('menuitemcheckbox', {name: /Comment on pages/});
        expect(comment).toHaveAttribute('aria-disabled', 'true');
        expect(comment).toHaveTextContent('Saving…');

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
