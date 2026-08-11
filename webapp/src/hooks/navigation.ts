// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useTeamContext} from 'hooks/team';
import {useCallback} from 'react';
import {useHistory, useRouteMatch} from 'react-router-dom';
import {
    DOCS_DRAFT_ROUTE,
    DOCS_IMPORT_ROUTE,
    DOCS_ROUTE,
    DOCS_SPACE_IMPORT_ROUTE,
    docsHomePath,
    docsPath,
    draftPath,
    importPath,
    pagePath,
    spaceImportPath,
    spacePath,
} from 'routing/paths';

type DocsRouteParams = {
    team?: string;
    spaceId?: string;
    pageId?: string;
};

// Reads the current Docs selection from the URL and provides imperative
// navigation. Path construction lives in routing/paths so the same logic backs
// React Router <Link>s; this hook binds the current team (so callers stay
// team-agnostic) and composes those builders with history for programmatic
// navigation (clicks, keyboard handlers).
export function useDocsNavigation() {
    const history = useHistory();

    // Most specific patterns first. DOCS_ROUTE treats the segment after :spaceId as
    // :pageId, so a draft URL (…/:spaceId/drafts/:pageId) would otherwise parse
    // pageId='drafts' and drop the real page id — and an import URL would parse its
    // keyword as a space or page id, which is why both import routes are matched
    // ahead of it and why those words are effectively reserved.
    const match = useRouteMatch<DocsRouteParams>([
        DOCS_DRAFT_ROUTE,
        DOCS_SPACE_IMPORT_ROUTE,
        DOCS_IMPORT_ROUTE,
        DOCS_ROUTE,
    ]);
    const {name: currentTeamName} = useTeamContext();

    const teamName = match?.params.team || currentTeamName;
    const spaceId = match?.params.spaceId;
    const pageId = match?.params.pageId;
    const isDraft = match?.path === DOCS_DRAFT_ROUTE;

    // isImport covers both shapes; spaceId then says which kind it is, since only the Space-scoped route has one.
    const isImport = match?.path === DOCS_IMPORT_ROUTE || match?.path === DOCS_SPACE_IMPORT_ROUTE;

    const goToSpace = useCallback((id: string) => history.push(spacePath(teamName, id)), [history, teamName]);
    const goToPage = useCallback((space: string, page: string) => history.push(pagePath(teamName, space, page)), [history, teamName]);
    const goToDraft = useCallback((space: string, page: string) => history.push(draftPath(teamName, space, page)), [history, teamName]);
    const goHome = useCallback(() => history.push(docsHomePath(teamName)), [history, teamName]);
    const navigate = useCallback((space: string, page?: string) => history.push(docsPath(teamName, space, page)), [history, teamName]);

    // Omitting the space imports into a new one; passing it imports into that Space.
    const goToImport = useCallback(
        (space?: string) => history.push(space ? spaceImportPath(teamName, space) : importPath(teamName)),
        [history, teamName],
    );

    // Navigate into an explicit team (the cross-team switcher routes a result to
    // its own team). Core re-initializes team context from the URL on arrival.
    const navigateInTeam = useCallback((team: string, space: string, page?: string) => history.push(docsPath(team, space, page)), [history]);

    return {
        teamName,
        spaceId,
        pageId,
        isDraft,
        isImport,
        goToSpace,
        goToPage,
        goToDraft,
        goHome,
        navigate,
        navigateInTeam,
        goToImport,

        // Re-exported for declarative use, e.g. <Link to={paths.space(id)}>.
        // Team is pre-bound so call sites match the imperative helpers.
        paths: {
            home: () => docsHomePath(teamName),
            space: (id: string) => spacePath(teamName, id),
            page: (space: string, page: string) => pagePath(teamName, space, page),
            draft: (space: string, page: string) => draftPath(teamName, space, page),
            import: (space?: string) => (space ? spaceImportPath(teamName, space) : importPath(teamName)),
            to: (space?: string, page?: string) => docsPath(teamName, space, page),
        },
    };
}
