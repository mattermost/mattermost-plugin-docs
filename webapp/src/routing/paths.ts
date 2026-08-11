// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Pure URL construction for the Docs product. The single source of truth for
// the team-scoped /spaces URL scheme (opaque ids, no slugs). Usable by React
// Router <Link to={...}>, <Route path={DOCS_ROUTE}>/useRouteMatch, and the
// imperative useDocsNavigation hook — so navigation logic is shared rather than
// re-derived per call site.
//
// Canonical shapes (spec: /{teamName}/spaces/{spaceId}/{pageId}):
//   /{team}/spaces                           ← product home
//   /{team}/spaces/:spaceId                  ← space home
//   /{team}/spaces/:spaceId/:pageId          ← page
//   /{team}/spaces/:spaceId/_drafts/:pageId  ← per-user draft
//   /{team}/spaces/_import                   ← import into a new space
//   /{team}/spaces/:spaceId/_import          ← import into that space
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

// Segments that name something other than content. Every one begins with an
// underscore, and that is the whole rule.
//
// A space id or page id in the URL can never start with one (SPACE_OR_PAGE_ID
// above, and SLUG_PATTERN in validation/space_schema), so these segments cannot
// collide with anything a user names — no reserved-word list to keep, nothing to
// enforce at creation time, and no migration if custom slugs arrive later.
//
// The alternative is a bare word matched ahead of the id routes, and its safety
// is circumstantial rather than structural: it holds only while no user-chosen
// string can reach that position. `/{space}/drafts/{page}` got away with it by
// arity — no content route has three segments after `spaces`, so a page named
// `drafts` stayed reachable — but a two-segment `/{space}/overview` sits exactly
// where a page id goes and hides any page addressed that way. Underscoring both
// makes the question stop needing to be asked.
//
// RESERVED_SEGMENTS exists so that stays true: paths.test.ts asserts every entry
// is unmatchable as an id, which is a mechanical check on the next one added.
export const DOCS_DRAFTS_SEGMENT = '_drafts';

// Where the Confluence import wizard lives. An import is in the URL rather than in
// component state because it outlives any one view of it: a job runs for minutes on
// the server, so the page showing it has to survive a reload and be linkable —
// "here is the import you asked about" is a URL, not a button someone else has to
// find.
export const DOCS_IMPORT_SEGMENT = '_import';

export const RESERVED_SEGMENTS = [DOCS_DRAFTS_SEGMENT, DOCS_IMPORT_SEGMENT] as const;

// The id pattern, anchored, for asserting that a reserved segment cannot be one.
export const SPACE_OR_PAGE_ID_PATTERN = new RegExp(`^${SPACE_OR_PAGE_ID}$`);

// Route patterns for <Route>/useRouteMatch, matched against the full
// team-scoped pathname the product mounts under.
export const DOCS_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})?/:pageId(${SPACE_OR_PAGE_ID})?`;
export const DOCS_DRAFT_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/${DOCS_DRAFTS_SEGMENT}/:pageId(${SPACE_OR_PAGE_ID})`;
export const DOCS_IMPORT_ROUTE = `/:team/${DOCS_KEYWORD}/${DOCS_IMPORT_SEGMENT}`;
export const DOCS_SPACE_IMPORT_ROUTE = `/:team/${DOCS_KEYWORD}/:spaceId(${SPACE_OR_PAGE_ID})/${DOCS_IMPORT_SEGMENT}`;

const segment = (value: string): string => encodeURIComponent(value);

const teamRoot = (teamName: string): string => `/${segment(teamName)}/${DOCS_KEYWORD}`;

export const docsHomePath = (teamName: string): string => teamRoot(teamName);

export const spacePath = (teamName: string, spaceId: string): string => `${teamRoot(teamName)}/${segment(spaceId)}`;

export const pagePath = (teamName: string, spaceId: string, pageId: string): string => `${spacePath(teamName, spaceId)}/${segment(pageId)}`;

export const draftPath = (teamName: string, spaceId: string, pageId: string): string =>
    `${spacePath(teamName, spaceId)}/${DOCS_DRAFTS_SEGMENT}/${segment(pageId)}`;

// Importing into a new Space is team-scoped; importing into an existing one hangs off that Space, because that
// is what the import is about and where the user was standing when they asked for it.
export const importPath = (teamName: string): string => `${teamRoot(teamName)}/${DOCS_IMPORT_SEGMENT}`;

export const spaceImportPath = (teamName: string, spaceId: string): string =>
    `${spacePath(teamName, spaceId)}/${DOCS_IMPORT_SEGMENT}`;

export const docsPath = (teamName: string, spaceId?: string, pageId?: string): string => {
    if (!spaceId) {
        return docsHomePath(teamName);
    }
    return pageId ? pagePath(teamName, spaceId, pageId) : spacePath(teamName, spaceId);
};
