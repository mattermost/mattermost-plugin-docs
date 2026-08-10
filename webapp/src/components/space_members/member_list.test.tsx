// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import type {MemberProfile} from 'hooks/members';
import React from 'react';

import {DocsModalController, closeAllDocsModals} from 'components/modals';
import {getDocsModalStack} from 'components/modals/modal_store';

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

describe('MemberList confirmations', () => {
    const state = {currentUser: {id: 'u1', username: 'ada'}};

    const renderRoster = (actions: {onRemove?: jest.Mock; onLeave: jest.Mock}) => renderWithContext(
        <>
            <MemberList
                members={members}
                avatarSize='sm'
                spaceTitle='Engineering'
                actions={{...actions, disabled: false}}
            />
            <DocsModalController/>
        </>,
        {state},
    );

    const openRowMenu = async (name: RegExp, item: string) => {
        fireEvent.click(screen.getByRole('button', {name}));
        fireEvent.click(await screen.findByRole('menuitem', {name: item}));
    };

    afterEach(() => act(() => {
        closeAllDocsModals();
    }));

    it('removes a member only after the confirmation is accepted', async () => {
        const actions = {onRemove: jest.fn(), onLeave: jest.fn()};
        renderRoster(actions);

        await openRowMenu(/Grace/, 'Remove from space');
        expect(actions.onRemove).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Yes, remove'}));

        await waitFor(() => expect(actions.onRemove).toHaveBeenCalledWith('u2'));
    });

    it('leaves only after the confirmation is accepted', async () => {
        const actions = {onRemove: jest.fn(), onLeave: jest.fn()};
        renderRoster(actions);

        await openRowMenu(/Ada/, 'Leave space');
        expect(actions.onLeave).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Yes, leave space'}));

        await waitFor(() => expect(actions.onLeave).toHaveBeenCalled());
    });

    it('shows only the current user action when removal is unavailable', () => {
        renderRoster({onLeave: jest.fn()});

        expect(screen.getByRole('button', {name: /Ada/})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: /Grace/})).not.toBeInTheDocument();
    });

    it('does nothing when the confirmation is cancelled', async () => {
        const actions = {onRemove: jest.fn(), onLeave: jest.fn()};
        renderRoster(actions);

        await openRowMenu(/Grace/, 'Remove from space');
        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));

        await waitFor(() => expect(getDocsModalStack()).toHaveLength(0));
        expect(actions.onRemove).not.toHaveBeenCalled();
    });

    // The modal runs the confirm handler INSTEAD of its own onClose, so a confirm
    // that forgets to close leaves an invisible entry on the stack forever.
    it('pops the confirmation off the modal stack on confirm, not just on cancel', async () => {
        const actions = {onRemove: jest.fn(), onLeave: jest.fn()};
        renderRoster(actions);

        await openRowMenu(/Grace/, 'Remove from space');
        expect(getDocsModalStack()).toHaveLength(1);

        fireEvent.click(screen.getByRole('button', {name: 'Yes, remove'}));

        await waitFor(() => expect(getDocsModalStack()).toHaveLength(0));
    });
});
