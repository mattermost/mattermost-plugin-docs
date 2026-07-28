// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {makePage, makeSpace, makeTeam} from 'store/test_fixtures';

import DocsSwitcher from './docs_switcher';

import {renderWithContext} from '../../../tests/react_testing_utils';

const state = {
    docs: {
        spaces: {eng: makeSpace('eng', 'Engineering', 'team1'), sales: makeSpace('sales', 'Sales', 'team1')},
        spacesInTeam: {team1: new Set(['eng', 'sales'])},
        pages: {},
        pagesInSpace: {},
    },
    currentTeam: makeTeam('team1', 'myteam'),
};

const stateWithPage = {
    docs: {
        spaces: {eng: makeSpace('eng', 'Engineering', 'team1')},
        spacesInTeam: {team1: new Set(['eng'])},
        pages: {onboarding: makePage('onboarding', 'eng', 'Onboarding')},
        pagesInSpace: {eng: new Set(['onboarding'])},
    },
    currentTeam: makeTeam('team1', 'myteam'),
};

function renderSwitcher(onClose = jest.fn()) {
    return renderWithContext(<DocsSwitcher onClose={onClose}/>, {state});
}

describe('DocsSwitcher', () => {
    it('renders the search combobox and lists the user spaces', () => {
        renderSwitcher();

        expect(screen.getByRole('combobox')).toBeInTheDocument();
        expect(screen.getByRole('option', {name: /Engineering/})).toBeInTheDocument();
        expect(screen.getByRole('option', {name: /Sales/})).toBeInTheDocument();
    });

    it('filters results by query', () => {
        renderSwitcher();

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'sales'}});

        expect(screen.getByRole('option', {name: /Sales/})).toBeInTheDocument();
        expect(screen.queryByRole('option', {name: /Engineering/})).not.toBeInTheDocument();
    });

    it('shows an empty state when nothing matches', () => {
        renderSwitcher();

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'zzz'}});
        expect(screen.getByText('No spaces or pages found')).toBeInTheDocument();
    });

    it('navigates to a space on click and closes', () => {
        const onClose = jest.fn();
        const {history} = renderSwitcher(onClose);

        fireEvent.click(screen.getByRole('option', {name: /Engineering/}));

        expect(history.location.pathname).toBe('/myteam/spaces/eng');
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('selects the highlighted result with Enter after keyboard navigation', () => {
        const {history} = renderSwitcher();

        const combobox = screen.getByRole('combobox');
        fireEvent.keyDown(combobox, {key: 'ArrowDown'});
        fireEvent.keyDown(combobox, {key: 'Enter'});

        expect(history.location.pathname).toBe('/myteam/spaces/sales');
    });

    it('navigates to a page result within its space', () => {
        const {history} = renderWithContext(<DocsSwitcher onClose={jest.fn()}/>, {state: stateWithPage});

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'onboard'}});
        fireEvent.click(screen.getByRole('option', {name: /Onboarding/}));

        expect(history.location.pathname).toBe('/myteam/spaces/eng/onboarding');
    });

    it('lists spaces across teams and navigates to a result in its own team', () => {
        const crossTeamState = {
            docs: {
                spaces: {
                    eng: makeSpace('eng', 'Engineering', 'team1'),
                    design: makeSpace('design', 'Design', 'team2'),
                },
                spacesInTeam: {team1: new Set(['eng']), team2: new Set(['design'])},
                pages: {},
                pagesInSpace: {},
            },
            currentTeam: makeTeam('team1', 'myteam'),
            teams: [makeTeam('team1', 'myteam'), makeTeam('team2', 'otherteam')],
        };
        const {history} = renderWithContext(<DocsSwitcher onClose={jest.fn()}/>, {state: crossTeamState});

        // Both teams' spaces are listed even though the current team is team1.
        expect(screen.getByRole('option', {name: /Engineering/})).toBeInTheDocument();
        fireEvent.click(screen.getByRole('option', {name: /Design/}));

        // Routed to the space's own team (team2 → otherteam), not the current one.
        expect(history.location.pathname).toBe('/otherteam/spaces/design');
    });
});
