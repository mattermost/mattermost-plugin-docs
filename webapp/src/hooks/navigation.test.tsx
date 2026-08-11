// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import {useDocsNavigation} from './navigation';

import {renderWithContext} from '../../tests/react_testing_utils';

function Probe() {
    const {teamName, spaceId, pageId, isDraft, isImport, paths} = useDocsNavigation();
    return (
        <ul>
            <li data-testid='team'>{teamName}</li>
            <li data-testid='space'>{spaceId ?? ''}</li>
            <li data-testid='page'>{pageId ?? ''}</li>
            <li data-testid='draft'>{String(isDraft)}</li>
            <li data-testid='import'>{String(isImport)}</li>
            <li data-testid='import-path'>{paths.import()}</li>
            <li data-testid='space-import-path'>{paths.import('space1')}</li>
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
        import: screen.getByTestId('import').textContent,
    };
}

describe('useDocsNavigation URL parsing', () => {
    it('parses the product home', () => {
        expect(renderAt('/myteam/spaces')).toEqual({team: 'myteam', space: '', page: '', draft: 'false', import: 'false'});
    });

    it('parses a space home', () => {
        expect(renderAt('/myteam/spaces/space1')).toEqual({team: 'myteam', space: 'space1', page: '', draft: 'false', import: 'false'});
    });

    it('parses a page', () => {
        expect(renderAt('/myteam/spaces/space1/pageX')).toEqual({team: 'myteam', space: 'space1', page: 'pageX', draft: 'false', import: 'false'});
    });

    it('parses a draft without dropping the page id', () => {
        expect(renderAt('/myteam/spaces/space1/drafts/pageX')).toEqual({team: 'myteam', space: 'space1', page: 'pageX', draft: 'true', import: 'false'});
    });

    it('does not treat a page literally reached via a non-draft segment as a draft', () => {
        expect(renderAt('/myteam/spaces/space1/pageX').draft).toBe('false');
    });

    // The import keyword sits where a space or page id would, and both id patterns match it, so the only thing
    // keeping these apart is match order. Without it an import URL parses as a Space called "import" and the
    // wizard never renders.
    it('parses an import into a new Space without mistaking the keyword for a Space', () => {
        expect(renderAt('/myteam/spaces/import')).toEqual({
            team: 'myteam', space: '', page: '', draft: 'false', import: 'true',
        });
    });

    it('parses an import into an existing Space without mistaking the keyword for a page', () => {
        expect(renderAt('/myteam/spaces/space1/import')).toEqual({
            team: 'myteam', space: 'space1', page: '', draft: 'false', import: 'true',
        });
    });

    it('builds both import paths', () => {
        renderWithContext(<Probe/>, {route: '/myteam/spaces', state: {currentTeam: {id: 't1', name: 'myteam'}}});
        expect(screen.getByTestId('import-path')).toHaveTextContent('/myteam/spaces/import');
        expect(screen.getByTestId('space-import-path')).toHaveTextContent('/myteam/spaces/space1/import');
    });

    it('falls back to the current team when the URL is outside Docs', () => {
        expect(renderAt('/')).toEqual({team: 'myteam', space: '', page: '', draft: 'false', import: 'false'});
    });
});
