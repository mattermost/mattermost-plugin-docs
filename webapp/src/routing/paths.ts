// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Pure URL construction for the Docs product. The single source of truth for
// the /spaces URL scheme (opaque ids, no slugs/team segment). Usable by
// React Router <Link to={...}>, <Route path={DOCS_ROUTE}>/useRouteMatch, and
// the imperative useDocsNavigation hook — so navigation logic is shared rather
// than re-derived per call site.
//
// Canonical shapes (spec: /{teamName}/spaces/{spaceId}/{pageId}):
//   /spaces                             ← product home
//   /spaces/:spaceId                    ← space home
//   /spaces/:spaceId/:pageId            ← page
//   /spaces/:spaceId/drafts/:pageId     ← per-user draft
//
// TODO: the spec requires a /{teamName} prefix matching every other MM
// permalink. registerProduct() only supports global baseURLs today; team
// prefix needs a core platform change or registerNeedsTeamRoute().

export const DOCS_BASE_URL = '/spaces';

// 26-char lowercase-alphanumeric — the platform's standard opaque id format.
const MM_ID = '[a-z0-9]{26}';

// Route patterns for <Route>/useRouteMatch.
export const DOCS_ROUTE = `${DOCS_BASE_URL}/:spaceId(${MM_ID})?/:pageId(${MM_ID})?`;
export const DOCS_DRAFT_ROUTE = `${DOCS_BASE_URL}/:spaceId(${MM_ID})/drafts/:pageId(${MM_ID})`;

const segment = (value: string): string => encodeURIComponent(value);

export const docsHomePath = (): string => DOCS_BASE_URL;

export const spacePath = (spaceId: string): string => `${DOCS_BASE_URL}/${segment(spaceId)}`;

export const pagePath = (spaceId: string, pageId: string): string => `${spacePath(spaceId)}/${segment(pageId)}`;

export const draftPath = (spaceId: string, pageId: string): string => `${spacePath(spaceId)}/drafts/${segment(pageId)}`;

export const docsPath = (spaceId?: string, pageId?: string): string => {
    if (!spaceId) {
        return docsHomePath();
    }
    return pageId ? pagePath(spaceId, pageId) : spacePath(spaceId);
};
