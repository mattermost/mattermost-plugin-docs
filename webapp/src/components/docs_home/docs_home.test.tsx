// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import type {SpaceSummary} from 'types/docs';

import DocsHome from './docs_home';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockGoToSpace = jest.fn();
let mockSummaries: SpaceSummary[] = [];

jest.mock('hooks/user', () => ({useCurrentUser: () => ({name: 'Sam'})}));
jest.mock('hooks/navigation', () => ({useDocsNavigation: () => ({goToSpace: mockGoToSpace})}));
jest.mock('hooks/spaces', () => ({useRecentSpaceSummaries: () => mockSummaries}));

const TEAM = {id: 'team1', name: 'team-1'};

// An entry for the team means "this team's spaces are loaded" (see
// areSpacesLoadedForCurrentTeam); without it Home can't tell an empty team from
// one whose spaces are still in flight.
const spacesLoaded = {currentTeam: TEAM, docs: {spacesInTeam: {[TEAM.id]: new Set<string>()}}};

describe('DocsHome', () => {
    beforeEach(() => {
        mockSummaries = [];
    });

    it('renders only the header until the team spaces have loaded', () => {
        renderWithContext(<DocsHome onCreateSpace={jest.fn()}/>, {state: {currentTeam: TEAM}});

        expect(screen.getByRole('button', {name: 'New Space'})).toBeInTheDocument();
        expect(screen.queryByText('Welcome to Docs.')).not.toBeInTheDocument();
    });

    it('shows the welcome hero and wires the create CTAs when there are no spaces', () => {
        const onCreateSpace = jest.fn();
        renderWithContext(<DocsHome onCreateSpace={onCreateSpace}/>, {state: spacesLoaded});

        expect(screen.getByText('Welcome to Docs.')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', {name: /Create a space/}));
        fireEvent.click(screen.getByRole('button', {name: 'New Space'}));
        expect(onCreateSpace).toHaveBeenCalledTimes(2);
    });

    it('invokes onBrowseSpaces from the empty-state CTA', () => {
        const onBrowseSpaces = jest.fn();
        renderWithContext(
            <DocsHome
                onCreateSpace={jest.fn()}
                onBrowseSpaces={onBrowseSpaces}
            />,
            {state: spacesLoaded},
        );

        fireEvent.click(screen.getByRole('button', {name: 'Browse spaces'}));
        expect(onBrowseSpaces).toHaveBeenCalledTimes(1);
    });

    it('greets the user and lists recent spaces, navigating on click', () => {
        mockSummaries = [{space: {...makeSpace('eng', 'Engineering'), icon: '📘'}, pageCount: 3, lastViewedAt: Date.now() - (12 * 60 * 1000)}];

        renderWithContext(<DocsHome onCreateSpace={jest.fn()}/>, {state: spacesLoaded});

        expect(screen.getByText(/Good (morning|afternoon|evening), Sam\./)).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', {name: /Engineering/}));
        expect(mockGoToSpace).toHaveBeenCalledWith('eng');
    });
});
