// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';
import {DOCS_OVERVIEW_SEGMENT} from 'routing/paths';

import {Client4} from 'mattermost-redux/client';

import {useDocsNavigation, useTogglePageEditMode} from './navigation';

import {renderWithContext} from '../../tests/react_testing_utils';

function Probe() {
    const {teamName, spaceId, pageId, isDraft, isImport, isEditing, goToEditDraft, goToEditPage} = useDocsNavigation();
    return (
        <ul>
            <li data-testid='team'>{teamName}</li>
            <li data-testid='space'>{spaceId ?? ''}</li>
            <li data-testid='page'>{pageId ?? ''}</li>
            <li data-testid='draft'>{String(isDraft)}</li>
            <li data-testid='import'>{String(isImport)}</li>
            <li data-testid='edit'>{String(isEditing)}</li>
            <li>
                <button
                    data-testid='go-edit-draft'
                    onClick={() => goToEditDraft('space1', 'pageX')}
                />
            </li>
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
        expect(renderAt('/myteam/spaces/space1/_drafts/pageX')).toEqual({team: 'myteam', space: 'space1', page: 'pageX', draft: 'true', import: 'false'});
    });

    it('does not treat a page literally reached via a non-draft segment as a draft', () => {
        expect(renderAt('/myteam/spaces/space1/pageX').draft).toBe('false');
    });

    // The import segment sits where a space or page id would, so the only thing keeping these apart is match
    // order. Without it an import URL parses as a Space whose id happens to be the keyword, and the wizard never
    // renders.
    it('parses an import into a new Space without mistaking the keyword for a Space', () => {
        expect(renderAt('/myteam/spaces/_import')).toEqual({
            team: 'myteam', space: '', page: '', draft: 'false', import: 'true',
        });
    });

    it('parses an import into an existing Space without mistaking the keyword for a page', () => {
        expect(renderAt('/myteam/spaces/space1/_import')).toEqual({
            team: 'myteam', space: 'space1', page: '', draft: 'false', import: 'true',
        });
    });

    // The leading underscore is why the keyword costs nothing: no slug can start with one, so content named
    // "import" is still content. A bare keyword would have hidden this Space, and a page of that name in every
    // Space, with nothing validating either against it.
    it('leaves a Space named like the keyword reachable', () => {
        expect(renderAt('/myteam/spaces/import')).toEqual({
            team: 'myteam', space: 'import', page: '', draft: 'false', import: 'false',
        });
    });

    it('leaves a page named like the keyword reachable', () => {
        expect(renderAt('/myteam/spaces/space1/import')).toEqual({
            team: 'myteam', space: 'space1', page: 'import', draft: 'false', import: 'false',
        });
    });

    it('falls back to the current team when the URL is outside Docs', () => {
        expect(renderAt('/')).toEqual({team: 'myteam', space: '', page: '', draft: 'false', import: 'false'});
    });
});

function PathsProbe({absolute = false}: {absolute?: boolean}) {
    const {paths} = useDocsNavigation({absolute});
    return (
        <div data-testid='paths'>
            {[
                paths.home(),
                paths.space('space1'),
                paths.page('space1', 'pageX'),
                paths.overview('space1'),
                paths.draft('space1', 'pageX'),
                paths.import(),
                paths.import('space1'),
                paths.to('space1', 'pageX'),
            ].join('|')}
        </div>
    );
}

const pathsAt = (absolute = false) => {
    renderWithContext(<PathsProbe absolute={absolute}/>, {
        route: '/myteam/spaces',
        state: {currentTeam: {id: 't1', name: 'myteam'}},
    });
    return screen.getByTestId('paths').textContent?.split('|');
};

describe('useDocsNavigation paths', () => {
    const relativePaths = [
        '/myteam/spaces',
        '/myteam/spaces/space1',
        '/myteam/spaces/space1/pageX',
        '/myteam/spaces/space1/_overview',
        '/myteam/spaces/space1/_drafts/pageX',
        '/myteam/spaces/_import',
        '/myteam/spaces/space1/_import',
        '/myteam/spaces/space1/pageX',
    ];

    it('returns relative paths by default', () => {
        expect(pathsAt()).toEqual(relativePaths);
    });

    it('includes the Mattermost site URL when requested', () => {
        const previousUrl = Client4.getUrl();
        Client4.setUrl('https://mattermost.test/mattermost');
        try {
            expect(pathsAt(true)).toEqual(relativePaths.map((path) => `https://mattermost.test/mattermost${path}`));
        } finally {
            Client4.setUrl(previousUrl);
        }
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
        expect(editStateAt(`/myteam/spaces/space1/${DOCS_OVERVIEW_SEGMENT}?edit=1`)).toBe('false');
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

describe('useDocsNavigation goToEditDraft', () => {
    it('pushes the draft edit URL', () => {
        const {history} = renderWithContext(<Probe/>, {
            route: '/myteam/spaces/space1',
            state: {currentTeam: {id: 't1', name: 'myteam'}},
        });

        fireEvent.click(screen.getByTestId('go-edit-draft'));

        expect(history.location.pathname + history.location.search).toBe('/myteam/spaces/space1/_drafts/pageX?edit=1');
    });

    it('preserves other view state while entering draft edit mode', () => {
        const {history} = renderWithContext(<Probe/>, {
            route: '/myteam/spaces/space1?rhs=comments&rhsView=thread&fs=1',
            state: {currentTeam: {id: 't1', name: 'myteam'}},
        });

        fireEvent.click(screen.getByTestId('go-edit-draft'));

        expect(history.location.pathname + history.location.search).
            toBe('/myteam/spaces/space1/_drafts/pageX?rhs=comments&rhsView=thread&fs=1&edit=1');
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
