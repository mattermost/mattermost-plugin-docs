// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {ClientError} from '@mattermost/client';

import {makeSpace} from 'store/test_fixtures';

import {toast} from 'components/toast';

import type {MemberProfile} from './members';
import {useManageSpaceMembers} from './space_members';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockAddSpaceMembers = jest.fn();
const mockRemoveSpaceMember = jest.fn();
const mockLeaveSpace = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    addSpaceMembers: (...args: unknown[]) => () => mockAddSpaceMembers(...args as []),
    removeSpaceMember: (...args: unknown[]) => () => mockRemoveSpaceMember(...args as []),
}));

jest.mock('./leave_space', () => ({useLeaveSpace: () => mockLeaveSpace}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const space = makeSpace('space-1', 'Engineering');

const profile = (id: string, displayName: string): MemberProfile => ({
    id,
    displayName,
    username: displayName.toLowerCase(),
    avatarUrl: '',
});

const render = () => {
    const store = makeTestStore();
    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                {children}
            </IntlProvider>
        </Provider>
    );

    return renderHook(() => useManageSpaceMembers(space), {wrapper}).result;
};

const clientError = (status: number, serverErrorId?: string) =>
    new ClientError('', {message: 'nope', status_code: status, url: '/x', server_error_id: serverErrorId});

describe('useManageSpaceMembers', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockLeaveSpace.mockResolvedValue(true);
    });

    it('adds every user and reports no failures', async () => {
        mockAddSpaceMembers.mockResolvedValue([]);
        const users = [profile('u1', 'Ada'), profile('u2', 'Grace')];
        const hook = render();

        let failed: MemberProfile[] = [];
        await act(async () => {
            failed = await hook.current.addMembers(users);
        });

        expect(mockAddSpaceMembers).toHaveBeenCalledWith('space-1', ['u1', 'u2']);
        expect(failed).toEqual([]);
        expect(toast.error).not.toHaveBeenCalled();
    });

    // The failed ids come back from the thunk; the caller needs the profiles it
    // passed in so it can restore exactly those chips.
    it('maps failed ids back to the profiles it was given', async () => {
        mockAddSpaceMembers.mockResolvedValue([{userId: 'u2', error: clientError(403)}]);
        const users = [profile('u1', 'Ada'), profile('u2', 'Grace')];
        const hook = render();

        let failed: MemberProfile[] = [];
        await act(async () => {
            failed = await hook.current.addMembers(users);
        });

        expect(failed).toEqual([users[1]]);
    });

    // A 403 is the one add failure the user can act on, so it says so by name.
    it('names the user and the reason for a single 403', async () => {
        mockAddSpaceMembers.mockResolvedValue([{userId: 'u2', error: clientError(403)}]);
        const hook = render();

        await act(async () => {
            await hook.current.addMembers([profile('u2', 'Grace')]);
        });

        expect(toast.error).toHaveBeenCalledWith("Grace isn't a member of this team.");
    });

    // Any other single-user failure gets the generic add message, not the
    // not-on-team wording, which is specific to a 403.
    it('names the user for a single non-403 failure', async () => {
        mockAddSpaceMembers.mockResolvedValue([{userId: 'u2', error: clientError(500)}]);
        const hook = render();

        await act(async () => {
            await hook.current.addMembers([profile('u2', 'Grace')]);
        });

        expect(toast.error).toHaveBeenCalledWith("Couldn't add Grace. Please try again.");
    });

    it('collapses a multi-failure batch to a count', async () => {
        mockAddSpaceMembers.mockResolvedValue([
            {userId: 'u1', error: clientError(403)},
            {userId: 'u2', error: clientError(500)},
        ]);
        const hook = render();

        await act(async () => {
            await hook.current.addMembers([profile('u1', 'Ada'), profile('u2', 'Grace')]);
        });

        expect(toast.error).toHaveBeenCalledWith("Couldn't add 2 people. Please try again.");
    });

    it('distinguishes the last-member refusal when removing someone', async () => {
        mockRemoveSpaceMember.mockRejectedValue(clientError(409, 'app.space.remove_member.last_member.app_error'));
        const hook = render();

        await act(async () => {
            await hook.current.removeMember('u1');
        });

        expect(toast.error).toHaveBeenCalledWith('A space must keep at least one member with access.');
    });

    it('distinguishes the last-admin refusal when removing someone', async () => {
        mockRemoveSpaceMember.mockRejectedValue(clientError(409, 'app.space.member.last_admin.app_error'));
        const hook = render();

        await act(async () => {
            await hook.current.removeMember('u1');
        });

        expect(toast.error).toHaveBeenCalledWith('A space must keep at least one administrator.');
    });

    it('reports a lock timeout as retryable when removing someone', async () => {
        mockRemoveSpaceMember.mockRejectedValue(clientError(409, 'app.space.lock_timeout.app_error'));
        const hook = render();

        await act(async () => {
            await hook.current.removeMember('u1');
        });

        expect(toast.error).toHaveBeenCalledWith('This space is being changed right now. Try again in a moment.');
    });

    it('reports any other removal failure generically', async () => {
        mockRemoveSpaceMember.mockRejectedValue(clientError(500));
        const hook = render();

        await act(async () => {
            await hook.current.removeMember('u1');
        });

        expect(toast.error).toHaveBeenCalledWith('Something went wrong. Please try again.');
    });

    // Leaving is one behaviour across the header menu, the sidebar row and here.
    it('delegates leaving to useLeaveSpace', async () => {
        const hook = render();

        let left = false;
        await act(async () => {
            left = await hook.current.leave();
        });

        expect(mockLeaveSpace).toHaveBeenCalled();
        expect(left).toBe(true);
        expect(toast.error).not.toHaveBeenCalled();
    });
});
