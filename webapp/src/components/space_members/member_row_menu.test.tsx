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

    // The tiers come first and speak the same vocabulary as the trigger; choosing one replaces
    // the grant with that tier's set, in the menu's own option order.
    it('offers the named tiers ahead of the granular grants and marks the effective one', async () => {
        const onChange = jest.fn();
        renderMenu({
            role: 'member',
            permissionMenu: {
                options: ['create_page', 'comment_page', 'edit_page', 'delete_own_page', 'delete_page', 'admin_space'],
                selected: [],
                effective: ['read_page', 'comment_page'],
                disabled: false,
                disabledOptions: ['admin_space'],
                onChange,
            },
        });

        expect(screen.getByRole('button', {name: 'Can comment — permissions for Ada'})).toHaveTextContent('Can comment');

        await openMenu();

        const items = screen.getAllByRole('menuitemradio');
        expect(items.map((item) => item.textContent)).toEqual([
            expect.stringContaining('Can view'),
            expect.stringContaining('Can comment'),
            expect.stringContaining('Can edit'),
            expect.stringContaining('Admin'),
        ]);

        // Admin requires the admin_space grant, which this caller may not grant.
        expect(screen.getByRole('menuitemradio', {name: /^Admin/})).toHaveAttribute('aria-disabled', 'true');

        fireEvent.click(screen.getByRole('menuitemradio', {name: /^Can edit/}));
        expect(onChange).toHaveBeenCalledWith(['create_page', 'comment_page', 'edit_page', 'delete_own_page']);
    });

    it('offers only the tiers whose permissions the menu may grant', async () => {
        renderMenu({
            role: 'member',
            permissionMenu: {
                options: ['create_page', 'edit_page'],
                selected: [],
                effective: ['read_page'],
                disabled: false,
                onChange: jest.fn(),
            },
        });

        await openMenu();

        expect(screen.getByRole('menuitemradio', {name: /^Can view/})).toBeInTheDocument();
        expect(screen.queryByRole('menuitemradio', {name: /^Can comment/})).not.toBeInTheDocument();
        expect(screen.queryByRole('menuitemradio', {name: /^Can edit/})).not.toBeInTheDocument();
        expect(screen.queryByRole('menuitemradio', {name: /^Admin/})).not.toBeInTheDocument();
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

    it('shows why a checkbox is locked via `disabled`', async () => {
        renderMenu({
            role: 'guest',
            permissionMenu: {
                options: ['comment_page'],
                selected: [],
                effective: ['read_page'],
                disabled: true,
                disabledReason: 'Guests can only view pages',
                onChange: jest.fn(),
            },
        });

        await openMenu();

        expect(screen.getByRole('menuitemcheckbox', {name: /Comment on pages/})).toHaveTextContent('Guests can only view pages');
    });

    it('shows why a checkbox is locked via `disabledOptions`, leaving unlocked checkboxes unlabeled', async () => {
        renderMenu({
            role: 'member',
            permissionMenu: {
                options: ['comment_page', 'admin_space'],
                selected: [],
                effective: ['read_page'],
                disabled: false,
                disabledOptions: ['admin_space'],
                disabledOptionsReason: 'Only a space administrator can grant this',
                onChange: jest.fn(),
            },
        });

        await openMenu();

        expect(screen.getByRole('menuitemcheckbox', {name: /Administer space/})).toHaveTextContent('Only a space administrator can grant this');
        expect(screen.getByRole('menuitemcheckbox', {name: 'Comment on pages'})).not.toHaveTextContent('Only a space administrator can grant this');
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
