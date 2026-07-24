// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

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

export const makePage = (id: string, spaceId: string, title: string, sortOrder = 0): Page => ({
    id,
    space_id: spaceId,
    parent_id: '',
    type: 'page',
    title,
    body: '',
    sort_order: sortOrder,
    create_at: 0,
    update_at: 0,
    edit_at: 0,
    delete_at: 0,
});
