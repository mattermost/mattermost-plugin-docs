// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useTeamContext} from 'hooks/team';
import {useCallback} from 'react';
import {useHistory, useLocation, useRouteMatch} from 'react-router-dom';
import {DOCS_DRAFT_ROUTE, DOCS_ROUTE, DOCS_SPACE_OVERVIEW_ROUTE, EDIT_QUERY, docsHomePath, docsPath, draftPath, editPagePath, overviewPath, pagePath, spacePath} from 'routing/paths';

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

    // Specific patterns first: DOCS_ROUTE treats the segment after :spaceId as
    // :pageId, so a draft URL (…/:spaceId/drafts/:pageId) would parse
    // pageId='drafts', and an overview URL would parse pageId='overview'.
    const match = useRouteMatch<DocsRouteParams>([DOCS_DRAFT_ROUTE, DOCS_SPACE_OVERVIEW_ROUTE, DOCS_ROUTE]);
    const {name: currentTeamName} = useTeamContext();

    const teamName = match?.params.team || currentTeamName;
    const spaceId = match?.params.spaceId;
    const pageId = match?.params.pageId;
    const isDraft = match?.path === DOCS_DRAFT_ROUTE;
    const isOverview = match?.path === DOCS_SPACE_OVERVIEW_ROUTE;

    const {search} = useLocation();

    // Only a routed page can be edited, so the query alone doesn't mean edit mode:
    // ?edit=1 on a space or overview URL names nothing to edit and is ignored.
    //
    // A draft is unpublished work with no published version to read, so its route is
    // always editing and carries no query — there is no reading mode to toggle to.
    const isEditing = isDraft ||
        (Boolean(pageId) && !isOverview && new URLSearchParams(search).get(EDIT_QUERY) === '1');

    // `replace` for arrivals that follow the routed page ceasing to exist: the URL
    // being left is dead, so it shouldn't be somewhere Back can return to.
    const goToSpace = useCallback((id: string, {replace = false} = {}) => {
        const path = spacePath(teamName, id);
        if (replace) {
            history.replace(path);
        } else {
            history.push(path);
        }
    }, [history, teamName]);

    // `replace` for arrivals where the URL being left no longer names anything —
    // publishing a draft, whose draft URL ceases to exist with it.
    const goToPage = useCallback((space: string, page: string, {replace = false} = {}) => {
        const path = pagePath(teamName, space, page);
        if (replace) {
            history.replace(path);
        } else {
            history.push(path);
        }
    }, [history, teamName]);
    const goToDraft = useCallback((space: string, page: string) => history.push(draftPath(teamName, space, page)), [history, teamName]);
    const goToEditPage = useCallback((space: string, page: string) => history.push(editPagePath(teamName, space, page)), [history, teamName]);
    const goToOverview = useCallback((space: string) => history.push(overviewPath(teamName, space)), [history, teamName]);
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
        isEditing,
        isOverview,
        goToSpace,
        goToPage,
        goToDraft,
        goToEditPage,
        goToOverview,
        goHome,
        navigate,
        navigateInTeam,

        // Re-exported for declarative use, e.g. <Link to={paths.space(id)}>.
        // Team is pre-bound so call sites match the imperative helpers.
        paths: {
            home: () => docsHomePath(teamName),
            space: (id: string) => spacePath(teamName, id),
            page: (space: string, page: string) => pagePath(teamName, space, page),
            overview: (space: string) => overviewPath(teamName, space),
            draft: (space: string, page: string) => draftPath(teamName, space, page),
            to: (space?: string, page?: string) => docsPath(teamName, space, page),
        },
    };
}

/**
 * Returns a handler that moves the routed page in and out of edit mode: into
 * `?edit=1` while reading, back to the bare page path while editing. Both are
 * pushes, so Back is an exit. A no-op when no page is routed, since there is
 * nothing to edit.
 */
export function useTogglePageEditMode(spaceId: string) {
    const {pageId, isEditing, goToPage, goToEditPage} = useDocsNavigation();

    return useCallback(() => {
        if (!pageId) {
            return;
        }
        if (isEditing) {
            goToPage(spaceId, pageId);
        } else {
            goToEditPage(spaceId, pageId);
        }
    }, [isEditing, pageId, spaceId, goToPage, goToEditPage]);
}
