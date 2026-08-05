// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

export const SpaceTypes = {
    RECEIVED_SPACES: manifest.id + '_received_spaces',
    CREATED_SPACE: manifest.id + '_created_space',
    DELETED_SPACE: manifest.id + '_deleted_space',
    RECEIVED_SPACE_MEMBERS: manifest.id + '_received_space_members',
} as const;

export const PageTypes = {
    RECEIVED_PAGES: manifest.id + '_received_pages',
    MOVED_PAGE: manifest.id + '_moved_page',
    DELETED_PAGE: manifest.id + '_deleted_page',
} as const;

export const DraftTypes = {
    RECEIVED_DRAFTS: manifest.id + '_received_drafts',
    RECEIVED_DRAFT: manifest.id + '_received_draft',
    DELETED_DRAFT: manifest.id + '_deleted_draft',

    // Compound on purpose: publishing removes the draft and adds the resulting page.
    // As two actions the tree renders one frame holding both the draft row and the
    // new page row, which reads as a duplicate.
    PUBLISHED_DRAFT: manifest.id + '_published_draft',
} as const;
