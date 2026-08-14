// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook} from '@testing-library/react';
import {FAVORITES_CATEGORY} from 'data/favorites';
import React from 'react';
import {Provider} from 'react-redux';

import {makeSpace} from 'store/test_fixtures';

import {useSidebarSpaces} from './use_sidebar_spaces';

import {makeTestStore} from '../../../tests/react_testing_utils';

const mockSetOrder = jest.fn();
let mockOrder = {favorites: [] as string[], spaces: [] as string[]};

// mattermost-redux/actions/preferences is untransformed ESM under jest, so the
// preference-writing hooks are mocked at their own boundary.
jest.mock('hooks/favorites', () => ({
    useSidebarOrder: () => ({order: mockOrder, setOrder: mockSetOrder}),
    useToggleFavorite: () => jest.fn(),
}));

jest.mock('./dnd/use_spaces_dnd', () => ({useSpacesDnd: () => undefined}));

const TEAM = {id: 'team1', name: 'team-1'};
const HERE = makeSpace('here', 'In this team', TEAM.id);
const ELSEWHERE = makeSpace('elsewhere', 'Another team', 'team2');

const favorite = (id: string, type: string) => ({category: FAVORITES_CATEGORY, name: id, value: type});

const render = (favorites: Array<[string, string]>) => {
    const store = makeTestStore({
        currentTeam: TEAM,
        docs: {
            spaces: {[HERE.id]: HERE, [ELSEWHERE.id]: ELSEWHERE},
            spacesInTeam: {[TEAM.id]: new Set([HERE.id]), team2: new Set([ELSEWHERE.id])},
        },
        preferences: favorites.map(([id, type]) => favorite(id, type)),
    });

    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>{children}</Provider>
    );

    return renderHook(() => useSidebarSpaces(false), {wrapper}).result;
};

describe('useSidebarSpaces', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockOrder = {favorites: [], spaces: []};
    });

    // Favorites are a user preference, so the stored list spans every team. An
    // out-of-team favorite has no row to render here, so counting it would leave
    // the Favorites category looking populated while showing nothing.
    it('scopes favorites to the current team', () => {
        const {current} = render([['here', 'space'], ['elsewhere', 'space']]);

        expect(current.favoriteSpaceIds).toEqual([HERE.id]);
    });

    it('leaves a favorited space out of the plain spaces list', () => {
        const {current} = render([['here', 'space']]);

        expect(current.spacesOrder).toEqual([]);
    });

    it('lists an unfavorited team space under spaces', () => {
        const {current} = render([]);

        expect(current.favoriteSpaceIds).toEqual([]);
        expect(current.spacesOrder).toEqual([HERE.id]);
    });

    it('ignores a stored order entry for a space that is not loaded', () => {
        mockOrder = {favorites: [], spaces: ['space:gone', `space:${HERE.id}`]};
        const {current} = render([]);

        expect(current.spacesOrder).toEqual([HERE.id]);
    });
});
