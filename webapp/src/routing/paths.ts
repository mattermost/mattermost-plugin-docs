// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Pure URL construction for the Docs product. The single source of truth for
// the /docs URL scheme (opaque ids, no slugs/team segment). Usable by
// React Router <Link to={...}>, <Route path={DOCS_ROUTE}>/useRouteMatch, and
// the imperative useDocsNavigation hook — so navigation logic is shared rather
// than re-derived per call site.

export const DOCS_BASE_URL = '/docs';

// Route pattern for <Route>/useRouteMatch — same scheme the builders produce.
export const DOCS_ROUTE = `${DOCS_BASE_URL}/:spaceId?/:pageId?`;

const segment = (value: string): string => encodeURIComponent(value);

export const docsHomePath = (): string => DOCS_BASE_URL;

export const spacePath = (spaceId: string): string => `${DOCS_BASE_URL}/${segment(spaceId)}`;

export const pagePath = (spaceId: string, pageId: string): string => `${spacePath(spaceId)}/${segment(pageId)}`;

// Builds the path for any depth of selection (home / space / page).
export const docsPath = (spaceId?: string, pageId?: string): string => {
    if (!spaceId) {
        return docsHomePath();
    }
    return pageId ? pagePath(spaceId, pageId) : spacePath(spaceId);
};
