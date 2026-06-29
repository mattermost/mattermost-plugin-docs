// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

import type {DocsDataSource, DocsSearchResults} from './docs_data_source';
import {pages, recentPageIds, recentSpaceIds, spaces, teamName} from './fixtures';

const spacesById = new Map<string, Space>(spaces.map((s) => [s.id, s]));
const pagesById = new Map<string, Page>(pages.map((p) => [p.id, p]));

export const mockDataSource: DocsDataSource = {
    getCurrentTeamName: () => teamName,
    listSpaces: () => spaces,
    getSpace: (id) => spacesById.get(id),
    listPages: () => pages,
    getRecentSpaces: () => recentSpaceIds.map((id) => spacesById.get(id)).filter((s): s is Space => Boolean(s)),
    getRecentPages: () => recentPageIds.map((id) => pagesById.get(id)).filter((p): p is Page => Boolean(p)),
    search: (query): DocsSearchResults => {
        const q = query.trim().toLowerCase();
        if (!q) {
            return {spaces: [], pages: []};
        }
        return {
            spaces: spaces.filter((s) => s.name.toLowerCase().includes(q)),
            pages: pages.filter((p) => p.title.toLowerCase().includes(q)),
        };
    },
};
