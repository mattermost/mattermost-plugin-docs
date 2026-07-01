// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';
import {useHistory, useRouteMatch} from 'react-router-dom';
import {DOCS_ROUTE, docsHomePath, docsPath, pagePath, spacePath} from 'routing/paths';

type DocsRouteParams = {
    spaceId?: string;
    pageId?: string;
};

// Reads the current Docs selection from the URL and provides imperative
// navigation. Path construction lives in routing/paths so the same logic backs
// React Router <Link>s; this hook just composes those builders with history for
// programmatic navigation (clicks, keyboard handlers).
export function useDocsNavigation() {
    const history = useHistory();
    const match = useRouteMatch<DocsRouteParams>(DOCS_ROUTE);
    const spaceId = match?.params.spaceId;
    const pageId = match?.params.pageId;

    const goToSpace = useCallback((id: string) => history.push(spacePath(id)), [history]);
    const goToPage = useCallback((space: string, page: string) => history.push(pagePath(space, page)), [history]);
    const goHome = useCallback(() => history.push(docsHomePath()), [history]);
    const navigate = useCallback((space: string, page?: string) => history.push(docsPath(space, page)), [history]);

    return {
        spaceId,
        pageId,
        goToSpace,
        goToPage,
        goHome,
        navigate,

        // Re-exported for declarative use, e.g. <Link to={paths.space(id)}>.
        paths: {home: docsHomePath, space: spacePath, page: pagePath, to: docsPath},
    };
}
