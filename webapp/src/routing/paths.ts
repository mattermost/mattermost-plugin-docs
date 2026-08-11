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

// The segment that opens the Confluence import wizard, either for a new Space
// (/{team}/spaces/_import) or into an existing one
// (/{team}/spaces/{spaceId}/_import).
//
// An import lives in the URL rather than in component state because it outlives
// any one view of it: a job runs for minutes on the server, so the page showing
// it has to survive a reload and be linkable — "here is the import you asked
// about" is a URL, not a button someone else has to find.
//
// The leading underscore is what keeps it from costing anything. Space slugs and
// page ids must start with a letter or digit (SLUG_PATTERN, and SPACE_OR_PAGE_ID
// below), so no user-chosen name can ever produce this segment — where a bare
// `import` would have shadowed a Space called "import", and a page called
// "import" inside every Space, with nothing validating either against it.
//
// `drafts` looks like a precedent for taking that risk but is not one: it only
// claims a three-segment shape (/{spaceId}/drafts/{pageId}) that no content
// route uses, so a page named `drafts` stays perfectly reachable.
export const DOCS_IMPORT_KEYWORD = '_import';

// Route patterns for <Route>/useRouteMatch, matched against the full
// team-scoped pathname the product mounts under.
export const DOCS_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})?/:pageId(${SPACE_OR_PAGE_ID})?`;
export const DOCS_DRAFT_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/drafts/:pageId(${SPACE_OR_PAGE_ID})`;
export const DOCS_IMPORT_ROUTE = `/:team/${DOCS_KEYWORD}/${DOCS_IMPORT_KEYWORD}`;
export const DOCS_SPACE_IMPORT_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/${DOCS_IMPORT_KEYWORD}`;

const segment = (value: string): string => encodeURIComponent(value);

const teamRoot = (teamName: string): string => `/${segment(teamName)}/${DOCS_KEYWORD}`;

export const docsHomePath = (teamName: string): string => teamRoot(teamName);

export const spacePath = (teamName: string, spaceId: string): string => `${teamRoot(teamName)}/${segment(spaceId)}`;

export const pagePath = (teamName: string, spaceId: string, pageId: string): string => `${spacePath(teamName, spaceId)}/${segment(pageId)}`;

export const draftPath = (teamName: string, spaceId: string, pageId: string): string => `${spacePath(teamName, spaceId)}/drafts/${segment(pageId)}`;

// Importing into a new Space is team-scoped; importing into an existing one hangs off that Space, because that
// is what the import is about and where the user was standing when they asked for it.
export const importPath = (teamName: string): string => `${teamRoot(teamName)}/${DOCS_IMPORT_KEYWORD}`;

export const spaceImportPath = (teamName: string, spaceId: string): string => `${spacePath(teamName, spaceId)}/${DOCS_IMPORT_KEYWORD}`;

export const docsPath = (teamName: string, spaceId?: string, pageId?: string): string => {
    if (!spaceId) {
        return docsHomePath(teamName);
    }
    return pageId ? pagePath(teamName, spaceId, pageId) : spacePath(teamName, spaceId);
};
