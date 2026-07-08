// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Space} from 'types/docs';

import type {DocsDataSource} from './docs_data_source';
import {pages, spaces} from './fixtures';

const spacesById = new Map<string, Space>(spaces.map((s) => [s.id, s]));

// Default icon for a newly created space until an icon/emoji picker (imported
// from the web app) exists.
const DEFAULT_SPACE_ICON = '📄';

export const mockDataSource: DocsDataSource = {
    listSpaces: () => spaces,
    getSpace: (id) => spacesById.get(id),
    listPages: (spaceId) => pages.filter((page) => page.space_id === spaceId),
    createSpace: (input): Space => {
        const now = Date.now();

        // The slug is the space's URL identifier, so it doubles as the id in the
        // mock (fixture ids are slugs too). isSlugAvailable guarantees it's free.
        const space: Space = {
            id: input.slug,
            team_id: '',
            creator_id: '',
            title: input.title.trim(),
            icon: input.icon || DEFAULT_SPACE_ICON,
            description: input.description?.trim() || undefined,
            visibility: input.visibility,
            props: {},
            create_at: now,
            update_at: now,
            delete_at: 0,
            sort_order: 0,
        };

        // Prepend so the new space shows at the top of the Spaces list, the way
        // the real source would reflect a freshly created entity.
        spaces.unshift(space);
        spacesById.set(space.id, space);
        return space;
    },

    // A slug is free when no existing space claims it. Mock spaces are keyed by
    // their slug (see createSpace + fixtures), so the id map is the source of
    // truth the real backend check stands in for.
    isSlugAvailable: (slug) => !spacesById.has(slug),
};
