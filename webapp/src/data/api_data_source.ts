// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {apiUrl, listAll, restDelete, restGet, restPost} from 'client/rest';

import type {CreateSpaceInput, Page, Space} from 'types/docs';

import type {DocsDataSource} from './docs_data_source';

// Docs data over the plugin REST API (server/api.go). Ids are opaque; lists are
// paginated ({items, has_more}) and followed to completion by listAll.
export const apiDataSource: DocsDataSource = {
    listSpaces: (teamId) => listAll<Space>((query) => `${apiUrl()}/teams/${teamId}/spaces?${query}`),

    getSpace: (spaceId) => restGet<Space>(`${apiUrl()}/spaces/${spaceId}`),

    createSpace: (teamId, input: CreateSpaceInput) => restPost<Space>(`${apiUrl()}/teams/${teamId}/spaces`, {
        title: input.title.trim(),
        description: input.description?.trim() || undefined,
        icon: input.icon || undefined,
    }),

    removeSpaceMember: (spaceId, userId) => restDelete<void>(`${apiUrl()}/spaces/${spaceId}/members/${userId}`),

    listPages: (spaceId) => listAll<Page>((query) => `${apiUrl()}/spaces/${spaceId}/pages?${query}`),
};
