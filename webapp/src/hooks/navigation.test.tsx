// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import {useDocsNavigation} from './navigation';

import {renderWithContext} from '../../tests/react_testing_utils';

function Probe() {
    const {teamName, spaceId, pageId, isDraft} = useDocsNavigation();
    return (
        <ul>
            <li data-testid='team'>{teamName}</li>
            <li data-testid='space'>{spaceId ?? ''}</li>
            <li data-testid='page'>{pageId ?? ''}</li>
            <li data-testid='draft'>{String(isDraft)}</li>
        </ul>
    );
}

function renderAt(route: string) {
    renderWithContext(<Probe/>, {route, state: {currentTeam: {id: 't1', name: 'myteam'}}});
    return {
        team: screen.getByTestId('team').textContent,
        space: screen.getByTestId('space').textContent,
        page: screen.getByTestId('page').textContent,
        draft: screen.getByTestId('draft').textContent,
    };
}

describe('useDocsNavigation URL parsing', () => {
    it('parses the product home', () => {
        expect(renderAt('/myteam/spaces')).toEqual({team: 'myteam', space: '', page: '', draft: 'false'});
    });

    it('parses a space home', () => {
        expect(renderAt('/myteam/spaces/space1')).toEqual({team: 'myteam', space: 'space1', page: '', draft: 'false'});
    });

    it('parses a page', () => {
        expect(renderAt('/myteam/spaces/space1/pageX')).toEqual({team: 'myteam', space: 'space1', page: 'pageX', draft: 'false'});
    });

    it('parses a draft without dropping the page id', () => {
        expect(renderAt('/myteam/spaces/space1/drafts/pageX')).toEqual({team: 'myteam', space: 'space1', page: 'pageX', draft: 'true'});
    });

    it('does not treat a page literally reached via a non-draft segment as a draft', () => {
        expect(renderAt('/myteam/spaces/space1/pageX').draft).toBe('false');
    });

    it('falls back to the current team when the URL is outside Docs', () => {
        expect(renderAt('/')).toEqual({team: 'myteam', space: '', page: '', draft: 'false'});
    });
});
