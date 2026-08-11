// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import * as importsClient from 'client/imports';
import React from 'react';

import type {ImportJobView} from 'types/imports';

import DocsRoot from './docs_root';

import {renderWithContext} from '../../../tests/react_testing_utils';

// The bootstrap hook fetches the team's Spaces on mount, which these tests are not about.
jest.mock('hooks/bootstrap', () => ({useBootstrapDocs: () => undefined}));

// react-hotkeys-hook ships ESM that this project's jest transform does not cover. It is mocked here rather than
// widening transformIgnorePatterns for everyone, since the shortcut it registers is not what these tests drive.
jest.mock('react-hotkeys-hook', () => ({useHotkeys: () => undefined}));

// Stand in for the ordinary content, which reads host config this harness does not build. What these tests are
// about is which of the two branches the route selects, so a marker is exactly as much as they need — and it
// makes the negative case assert that the other branch rendered, rather than that nothing did.
jest.mock('./docs_main_content', () => () => <div data-testid='main-content'/>);

const TEAM = {currentTeam: {id: 't1', name: 'myteam'}};

const noJobs = () => ({items: [], page: 0, per_page: 20, has_more: false});

const renderAt = (route: string) => renderWithContext(<DocsRoot/>, {route, state: TEAM});

describe('DocsRoot import route', () => {
    beforeEach(() => {
        jest.spyOn(importsClient, 'listImportJobs').mockResolvedValue(noJobs());
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    // The import URL is what makes the wizard usable at all: a job runs for minutes on the server, so the view
    // of it has to survive a reload and be shareable. Held in component state, it could only ever be reached by
    // whoever clicked the button, in that tab, until they navigated away.
    it('renders the wizard for a new-Space import', async () => {
        renderAt('/myteam/spaces/_import');

        expect(await screen.findByRole('region', {name: 'Import from Confluence'})).toBeInTheDocument();
        expect(await screen.findByLabelText('Confluence export bundle')).toBeInTheDocument();
    });

    // Importing into an existing Space is the same wizard pointed somewhere else, and the Space comes from the
    // URL — so the target cannot drift from what the address bar says.
    it('targets the Space in the URL when importing into an existing one', async () => {
        const upload = jest.spyOn(importsClient, 'uploadImportBundle');
        renderAt('/myteam/spaces/space1/_import');

        expect(await screen.findByRole('region', {name: 'Import from Confluence'})).toBeInTheDocument();

        // The lookup for an import already in flight is scoped to the same target the upload would use.
        await waitFor(() => expect(importsClient.listImportJobs).toHaveBeenCalled());
        expect(upload).not.toHaveBeenCalled();
    });

    it('leaves the wizard out of the way everywhere else', async () => {
        renderAt('/myteam/spaces');

        expect(await screen.findByTestId('main-content')).toBeInTheDocument();
        expect(screen.queryByRole('region', {name: 'Import from Confluence'})).not.toBeInTheDocument();

        // Nor does it look for an import in flight on a page that is not about importing.
        expect(importsClient.listImportJobs).not.toHaveBeenCalled();
    });

    // The whole path a user takes, in one test: the entry point in the sidebar, the URL it goes to, and the
    // wizard arriving there. Each half was already covered; what this pins is that they are connected.
    it('reaches the wizard from the sidebar entry', async () => {
        const {history} = renderAt('/myteam/spaces');

        expect(await screen.findByTestId('main-content')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', {name: 'Import from Confluence'}));

        expect(history.location.pathname).toBe('/myteam/spaces/_import');
        expect(await screen.findByRole('region', {name: 'Import from Confluence'})).toBeInTheDocument();
    });

    // Finishing an import into a new Space and being returned to an empty product home is the wrong ending: the
    // user spent minutes on a Space that now exists and is nowhere in sight. Done goes to it.
    //
    // The path is walked rather than jumped to, because arriving at the import URL fresh does *not* show a
    // finished job — resuming deliberately ignores those, since a completed import is history and someone asking
    // to import again means a new one. So the result step is only reachable the way a user reaches it.
    it('lands on the Space a finished import created', async () => {
        const finished = {
            id: 'job1',
            state: 'completed_with_issues',
            phase: '',
            progress: {phase: '', current: 3, total: 3},
            target: {kind: 'new', team_id: 't1', space_id: 'newspace', existed: false},
            bundle: {
                version: 2,
                source: {organization_id: '', space_key: 'DOCS', space_name: 'Docs'},
                space_defaults: {title: 'Docs', description: ''},
                counts: {
                    pages: 3,
                    comments: 0,
                    attachments: 0,
                    restricted_manifest_total: 0,
                    restricted_emitted_pages: 0,
                    restricted_manifest_only: 0,
                },
            },
            source_candidates: [],
            required_acknowledgements: [],
            create_at: 1,
            update_at: 2,
            finished_at: 3,
        } as unknown as ImportJobView;

        jest.spyOn(importsClient, 'uploadImportBundle').mockResolvedValue({...finished, state: 'queued_preflight'});
        jest.spyOn(importsClient, 'getImportJob').mockResolvedValue(finished);

        const {history} = renderAt('/myteam/spaces/_import');

        const input = await screen.findByLabelText('Confluence export bundle');
        await act(async () => {
            fireEvent.change(input, {target: {files: [new File([new Uint8Array([1])], 'b.zip', {type: 'application/zip'})]}});
        });
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Upload and inspect'}));
        });

        fireEvent.click(await screen.findByRole('button', {name: 'Done'}));
        expect(history.location.pathname).toBe('/myteam/spaces/newspace');
    });

    // The reserved segment reaches the wizard, and — because no space id or page id can start with an
    // underscore — it does so without taking a name away from anyone. See routing/paths.test for the rule
    // itself; this is the half of it that shows the route actually resolves.
    it('treats the reserved segment as the import route, not as a Space of that name', async () => {
        renderAt('/myteam/spaces/_import');

        expect(await screen.findByRole('region', {name: 'Import from Confluence'})).toBeInTheDocument();
        expect(screen.queryByTestId('main-content')).not.toBeInTheDocument();
    });
});
