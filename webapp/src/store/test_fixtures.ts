// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

export const makeTeam = (id: string, name: string) => ({id, name});

export const makeSpace = (id: string, title: string): Space => ({
    id,
    team_id: '',
    creator_id: '',
    title,
    props: {},
    create_at: 0,
    update_at: 0,
    delete_at: 0,
    sort_order: 0,
});

export const makePage = (id: string, spaceId: string, title: string): Page => ({
    id,
    space_id: spaceId,
    parent_id: '',
    type: 'page',
    title,
    body: '',
    sort_order: 0,
    create_at: 0,
    update_at: 0,
    edit_at: 0,
    delete_at: 0,
});
