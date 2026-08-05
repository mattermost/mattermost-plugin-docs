// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Pure URL construction for the Docs product. The single source of truth for
// the team-scoped /spaces URL scheme (opaque ids, no slugs). Usable by React
// Router <Link to={...}>, <Route path={DOCS_ROUTE}>/useRouteMatch, and the
// imperative useDocsNavigation hook — so navigation logic is shared rather than
// re-derived per call site.
//
// Canonical shapes (spec: /{teamName}/spaces/{spaceId}/{pageId}):
//   /{team}/spaces                          ← product home
//   /{team}/spaces/:spaceId                 ← space home
//   /{team}/spaces/:spaceId/:pageId         ← page
//   /{team}/spaces/:spaceId/drafts/:pageId  ← per-user draft
//
// registerProduct() is called with isTeamScoped: true, so core mounts the
// product at `/:team${DOCS_BASE_URL}` and initializes team context from the URL
// (MM-69728). Both DOCS_BASE_URL and DOCS_SWITCHER_LINK_URL are the plain
// keyword path; core prepends the current team to the switcher link.

export const DOCS_KEYWORD = 'spaces';

export const DOCS_BASE_URL = `/${DOCS_KEYWORD}`;
export const DOCS_SWITCHER_LINK_URL = `/${DOCS_KEYWORD}`;

// A space/page id in the URL is the platform's 26-char opaque id (the canonical,
// ID-based form) OR a human-readable custom slug. Both reduce to: start with a
// lowercase alphanumeric, then lowercase alphanumerics, dashes, or underscores.
const SPACE_OR_PAGE_ID = '[a-z0-9][a-z0-9\\-_]*';

// Route patterns for <Route>/useRouteMatch, matched against the full
// team-scoped pathname the product mounts under.
export const DOCS_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})?/:pageId(${SPACE_OR_PAGE_ID})?`;
export const DOCS_DRAFT_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/drafts/:pageId(${SPACE_OR_PAGE_ID})`;

// A routed space, with or without a page. Distinct from DOCS_ROUTE (whose params
// are both optional, for parsing the current location) so it can be matched on
// its own in a <Switch> — the product home is then the fallthrough.
export const DOCS_SPACE_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/:pageId(${SPACE_OR_PAGE_ID})?`;

// The space's front door, addressed explicitly. A bare space URL redirects to the
// space's default page when one is set, so "show me the overview" needs a URL of
// its own — otherwise the redirect would always win. Like `drafts`, this segment
// must be matched before the generic page route, which would otherwise read it as
// a page id.
export const DOCS_OVERVIEW_SEGMENT = 'overview';
export const DOCS_SPACE_OVERVIEW_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/${DOCS_OVERVIEW_SEGMENT}`;

const segment = (value: string): string => encodeURIComponent(value);

const teamRoot = (teamName: string): string => `/${segment(teamName)}/${DOCS_KEYWORD}`;

export const docsHomePath = (teamName: string): string => teamRoot(teamName);

export const spacePath = (teamName: string, spaceId: string): string => `${teamRoot(teamName)}/${segment(spaceId)}`;

export const pagePath = (teamName: string, spaceId: string, pageId: string): string => `${spacePath(teamName, spaceId)}/${segment(pageId)}`;

export const overviewPath = (teamName: string, spaceId: string): string => `${spacePath(teamName, spaceId)}/${DOCS_OVERVIEW_SEGMENT}`;

export const draftPath = (teamName: string, spaceId: string, pageId: string): string => `${spacePath(teamName, spaceId)}/drafts/${segment(pageId)}`;

// Edit mode is a query on the page URL, not a path segment: the page id stays
// canonical in the path, and /drafts/:pageId is left to mean a page with no
// published version yet.
export const EDIT_QUERY = 'edit';

// Which right-hand panel is open, and which of its screens. A query for the same
// reason edit mode is one: the panel is a view of the routed page rather than a
// place of its own, so the path keeps naming the page.
export const RHS_QUERY = 'rhs';
export const RHS_VIEW_QUERY = 'rhsView';

export const editPagePath = (teamName: string, spaceId: string, pageId: string): string =>
    `${pagePath(teamName, spaceId, pageId)}?${EDIT_QUERY}=1`;

export const docsPath = (teamName: string, spaceId?: string, pageId?: string): string => {
    if (!spaceId) {
        return docsHomePath(teamName);
    }
    return pageId ? pagePath(teamName, spaceId, pageId) : spacePath(teamName, spaceId);
};
