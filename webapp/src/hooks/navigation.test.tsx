// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {useDocsNavigation, useTogglePageEditMode} from './navigation';

import {renderWithContext} from '../../tests/react_testing_utils';

function Probe() {
    const {teamName, spaceId, pageId, isDraft, isEditing, goToEditPage} = useDocsNavigation();
    return (
        <ul>
            <li data-testid='team'>{teamName}</li>
            <li data-testid='space'>{spaceId ?? ''}</li>
            <li data-testid='page'>{pageId ?? ''}</li>
            <li data-testid='draft'>{String(isDraft)}</li>
            <li data-testid='edit'>{String(isEditing)}</li>
            <li>
                <button
                    data-testid='go-edit'
                    onClick={() => goToEditPage('space1', 'pageX')}
                />
            </li>
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

const editStateAt = (route: string) => {
    renderWithContext(<Probe/>, {route, state: {currentTeam: {id: 't1', name: 'myteam'}}});
    return screen.getByTestId('edit').textContent;
};

describe('useDocsNavigation edit mode', () => {
    it('is editing when a page URL carries the edit query', () => {
        expect(editStateAt('/myteam/spaces/space1/pageX?edit=1')).toBe('true');
    });

    it('is not editing without the query', () => {
        expect(editStateAt('/myteam/spaces/space1/pageX')).toBe('false');
    });

    it('is not editing on a space URL, which has no page to edit', () => {
        expect(editStateAt('/myteam/spaces/space1?edit=1')).toBe('false');
    });

    it('is not editing on the overview URL, which has no page to edit', () => {
        expect(editStateAt('/myteam/spaces/space1/overview?edit=1')).toBe('false');
    });

    it('ignores an edit query with any other value', () => {
        expect(editStateAt('/myteam/spaces/space1/pageX?edit=0')).toBe('false');
    });
});

describe('useDocsNavigation goToEditPage', () => {
    it('pushes the edit URL so back exits edit mode', () => {
        const {history} = renderWithContext(<Probe/>, {
            route: '/myteam/spaces/space1/pageX',
            state: {currentTeam: {id: 't1', name: 'myteam'}},
        });

        fireEvent.click(screen.getByTestId('go-edit'));

        expect(history.location.pathname + history.location.search).toBe('/myteam/spaces/space1/pageX?edit=1');
        expect(history.length).toBe(2);
    });
});

function ToggleProbe({spaceId}: {spaceId: string}) {
    const toggleEdit = useTogglePageEditMode(spaceId);
    return (
        <button
            data-testid='toggle-edit'
            onClick={toggleEdit}
        />
    );
}

const toggleFrom = (route: string) => {
    const {history} = renderWithContext(<ToggleProbe spaceId='space1'/>, {
        route,
        state: {currentTeam: {id: 't1', name: 'myteam'}},
    });

    fireEvent.click(screen.getByTestId('toggle-edit'));

    return {history, url: history.location.pathname + history.location.search};
};

describe('useTogglePageEditMode', () => {
    it('enters edit mode from a page URL', () => {
        expect(toggleFrom('/myteam/spaces/space1/pageX').url).toBe('/myteam/spaces/space1/pageX?edit=1');
    });

    it('leaves edit mode from an edit URL', () => {
        expect(toggleFrom('/myteam/spaces/space1/pageX?edit=1').url).toBe('/myteam/spaces/space1/pageX');
    });

    it('does nothing on a space URL, where no page is routed', () => {
        const {history, url} = toggleFrom('/myteam/spaces/space1');

        expect(url).toBe('/myteam/spaces/space1');
        expect(history.length).toBe(1);
    });
});
