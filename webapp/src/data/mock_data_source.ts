// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space, SpaceSummary} from 'types/docs';

import type {DocsDataSource, DocsSearchResults} from './docs_data_source';
import {pages, recentPageIds, recentSpaceIds, recentSpaceSummaries, spaces} from './fixtures';

const spacesById = new Map<string, Space>(spaces.map((s) => [s.id, s]));
const pagesById = new Map<string, Page>(pages.map((p) => [p.id, p]));

// Default emoji for a newly created space until an icon/emoji picker (imported
// from the web app) exists.
const DEFAULT_SPACE_EMOJI = '📄';

let createdCount = 0;

export const mockDataSource: DocsDataSource = {
    listSpaces: () => spaces,
    getSpace: (id) => spacesById.get(id),
    listPages: (spaceId) => pages.filter((page) => page.spaceId === spaceId),
    getRecentSpaces: () => recentSpaceIds.map((id) => spacesById.get(id)).filter((s): s is Space => Boolean(s)),
    getRecentSpaceSummaries: (): SpaceSummary[] => recentSpaceSummaries.flatMap(({spaceId, pageCount, viewedLabel}) => {
        const space = spacesById.get(spaceId);
        return space ? [{space, pageCount, viewedLabel}] : [];
    }),
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
    createSpace: (input): Space => {
        createdCount += 1;
        const space: Space = {
            id: `mock-space-${input.slug || 'space'}-${createdCount}`,
            name: input.name.trim(),
            emoji: input.emoji || DEFAULT_SPACE_EMOJI,
            visibility: input.visibility,
            description: input.description?.trim() || undefined,
        };

        // Prepend so the new space shows at the top of the Spaces list, the way
        // the real source would reflect a freshly created entity.
        spaces.unshift(space);
        spacesById.set(space.id, space);
        return space;
    },

    // No slug uniqueness without a server; every slug reads as available until
    // the real source checks the backend.
    isSlugAvailable: () => true,
};
