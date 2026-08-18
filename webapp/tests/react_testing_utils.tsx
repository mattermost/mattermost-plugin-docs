// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {render} from '@testing-library/react';
import type {RenderOptions, RenderResult} from '@testing-library/react';
import {createMemoryHistory} from 'history';
import type {MemoryHistory} from 'history';
import manifest from 'manifest';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {Router} from 'react-router-dom';
import {applyMiddleware, legacy_createStore as createStore} from 'redux';
import type {Middleware, Store} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import type {DocsEntitiesState} from 'store/types';

const EMPTY_DOCS_ENTITIES: DocsEntitiesState = {
    spaces: {},
    spacesInTeam: {},
    pages: {},
    pagesInSpace: {},
    spaceMembers: {},
    drafts: {},
    draftsInSpace: {},
};

type TestTeam = {id: string; name: string; display_name?: string};
type TestUser = {id: string; username?: string; first_name?: string; last_name?: string; nickname?: string};

export type TestStateOptions = {
    docs?: Partial<DocsEntitiesState>;
    currentTeam?: TestTeam;

    // The user's teams (for cross-team reads like the switcher). Defaults to just
    // the current team.
    teams?: TestTeam[];
    currentUser?: TestUser;

    // Host user preferences, which is where Docs favorites and sidebar order live.
    preferences?: Array<{category: string; name: string; value: string}>;
};

// Builds a host-shaped GlobalState with the Docs plugin subtree under
// `plugins-<id>` (where the plugin's registered reducer mounts) plus the minimal
// entities the Docs hooks read (current team + membership + current user).
export function makeTestState({docs, currentTeam, teams, currentUser, preferences}: TestStateOptions = {}): GlobalState {
    const teamId = currentTeam?.id ?? '';
    const userId = currentUser?.id ?? '';
    const allTeams = teams ?? (currentTeam ? [currentTeam] : []);

    return {
        ['plugins-' + manifest.id]: {entities: {...EMPTY_DOCS_ENTITIES, ...docs}},
        entities: {
            teams: {
                currentTeamId: teamId,

                // delete_at + a membership are what getMyTeams filters on.
                teams: Object.fromEntries(allTeams.map((team) => [team.id, {...team, delete_at: 0}])),
                myMembers: Object.fromEntries(allTeams.map((team) => [team.id, {team_id: team.id, user_id: userId}])),
            },
            users: {
                currentUserId: userId,
                profiles: currentUser ? {[userId]: currentUser} : {},
            },

            // Empty but present: host selectors a Docs component may reach for
            // (the teammate name-display preference, say) index into these
            // without guarding, so omitting them throws rather than defaulting.
            general: {config: {}, license: {}},

            // Keyed `category--name`, as mattermost-redux's preference selectors expect.
            preferences: {
                myPreferences: Object.fromEntries((preferences ?? []).map((preference) => [
                    `${preference.category}--${preference.name}`,
                    {user_id: userId, ...preference},
                ])),
            },
        },
    } as unknown as GlobalState;
}

// Docs actions are thunks (createSpace etc.); a minimal thunk middleware lets
// them dispatch without pulling in redux-thunk.
const thunk: Middleware = ({dispatch, getState}) => (next) => (action) =>
    (typeof action === 'function' ? action(dispatch, getState) : next(action));

// A fixed-state store (identity reducer) is enough for component tests: they
// assert rendering and callbacks, not reducer transitions. For store-reactivity
// seams, build a store from the real reducer instead (see store tests).
export function makeTestStore(options?: TestStateOptions): Store<GlobalState> {
    const state = makeTestState(options);
    return createStore((s: GlobalState = state) => s, applyMiddleware(thunk));
}

type RenderWithContextOptions = RenderOptions & {
    store?: Store<GlobalState>;
    state?: TestStateOptions;
    history?: MemoryHistory;
    route?: string;
};

export type RenderWithContextResult = RenderResult & {
    store: Store<GlobalState>;
    history: MemoryHistory;
};

// Wraps `ui` in the providers Docs components expect: Redux store, react-intl
// (defaultMessage fallbacks; i18n is mocked to {}), and a react-router history.
export function renderWithContext(ui: React.ReactElement, options: RenderWithContextOptions = {}): RenderWithContextResult {
    const {store: providedStore, state, history: providedHistory, route, ...renderOptions} = options;

    const store = providedStore ?? makeTestStore(state);
    const history = providedHistory ?? createMemoryHistory({initialEntries: [route ?? '/']});

    const Wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                <Router history={history}>
                    {children}
                </Router>
            </IntlProvider>
        </Provider>
    );

    return {
        ...render(ui, {wrapper: Wrapper, ...renderOptions}),
        store,
        history,
    };
}
