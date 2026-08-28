// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {SpaceTypes} from 'store/action_types';

import type {PluginRegistry} from 'types/mattermost-webapp';

import {makeTestStore} from '../tests/react_testing_utils';

const mockFetchSpace = jest.fn();
const mockFetchSpaces = jest.fn();
const mockFetchSpaceMembers = jest.fn();
const mockRefreshSpaceAfterMemberPermissionsChanged = jest.fn();
const mockRefreshSpaceAfterSelfRemoval = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    fetchSpace: (...args: unknown[]) => () => mockFetchSpace(...args as []),
    fetchSpaces: (...args: unknown[]) => () => mockFetchSpaces(...args as []),
    fetchSpaceMembers: (...args: unknown[]) => () => mockFetchSpaceMembers(...args as []),
    refreshSpaceAfterMemberPermissionsChanged: (...args: unknown[]) => () => mockRefreshSpaceAfterMemberPermissionsChanged(...args as []),
    refreshSpaceAfterSelfRemoval: (...args: unknown[]) => () => mockRefreshSpaceAfterSelfRemoval(...args as []),
}));

type FakeMessage = {data?: {space_id?: string; user_id?: string}};
type FakeHandler = (msg: FakeMessage) => void;

// A minimal stand-in for the host's PluginRegistry: captures the handlers this
// plugin registers so a test can fire them directly, without a real WebSocket.
const makeFakeRegistry = () => {
    const handlers = new Map<string, FakeHandler>();
    let reconnectHandler: (() => void) | undefined;

    const registry = {
        registerTranslations: jest.fn(),
        registerReducer: jest.fn(),
        registerWebSocketEventHandler: jest.fn((event: string, handler: FakeHandler) => {
            handlers.set(event, handler);
        }),
        registerReconnectHandler: jest.fn((handler: () => void) => {
            reconnectHandler = handler;
        }),
        registerProduct: jest.fn(),
    } as unknown as PluginRegistry;

    return {
        registry,
        fire: (event: string, msg: FakeMessage = {}) => handlers.get(event)?.(msg),
        fireReconnect: () => reconnectHandler?.(),
    };
};

const eventName = (suffix: string) => `custom_${manifest.id}_${suffix}`;

describe('Docs plugin WebSocket wiring', () => {
    let PluginClass: new () => {initialize: (registry: PluginRegistry, store: unknown) => Promise<void>};
    let fake: ReturnType<typeof makeFakeRegistry>;
    let store: ReturnType<typeof makeTestStore>;
    let dispatchSpy: jest.SpyInstance;

    beforeAll(() => {
        (window as unknown as {registerPlugin: jest.Mock}).registerPlugin = jest.fn();

        // A dynamic require, not a static import: the module registers itself on
        // window at load time, which must happen after the stub above is in place.
        // eslint-disable-next-line global-require
        PluginClass = require('./index').default;
    });

    beforeEach(async () => {
        jest.clearAllMocks();

        store = makeTestStore({currentUser: {id: 'user1'}});
        dispatchSpy = jest.spyOn(store, 'dispatch');
        fake = makeFakeRegistry();

        await new PluginClass().initialize(fake.registry, store);
    });

    it('refreshes the team space list when space_created fires', () => {
        fake.fire(eventName('space_created'), {data: {space_id: 'space1'}});

        expect(mockFetchSpaces).toHaveBeenCalled();
    });

    it('re-fetches the space when space_restored fires, reversing a prior eviction', () => {
        fake.fire(eventName('space_restored'), {data: {space_id: 'space1'}});

        expect(mockFetchSpace).toHaveBeenCalledWith('space1');
    });

    it('re-fetches the space when space_updated fires', () => {
        fake.fire(eventName('space_updated'), {data: {space_id: 'space1'}});

        expect(mockFetchSpace).toHaveBeenCalledWith('space1');
    });

    it('dispatches DELETED_SPACE when space_deleted fires', () => {
        fake.fire(eventName('space_deleted'), {data: {space_id: 'space1'}});

        expect(dispatchSpy).toHaveBeenCalledWith({type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'});
    });

    it('re-syncs the space and roster when space_member_added fires', () => {
        fake.fire(eventName('space_member_added'), {data: {space_id: 'space1'}});

        expect(mockFetchSpace).toHaveBeenCalledWith('space1');
        expect(mockFetchSpaceMembers).toHaveBeenCalledWith('space1');
    });

    it('re-syncs shared access and bumps the grant revision when space_member_permissions_updated fires', () => {
        fake.fire(eventName('space_member_permissions_updated'), {data: {space_id: 'space1'}});

        expect(mockRefreshSpaceAfterMemberPermissionsChanged).toHaveBeenCalledWith('space1');
    });

    it('evicts only after a definitive denial when the current user is the one removed', () => {
        fake.fire(eventName('space_member_removed'), {data: {space_id: 'space1', user_id: 'user1'}});

        expect(mockRefreshSpaceAfterSelfRemoval).toHaveBeenCalledWith('space1');
        expect(mockFetchSpace).not.toHaveBeenCalled();
        expect(mockFetchSpaceMembers).not.toHaveBeenCalled();
    });

    it('re-syncs the roster rather than evicting when someone else is removed', () => {
        fake.fire(eventName('space_member_removed'), {data: {space_id: 'space1', user_id: 'someone-else'}});

        expect(mockFetchSpace).toHaveBeenCalledWith('space1');
        expect(mockFetchSpaceMembers).toHaveBeenCalledWith('space1');
        expect(mockRefreshSpaceAfterSelfRemoval).not.toHaveBeenCalled();
    });

    it('reconciles the team space list on reconnect', () => {
        fake.fireReconnect();

        expect(mockFetchSpaces).toHaveBeenCalled();
    });
});
