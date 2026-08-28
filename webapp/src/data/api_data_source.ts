// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError, apiUrl, listAll, restDelete, restGet, restPatch, restPost} from 'client/rest';

import type {CreatePageInput, CreateSpaceInput, Page, Space, UpdatePagePatch, UpdateSpacePatch} from 'types/docs';
import type {Draft, DraftPatch, DraftSummary} from 'types/drafts';
import type {SpaceAccess, SpaceMember} from 'types/permissions';

import type {DocsDataSource} from './docs_data_source';
import {asPublishConflict} from './publish_conflict';

// Ids are server-generated and URL-safe today, so this is defense in depth: an id
// that ever arrives malformed (or user-influenced) can't reshape the request path.
const seg = encodeURIComponent;

// A draft is addressed through its page, not by an id of its own.
const draftUrl = (spaceId: string, pageId: string): string =>
    `${apiUrl()}/spaces/${seg(spaceId)}/pages/${seg(pageId)}/draft`;

// The server's page list returns summaries (no body/delete_at); fill the fields
// the store's Page type needs so a summary is a valid, body-less Page.
const toPage = (summary: Page): Page => ({
    ...summary,
    body: summary.body ?? '',
    delete_at: summary.delete_at ?? 0,
    user_id: summary.user_id ?? '',
    last_modified_by: summary.last_modified_by ?? '',
});

// Docs data over the plugin REST API (server/api.go). Ids are opaque; lists are
// paginated ({items, has_more}) and followed to completion by listAll.
export const apiDataSource: DocsDataSource = {
    listSpaces: (teamId) => listAll<Space>((query) => `${apiUrl()}/teams/${seg(teamId)}/spaces?${query}`),

    getSpace: (spaceId) => restGet<SpaceAccess>(`${apiUrl()}/spaces/${seg(spaceId)}`),

    createSpace: (teamId, input: CreateSpaceInput) => restPost<SpaceAccess>(`${apiUrl()}/teams/${seg(teamId)}/spaces`, {
        title: input.title.trim(),
        description: input.description?.trim() || undefined,
        icon: input.icon || undefined,

        // Sent on every create: the server defaults an absent view_access to
        // 'open', so omitting it would discard a 'private' selection.
        view_access: input.view_access,
    }),

    updateSpace: (spaceId, patch: UpdateSpacePatch, expectedUpdateAt) => restPatch<SpaceAccess>(`${apiUrl()}/spaces/${seg(spaceId)}`, {
        title: patch.title?.trim(),
        description: patch.description?.trim(),
        icon: patch.icon,
        props: patch.props,
        expected_update_at: expectedUpdateAt,
    }),

    deleteSpace: (spaceId) => restDelete<void>(`${apiUrl()}/spaces/${seg(spaceId)}`),

    addSpaceMember: (spaceId, userId) =>
        restPost<SpaceMember>(`${apiUrl()}/spaces/${seg(spaceId)}/members`, {user_id: userId}),

    removeSpaceMember: (spaceId, userId) => restDelete<void>(`${apiUrl()}/spaces/${seg(spaceId)}/members/${seg(userId)}`),

    listSpaceMembers: (spaceId) => listAll<SpaceMember>((query) => `${apiUrl()}/spaces/${seg(spaceId)}/members?${query}`),

    listPages: async (spaceId) => {
        const summaries = await listAll<Page>((query) => `${apiUrl()}/spaces/${seg(spaceId)}/pages?${query}`);
        return summaries.map(toPage);
    },

    getPage: async (spaceId, pageId) =>
        toPage(await restGet<Page>(`${apiUrl()}/spaces/${seg(spaceId)}/pages/${seg(pageId)}`)),

    movePage: async (spaceId, pageId, parentId, siblingIndex, expectedUpdateAt) => {
        const moved = await restPatch<Page>(`${apiUrl()}/spaces/${seg(spaceId)}/pages/${seg(pageId)}/move`, {
            parent_id: parentId,
            sibling_index: siblingIndex,
            expected_update_at: expectedUpdateAt,
        });
        return toPage(moved);
    },

    createPage: async (spaceId, input: CreatePageInput) => {
        const created = await restPost<Page>(`${apiUrl()}/spaces/${seg(spaceId)}/pages`, {
            title: input.title.trim(),
            parent_id: input.parentId || undefined,
        });
        return toPage(created);
    },

    updatePage: async (spaceId, pageId, patch: UpdatePagePatch, baseEditAt) => {
        const updated = await restPatch<Page>(`${apiUrl()}/spaces/${seg(spaceId)}/pages/${seg(pageId)}`, {
            title: patch.title?.trim(),
            body: patch.body,
            base_edit_at: baseEditAt,
        });
        return toPage(updated);
    },

    deletePage: (spaceId, pageId) => restDelete<void>(`${apiUrl()}/spaces/${seg(spaceId)}/pages/${seg(pageId)}`),

    createSpaceDraft: (spaceId, title, parentId) => restPost<Draft>(`${apiUrl()}/spaces/${seg(spaceId)}/drafts`, {
        title,
        parent_id: parentId,
    }),

    getPageDraft: async (spaceId, pageId, signal) => {
        try {
            return await restGet<Draft>(draftUrl(spaceId, pageId), signal);
        } catch (error) {
            // Having no draft is the common case, not an error worth propagating.
            if (error instanceof RestError && error.status === 404) {
                return undefined;
            }
            throw error;
        }
    },

    updatePageDraft: (spaceId, pageId, patch: DraftPatch, signal) =>
        restPatch<Draft>(draftUrl(spaceId, pageId), patch, signal),

    deletePageDraft: (spaceId, pageId) => restDelete<void>(draftUrl(spaceId, pageId)),

    listSpaceDrafts: (spaceId) =>
        listAll<DraftSummary>((query) => `${apiUrl()}/spaces/${seg(spaceId)}/drafts?${query}`),

    publishPageDraft: async (spaceId, pageId, force) => {
        try {
            return toPage(await restPost<Page>(`${draftUrl(spaceId, pageId)}/publish`, {force}));
        } catch (error) {
            return asPublishConflict(error);
        }
    },
};
