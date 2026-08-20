// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';
import {RestError} from 'client/rest';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {makeSpace} from 'store/test_fixtures';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';
import type {Permission, SpaceAccess, SpaceMember} from 'types/permissions';

import {useSpacePermissions} from './space_permissions';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockGetSpaceAccess = jest.fn();
const mockListAllSpaceMembers = jest.fn();
const mockSetDefaultPermissions = jest.fn();
const mockSetMemberPermissions = jest.fn();
const mockSetSpaceViewAccess = jest.fn();

jest.mock('client/space_permissions', () => ({
    getSpaceAccess: (...args: unknown[]) => mockGetSpaceAccess(...args as []),
    listAllSpaceMembers: (...args: unknown[]) => mockListAllSpaceMembers(...args as []),
    setDefaultPermissions: (...args: unknown[]) => mockSetDefaultPermissions(...args as []),
    setMemberPermissions: (...args: unknown[]) => mockSetMemberPermissions(...args as []),
    setSpaceViewAccess: (...args: unknown[]) => mockSetSpaceViewAccess(...args as []),
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const space = makeSpace('space-1', 'Engineering');

const access = (permissions: Permission[], defaults: Permission[] = []): SpaceAccess => ({
    id: 'space-1',
    default_permissions: defaults,
    permissions,
    view_access: 'open',
    update_at: 100,
});

const member = (userId: string, isAdmin = false): SpaceMember => ({
    user_id: userId,
    permissions: ['read_page'],
    granted_permissions: [],
    is_admin: isAdmin,
    is_guest: false,
    auto_joined: false,
});

// The server answers an AppError as a flat body; RestError carries the id the hook
// dispatches on separately from the status.
const restError = (status: number, serverErrorId?: string) =>
    new RestError('/x', status, 'nope', {}, serverErrorId);

const wrapper = ({children}: {children: React.ReactNode}) => (
    <Provider store={makeTestStore({currentUser: {id: 'me'}})}>
        <IntlProvider
            locale='en'
            messages={{}}
        >
            {children}
        </IntlProvider>
    </Provider>
);

const render = () => renderHook(() => useSpacePermissions(space), {wrapper}).result;

// Renders and waits for the initial load to resolve, so an assertion cannot pass
// against the pre-load defaults.
const renderLoaded = async () => {
    const hook = render();
    await waitFor(() => expect(hook.current.loading).toBe(false));
    return hook;
};

// Renders against a swappable space, for the assertions that need a SECOND load on the
// same hook instance — state that is only reset on reload has no killing assertion
// otherwise, since its initial value already matches what the reset would produce.
const renderSwitchable = async () => {
    const rendered = renderHook(
        ({current}: {current: Space}) => useSpacePermissions(current),
        {wrapper, initialProps: {current: space}},
    );
    await waitFor(() => expect(rendered.result.current.loading).toBe(false));

    const reloadWith = async (next: Space) => {
        rendered.rerender({current: next});
        await waitFor(() => expect(rendered.result.current.loading).toBe(false));
    };

    return {result: rendered.result, reloadWith};
};

describe('useSpacePermissions', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockGetSpaceAccess.mockResolvedValue(access([]));
        mockListAllSpaceMembers.mockResolvedValue([]);
    });

    describe('canAdminister', () => {
        // The regression this exists for: a system administrator holds admin_space without
        // holding a channel-member row, so deriving this from the roster locks them out of
        // a space they can in fact administer.
        it('is true from the caller\'s own permissions even when absent from the roster', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'admin_space']));
            mockListAllSpaceMembers.mockResolvedValue([member('someone-else', true)]);

            const hook = await renderLoaded();

            expect(hook.current.canAdminister).toBe(true);
            expect(hook.current.loadFailed).toBe(false);
        });

        it('is false when the caller\'s permissions omit admin_space', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'create_page']));
            mockListAllSpaceMembers.mockResolvedValue([member('me')]);

            const hook = await renderLoaded();

            expect(hook.current.canAdminister).toBe(false);
        });
    });

    describe('a failed initial read', () => {
        // Without loadFailed the surface cannot tell "the read failed" from "you are not an
        // administrator", and states the latter.
        it('is reported separately from not being an administrator', async () => {
            mockGetSpaceAccess.mockRejectedValue(restError(500));

            const hook = await renderLoaded();

            expect(hook.current.loadFailed).toBe(true);
            expect(hook.current.canAdminister).toBe(false);
            expect(hook.current.members.size).toBe(0);
        });

        it('does not toast, since the emptied surface already shows it', async () => {
            mockGetSpaceAccess.mockRejectedValue(restError(403));

            await renderLoaded();

            expect(toast.error).not.toHaveBeenCalled();
        });

        // Both flags are reset on reload rather than only ever set, so switching to a space
        // that reads cleanly must clear them. Asserting this from an initial failure means
        // the initial state cannot stand in for the reset.
        it('clears once a later space loads cleanly', async () => {
            mockGetSpaceAccess.mockRejectedValue(restError(500));
            const {result, reloadWith} = await renderSwitchable();
            expect(result.current.loadFailed).toBe(true);

            mockGetSpaceAccess.mockResolvedValue(access(['admin_space']));
            await reloadWith(makeSpace('space-2', 'Design'));

            expect(result.current.loadFailed).toBe(false);
            expect(result.current.canAdminister).toBe(true);
        });

        // The mirror of the above: an administrator switching to a space whose read fails
        // must not keep the authority the previous space resolved for them.
        it('drops a previously resolved authority when a later space fails to load', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['admin_space']));
            const {result, reloadWith} = await renderSwitchable();
            expect(result.current.canAdminister).toBe(true);

            mockGetSpaceAccess.mockRejectedValue(restError(500));
            await reloadWith(makeSpace('space-2', 'Design'));

            expect(result.current.canAdminister).toBe(false);
            expect(result.current.loadFailed).toBe(true);
        });
    });

    describe('setDefaults', () => {
        it('applies the set the server returns, not the one it was given', async () => {
            mockSetDefaultPermissions.mockResolvedValue(access(['admin_space'], ['create_page']));
            const hook = await renderLoaded();

            await act(async () => {
                await hook.current.setDefaults(['create_page', 'edit_page']);
            });

            expect(mockSetDefaultPermissions).toHaveBeenCalledWith('space-1', ['create_page', 'edit_page']);
            expect(hook.current.defaults).toEqual(['create_page']);
        });

        // The write committed; only the roster refresh that follows it failed. Reporting
        // that as a failure tells the caller their saved change was lost.
        it('does not report a failure when only the roster refresh fails', async () => {
            mockSetDefaultPermissions.mockResolvedValue(access([], ['create_page']));
            const hook = await renderLoaded();

            mockListAllSpaceMembers.mockRejectedValue(restError(500));

            await act(async () => {
                await hook.current.setDefaults(['create_page']);
            });

            expect(toast.error).not.toHaveBeenCalled();
            expect(hook.current.defaults).toEqual(['create_page']);
            expect(hook.current.busy).toBe(false);
        });

        it('reports a failure of the write itself', async () => {
            mockSetDefaultPermissions.mockRejectedValue(restError(500));
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setDefaults(['create_page'])).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith('Something went wrong. Please try again.');
        });
    });

    describe('setMemberGrants', () => {
        it('names the last-administrator refusal', async () => {
            mockSetMemberPermissions.mockRejectedValue(restError(409, 'app.space.member.last_admin.app_error'));
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setMemberGrants('u1', [])).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith('A space must keep at least one administrator.');
        });

        // A different rule, a different status (400, not 409) — so the message is chosen by
        // the server's error id rather than by the status code.
        it('names the guest refusal', async () => {
            mockSetMemberPermissions.mockRejectedValue(restError(400, 'app.space.member.guest_not_assignable.app_error'));
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setMemberGrants('u1', ['edit_page'])).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith('Guests are read-only and cannot be granted permissions.');
        });

        it('falls back to the generic message for any other refusal', async () => {
            mockSetMemberPermissions.mockRejectedValue(restError(500));
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setMemberGrants('u1', ['edit_page'])).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith('Something went wrong. Please try again.');
        });
    });

    describe('setViewAccess', () => {
        it('sends the load\'s update_at as the optimistic-lock baseline', async () => {
            mockSetSpaceViewAccess.mockResolvedValue({...access([]), view_access: 'private', update_at: 200});
            const hook = await renderLoaded();

            await act(async () => {
                await hook.current.setViewAccess('private');
            });

            expect(mockSetSpaceViewAccess).toHaveBeenCalledWith('space-1', 'private', 100);
            expect(hook.current.viewAccess).toBe('private');
        });

        // The baseline has to advance to what the write returned. Left at the load's value, a
        // second flip sends a stale update_at and the server rejects an uncontended edit as a
        // conflict — which only a second call can catch.
        it('advances the baseline to the value the previous write returned', async () => {
            mockSetSpaceViewAccess.mockResolvedValue({...access([]), view_access: 'private', update_at: 200});
            const hook = await renderLoaded();

            await act(async () => {
                await hook.current.setViewAccess('private');
            });

            mockSetSpaceViewAccess.mockResolvedValue({...access([]), view_access: 'open', update_at: 300});
            await act(async () => {
                await hook.current.setViewAccess('open');
            });

            expect(mockSetSpaceViewAccess).toHaveBeenLastCalledWith('space-1', 'open', 200);
            expect(hook.current.viewAccess).toBe('open');
        });

        it('names a concurrent edit on a 409', async () => {
            mockSetSpaceViewAccess.mockRejectedValue(restError(409, 'app.store.conflict.app_error'));
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setViewAccess('private')).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith('Someone else changed this space. Reopen settings and try again.');
        });
    });
});
