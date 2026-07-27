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
} as const;
