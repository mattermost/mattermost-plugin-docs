// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';
import {RestError} from 'client/rest';
import manifest from 'manifest';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {legacy_createStore as createStore} from 'redux';
import type {UnknownAction} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {SpaceTypes} from 'store/action_types';
import docsReducer from 'store/reducer';
import {makeSpace} from 'store/test_fixtures';
import type {DocsPluginState} from 'store/types';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';
import type {Permission, SpaceAccess, SpaceMember} from 'types/permissions';

import {useSpacePermissions} from './space_permissions';

import {makeTestState} from '../../tests/react_testing_utils';

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

// `from` is the space the response describes. It matters because the hook writes each response into
// the spaces slice and reads the caller's tiers back out of it by space id, so a response carrying
// the wrong id resolves to no tiers at all.
const access = (permissions: Permission[], defaults: Permission[] = [], from: Space = space): SpaceAccess => ({
    ...from,
    default_permissions: defaults,
    permissions,

    // These fixtures describe a caller who is already in the space, so there is nothing to join.
    can_join: false,
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

// A store running the real Docs reducer, so the hook's own dispatches land: it writes each resolved
// read into the spaces slice and reads the caller's tiers back out of it.
const pluginKey = 'plugins-' + manifest.id;
const makeLiveStore = (docs: Record<string, unknown> = {}) => {
    const initial = makeTestState({currentUser: {id: 'me'}, docs});

    return createStore((state: GlobalState = initial, action: UnknownAction): GlobalState => ({
        ...state,
        [pluginKey]: docsReducer((state as unknown as Record<string, DocsPluginState>)[pluginKey], action),
    } as unknown as GlobalState));
};

let liveStore = makeLiveStore();

const wrapper = ({children}: {children: React.ReactNode}) => (
    <Provider store={liveStore}>
        <IntlProvider
            locale='en'
            messages={{}}
        >
            {children}
        </IntlProvider>
    </Provider>
);

const render = () => renderHook(() => useSpacePermissions(space), {wrapper}).result;

// Seeds the roster slice the hook's reload watches.
const makeRosterStore = () => makeLiveStore({spaceMembers: {'space-1': ['me']}});

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

        // manage_space by default: this surface is only reached behind the manage tier, and the
        // roster read is now gated on it (see the redacted-roster describe below).
        mockGetSpaceAccess.mockResolvedValue(access(['manage_space']));
        mockListAllSpaceMembers.mockResolvedValue([]);
        liveStore = makeLiveStore();
    });

    describe('canAdminister', () => {
        // The regression this exists for: a system administrator holds admin_space without
        // holding a channel-member row, so deriving this from the roster locks them out of
        // a space they can in fact administer.
        it('is true from the caller\'s own permissions even when absent from the roster', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'admin_space', 'manage_space']));
            mockListAllSpaceMembers.mockResolvedValue([member('someone-else', true)]);

            const hook = await renderLoaded();

            expect(hook.current.canAdminister).toBe(true);
            expect(hook.current.loadFailed).toBe(false);
        });

        it('is false when the caller\'s permissions omit admin_space', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'create_page', 'manage_space']));
            mockListAllSpaceMembers.mockResolvedValue([member('me')]);

            const hook = await renderLoaded();

            expect(hook.current.canAdminister).toBe(false);
        });

        it('loads and enables the roster for an admin_space-only caller', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'admin_space']));
            mockListAllSpaceMembers.mockResolvedValue([member('me', true)]);

            const hook = await renderLoaded();

            expect(mockListAllSpaceMembers).toHaveBeenCalledWith('space-1');
            expect(hook.current.members.get('me')).toBeDefined();
            expect(hook.current.canAdminister).toBe(true);
            expect(hook.current.canManageMembers).toBe(true);
            expect(hook.current.loadFailed).toBe(false);
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

            const second = makeSpace('space-2', 'Design');
            mockGetSpaceAccess.mockResolvedValue(access(['admin_space', 'manage_space'], [], second));
            await reloadWith(second);

            expect(result.current.loadFailed).toBe(false);
            expect(result.current.canAdminister).toBe(true);
        });

        // The default set goes with the authority: left standing, the surface would attribute the
        // previous space's defaults to a space it never read. View access is not in that set — it
        // rides on the space record itself, so a space whose read failed shows its own stored value
        // rather than the previous space's, which is what the second assertion pins.
        it('drops the previously resolved policy when a later space fails to load', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['admin_space', 'manage_space'], ['create_page']));
            const {result, reloadWith} = await renderSwitchable();
            expect(result.current.defaults).toEqual(['create_page']);
            expect(result.current.viewAccess).toBe('open');

            mockGetSpaceAccess.mockRejectedValue(restError(500));
            await reloadWith(makeSpace('space-2', 'Design'));

            expect(result.current.defaults).toEqual([]);
            expect(result.current.viewAccess).toBe('private');
        });

        // The mirror of the above: an administrator switching to a space whose read fails
        // must not keep the authority the previous space resolved for them.
        it('drops a previously resolved authority when a later space fails to load', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['admin_space', 'manage_space']));
            const {result, reloadWith} = await renderSwitchable();
            expect(result.current.canAdminister).toBe(true);

            mockGetSpaceAccess.mockRejectedValue(restError(500));
            await reloadWith(makeSpace('space-2', 'Design'));

            expect(result.current.canAdminister).toBe(false);
            expect(result.current.loadFailed).toBe(true);
        });
    });

    // The roster route serves every reader so a space view can render its member count, but it
    // redacts the per-member permission matrix below the manage tier. This surface edits that
    // matrix, so it must not read a roster it would have to display as an answer.
    describe('a caller without the manage tier', () => {
        it('does not read the roster at all', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'create_page']));

            await renderLoaded();

            expect(mockListAllSpaceMembers).not.toHaveBeenCalled();
        });

        // Reported the way a failed read is, rather than as an empty roster: an all-empty grid
        // would state that every member holds nothing.
        it('reports it as a failed read rather than an empty roster', async () => {
            mockGetSpaceAccess.mockResolvedValue(access(['read_page', 'create_page']));

            const hook = await renderLoaded();

            expect(hook.current.loadFailed).toBe(true);
            expect(hook.current.members.size).toBe(0);
            expect(hook.current.canManageMembers).toBe(false);
            expect(hook.current.canAdminister).toBe(false);
        });
    });

    // The tab's own add/remove field and the websocket membership handlers both write the roster
    // to the store, and neither touches this hook's snapshot of the grant matrix.
    describe('a membership change in the store', () => {
        it('re-reads the roster, so a newly added member has a permission row', async () => {
            const store = makeRosterStore();
            const rosterWrapper = ({children}: {children: React.ReactNode}) => (
                <Provider store={store}>
                    <IntlProvider
                        locale='en'
                        messages={{}}
                    >
                        {children}
                    </IntlProvider>
                </Provider>
            );

            const {result} = renderHook(() => useSpacePermissions(space), {wrapper: rosterWrapper});
            await waitFor(() => expect(result.current.loading).toBe(false));
            expect(result.current.members.get('u2')).toBeUndefined();
            const accessReadsAfterLoad = mockGetSpaceAccess.mock.calls.length;

            mockListAllSpaceMembers.mockResolvedValue([member('u2')]);
            act(() => {
                store.dispatch({type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId: space.id, userId: 'u2'});
            });

            await waitFor(() => expect(result.current.members.get('u2')).toBeDefined());
            expect(mockGetSpaceAccess).toHaveBeenCalledTimes(accessReadsAfterLoad);
        });
    });

    // Another administrator's change reaches this client as a websocket event. Its thunk first
    // stores the fresh space access, then bumps the grant revision so this hook only has to reload
    // its local matrix. Without the revision the surface would keep showing what was true when it
    // mounted; re-reading access here would duplicate the thunk's request.
    describe('an access change reported by the server', () => {
        it('uses the refreshed space and re-reads only the grant matrix', async () => {
            const store = makeRosterStore();
            const revisionWrapper = ({children}: {children: React.ReactNode}) => (
                <Provider store={store}>
                    <IntlProvider
                        locale='en'
                        messages={{}}
                    >
                        {children}
                    </IntlProvider>
                </Provider>
            );

            const {result} = renderHook(() => useSpacePermissions(space), {wrapper: revisionWrapper});
            await waitFor(() => expect(result.current.loading).toBe(false));
            expect(result.current.defaults).toEqual([]);
            const accessReadsAfterLoad = mockGetSpaceAccess.mock.calls.length;

            mockListAllSpaceMembers.mockResolvedValue([member('me', true), member('u2')]);
            act(() => {
                store.dispatch({
                    type: SpaceTypes.RECEIVED_SPACES,
                    spaces: [access(['admin_space', 'manage_space'], ['create_page'])],
                });
                store.dispatch({type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId: space.id});
            });

            await waitFor(() => expect(result.current.defaults).toEqual(['create_page']));
            await waitFor(() => expect(result.current.members.get('u2')).toBeDefined());
            expect(mockGetSpaceAccess).toHaveBeenCalledTimes(accessReadsAfterLoad);
        });

        it('clears the matrix without reading a redacted roster after manage access is revoked', async () => {
            const store = makeRosterStore();
            const revisionWrapper = ({children}: {children: React.ReactNode}) => (
                <Provider store={store}>
                    <IntlProvider
                        locale='en'
                        messages={{}}
                    >
                        {children}
                    </IntlProvider>
                </Provider>
            );

            mockListAllSpaceMembers.mockResolvedValue([member('me', true)]);
            const {result} = renderHook(() => useSpacePermissions(space), {wrapper: revisionWrapper});
            await waitFor(() => expect(result.current.loading).toBe(false));
            expect(result.current.members.get('me')).toBeDefined();
            const rosterReadsAfterLoad = mockListAllSpaceMembers.mock.calls.length;

            act(() => {
                store.dispatch({
                    type: SpaceTypes.RECEIVED_SPACES,
                    spaces: [access(['read_page'])],
                });
                store.dispatch({type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId: space.id});
            });

            await waitFor(() => expect(result.current.loadFailed).toBe(true));
            expect(result.current.canManageMembers).toBe(false);
            expect(result.current.members.size).toBe(0);
            expect(mockListAllSpaceMembers).toHaveBeenCalledTimes(rosterReadsAfterLoad);
        });

        it('ignores a change reported for another space', async () => {
            const store = makeRosterStore();
            const revisionWrapper = ({children}: {children: React.ReactNode}) => (
                <Provider store={store}>
                    <IntlProvider
                        locale='en'
                        messages={{}}
                    >
                        {children}
                    </IntlProvider>
                </Provider>
            );

            const {result} = renderHook(() => useSpacePermissions(space), {wrapper: revisionWrapper});
            await waitFor(() => expect(result.current.loading).toBe(false));
            const readsAfterLoad = mockGetSpaceAccess.mock.calls.length;

            act(() => {
                store.dispatch({type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId: 'space-2'});
            });

            expect(mockGetSpaceAccess).toHaveBeenCalledTimes(readsAfterLoad);
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

        it.each([
            ['a lock timeout', restError(409, 'app.space.lock_timeout.app_error'), 'This space is being changed right now. Try again in a moment.'],
            ['a concurrent edit', restError(409, 'app.store.conflict.app_error'), 'Someone else changed this space. Reopen settings and try again.'],
            ['an expired custom-scheme entitlement', restError(501, 'app.scheme.plugin_scheme.scheme_license.app_error'), 'Custom permission combinations require a Professional or Enterprise license.'],
        ])('names %s', async (_case, error, message) => {
            mockSetDefaultPermissions.mockRejectedValue(error);
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setDefaults(['create_page'])).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith(message);
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

        it.each([
            ['a lock timeout', restError(409, 'app.space.lock_timeout.app_error'), 'This space is being changed right now. Try again in a moment.'],
            ['a concurrent edit', restError(409, 'app.store.conflict.app_error'), 'Someone else changed this space. Reopen settings and try again.'],
        ])('names %s', async (_case, error, message) => {
            mockSetMemberPermissions.mockRejectedValue(error);
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setMemberGrants('u1', ['edit_page'])).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith(message);
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

        it('names a lock timeout separately from a conflict', async () => {
            mockSetSpaceViewAccess.mockRejectedValue(restError(409, 'app.space.lock_timeout.app_error'));
            const hook = await renderLoaded();

            await act(async () => {
                await expect(hook.current.setViewAccess('private')).rejects.toThrow();
            });

            expect(toast.error).toHaveBeenCalledWith('This space is being changed right now. Try again in a moment.');
        });
    });
});
