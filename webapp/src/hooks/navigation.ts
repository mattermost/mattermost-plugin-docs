// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useTeamContext} from 'hooks/team';
import {useCallback} from 'react';
import {useHistory, useRouteMatch} from 'react-router-dom';
import {DOCS_DRAFT_ROUTE, DOCS_ROUTE, docsHomePath, docsPath, draftPath, pagePath, spacePath} from 'routing/paths';

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

    // Draft route first: it's the more specific pattern. DOCS_ROUTE treats the
    // segment after :spaceId as :pageId, so a draft URL (…/:spaceId/drafts/:pageId)
    // would otherwise parse pageId='drafts' and drop the real page id.
    const match = useRouteMatch<DocsRouteParams>([DOCS_DRAFT_ROUTE, DOCS_ROUTE]);
    const {name: currentTeamName} = useTeamContext();

    const teamName = match?.params.team || currentTeamName;
    const spaceId = match?.params.spaceId;
    const pageId = match?.params.pageId;
    const isDraft = match?.path === DOCS_DRAFT_ROUTE;

    const goToSpace = useCallback((id: string) => history.push(spacePath(teamName, id)), [history, teamName]);
    const goToPage = useCallback((space: string, page: string) => history.push(pagePath(teamName, space, page)), [history, teamName]);
    const goToDraft = useCallback((space: string, page: string) => history.push(draftPath(teamName, space, page)), [history, teamName]);
    const goHome = useCallback(() => history.push(docsHomePath(teamName)), [history, teamName]);
    const navigate = useCallback((space: string, page?: string) => history.push(docsPath(teamName, space, page)), [history, teamName]);

    // Navigate into an explicit team (the cross-team switcher routes a result to
    // its own team). Core re-initializes team context from the URL on arrival.
    const navigateInTeam = useCallback((team: string, space: string, page?: string) => history.push(docsPath(team, space, page)), [history]);

    return {
        teamName,
        spaceId,
        pageId,
        isDraft,
        goToSpace,
        goToPage,
        goToDraft,
        goHome,
        navigate,
        navigateInTeam,

        // Re-exported for declarative use, e.g. <Link to={paths.space(id)}>.
        // Team is pre-bound so call sites match the imperative helpers.
        paths: {
            home: () => docsHomePath(teamName),
            space: (id: string) => spacePath(teamName, id),
            page: (space: string, page: string) => pagePath(teamName, space, page),
            draft: (space: string, page: string) => draftPath(teamName, space, page),
            to: (space?: string, page?: string) => docsPath(teamName, space, page),
        },
    };
}
