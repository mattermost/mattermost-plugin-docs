// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';
import type {Draft} from 'types/drafts';

export const makeTeam = (id: string, name: string) => ({id, name});

export const makeSpace = (id: string, title: string, teamId = '', sortOrder = 0): Space => ({
    id,
    team_id: teamId,
    creator_id: '',
    title,
    props: {},
    create_at: 0,
    update_at: 0,
    delete_at: 0,
    sort_order: sortOrder,
});

// `baseEditAt` 0 is an orphan draft (a page that does not exist yet); non-zero is
// unpublished edits to the page it was started from.
export const makeDraft = (pageId: string, spaceId: string, title: string, updateAt = 0, baseEditAt = 0): Draft => ({
    user_id: 'me',
    space_id: spaceId,
    page_id: pageId,
    parent_id: '',
    title,
    body: '',
    file_ids: [],
    props: {},
    create_at: 0,
    update_at: updateAt,
    last_active_at: 0,
    base_edit_at: baseEditAt,
});

export const makePage = (id: string, spaceId: string, title: string, sortOrder = 0): Page => ({
    id,
    space_id: spaceId,
    parent_id: '',
    type: 'page',
    title,
    body: '',
    user_id: '',
    last_modified_by: '',
    sort_order: sortOrder,
    create_at: 0,
    update_at: 0,
    edit_at: 0,
    delete_at: 0,
});
