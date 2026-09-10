// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';
import {recordOwnPageWrite} from 'hooks/own_page_writes';

import {ClientError} from '@mattermost/client';

import {getCurrentTeamId, getMyTeams} from 'mattermost-redux/selectors/entities/teams';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import type {CreatePageInput, CreateSpaceInput, Page, Space, UpdatePagePatch, UpdateSpacePatch} from 'types/docs';
import type {Draft, DraftPatch} from 'types/drafts';
import type {DocsThunkAction} from 'types/store';

import {DraftTypes, PageTypes, SpaceTypes} from './action_types';
import {collectSubtreeIds} from './entities';
import {getPage, getPagesById, getSpace} from './selectors';

// Spaces the caller belongs to in the current team (the server scopes the list
// by backing-channel membership). A failed load leaves the store empty rather
// than crashing the product on mount.
export function fetchSpaces(): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const teamId = getCurrentTeamId(getState());
        if (!teamId) {
            return;
        }
        try {
            const spaces = await docsDataSource.listSpaces(teamId);

            // `teamId` records that this team's list is now known, so consumers
            // can tell "no spaces" from "not loaded yet" (an empty result would
            // otherwise leave no trace in the index).
            dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces, teamId});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load spaces', error);
        }
    };
}

/**
 * Loads one space by id, for a URL that names a space the store doesn't hold.
 *
 * Deliberately not folded into fetchSpaces: a deep link must resolve against the
 * server, not against whatever the team listing returned. The listing can be
 * stale (a space created since), scoped to another team, or simply not to have run
 * yet, and none of those mean the id is bad.
 *
 * Resolves to undefined when the space can't be read (403/404) or the request
 * failed — the caller can only tell "not there" from "not asked yet" by whether
 * this settled, so it must not reject.
 */
export function fetchSpace(spaceId: string): DocsThunkAction<Promise<Space | undefined>> {
    return async (dispatch) => {
        try {
            const space = await docsDataSource.getSpace(spaceId);
            if (space) {
                dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: [space]});
            }
            return space;
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load space', spaceId, error);
            return undefined;
        }
    };
}

// Cross-team load for the switcher: fan out over the user's teams. The server
// has no all-teams endpoint, so this is N team-scoped calls run in parallel.
export function fetchAllSpaces(): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const teams = getMyTeams(getState());
        const settled = await Promise.allSettled(teams.map((team) => docsDataSource.listSpaces(team.id)));
        const spaces = settled.flatMap((result, index) => {
            if (result.status === 'fulfilled') {
                return result.value;
            }
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load spaces for team', teams[index].id, result.reason);
            return [];
        });
        if (spaces.length > 0) {
            dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces});
        }
    };
}

// Loads a space's pages into the store (backs the page count today, the page
// tree later). Best-effort: a failure leaves the count at its current value.
export function fetchPages(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        try {
            await dispatch(loadPages(spaceId));
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load pages', error);
        }
    };
}

// Same load, but rejecting — for callers that have to react to a failure rather
// than tolerate a stale list.
export function loadPages(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        const pages = await docsDataSource.listPages(spaceId);
        dispatch({type: PageTypes.RECEIVED_PAGES, pages, spaceId});
    };
}

/**
 * Loads one page by id, for a URL that names a page the store doesn't hold.
 *
 * The space listing is the usual source, so this covers what it can't: a page
 * created since the listing ran, or a page reached before it did. Also the only
 * source of a page's body — the listing returns summaries.
 *
 * Resolves to undefined rather than rejecting, for the same reason as fetchSpace:
 * "settled with nothing" is the answer a routed id needs.
 */
export function fetchPage(spaceId: string, pageId: string): DocsThunkAction<Promise<Page | undefined>> {
    return async (dispatch) => {
        try {
            const page = await docsDataSource.getPage(spaceId, pageId);
            dispatch({type: PageTypes.RECEIVED_PAGES, pages: [page]});
            return page;
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load page', pageId, error);
            return undefined;
        }
    };
}

// Reparents/reorders a page. Optimistically reindexes the store, then reconciles
// with the server-returned page. On failure it re-fetches the space's pages to
// restore server truth. siblingIndex is 0-based within the new parent;
// parentId '' is the space root.
export function movePage(spaceId: string, pageId: string, parentId: string, siblingIndex: number): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const page = getPage(getState(), pageId);
        if (!page) {
            return;
        }
        const expectedUpdateAt = page.update_at;

        dispatch({type: PageTypes.MOVED_PAGE, pageId, spaceId, parentId, siblingIndex});

        try {
            const moved = await docsDataSource.movePage(spaceId, pageId, parentId, siblingIndex, expectedUpdateAt);
            dispatch({type: PageTypes.RECEIVED_PAGES, pages: [moved]});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to move page', error);

            // The optimistic reindex is now wrong, so pull server truth back in
            // before rejecting — the caller surfaces the failure to the user.
            await dispatch(fetchPages(spaceId));
            throw error;
        }
    };
}

// Creates a page in a space (optionally under a parent) and returns the
// server-assigned entity (rejects on failure so the caller can surface it).
export function createPage(spaceId: string, input: CreatePageInput): DocsThunkAction<Promise<Page>> {
    return async (dispatch) => {
        const page = await docsDataSource.createPage(spaceId, input);
        dispatch({type: PageTypes.RECEIVED_PAGES, pages: [page]});
        return page;
    };
}

// Patches a page's editable fields and reconciles the store with the
// server-returned page. Reads the current edit_at as the optimistic-lock
// baseline (the server rejects a stale write). Rejects on failure so the caller
// can surface it and stay open.
export function updatePage(spaceId: string, pageId: string, patch: UpdatePagePatch): DocsThunkAction<Promise<Page>> {
    return async (dispatch, getState) => {
        const page = getPage(getState(), pageId);
        if (!page) {
            throw new Error(`Docs: page ${pageId} is not loaded`);
        }
        const updated = await docsDataSource.updatePage(spaceId, pageId, patch, page.edit_at);
        recordOwnPageWrite(updated.id, updated.edit_at);
        dispatch({type: PageTypes.RECEIVED_PAGES, pages: [updated]});
        return updated;
    };
}

// Deletes (soft-deletes) a page and prunes it — and its descendants — from the
// store. Rejects on failure so the caller can surface it.
export function deletePage(spaceId: string, pageId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const pageIds = [...collectSubtreeIds(getPagesById(getState()), pageId)];
        await docsDataSource.deletePage(spaceId, pageId);
        dispatch({type: PageTypes.DELETED_PAGE, pageId, spaceId, pageIds});
    };
}

// The caller's drafts for a space. Seeds the index entry even when there are none,
// so consumers can tell "no drafts" from "not fetched". A failed load leaves the
// space's drafts unknown rather than pretending it has none.
export function fetchDrafts(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        try {
            const drafts = await docsDataSource.listSpaceDrafts(spaceId);
            dispatch({type: DraftTypes.RECEIVED_DRAFTS, drafts, spaceId});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load drafts', error);
        }
    };
}

// Loads one draft with its body (the space list carries metadata only). Returns
// the draft, or undefined when the page has none. Rejects on failure so the editor
// can tell "no draft" apart from "could not tell".
export function fetchPageDraft(spaceId: string, pageId: string, signal?: AbortSignal): DocsThunkAction<Promise<Draft | undefined>> {
    return async (dispatch) => {
        const draft = await docsDataSource.getPageDraft(spaceId, pageId, signal);
        if (draft) {
            dispatch({type: DraftTypes.RECEIVED_DRAFT, draft});
        }
        return draft;
    };
}

/**
 * Reserves a page id and creates a draft for a page that does not exist yet.
 *
 * `base_edit_at` stays 0 for the draft's lifetime, which is what marks it as a new
 * page rather than an edit to an existing one.
 */
export function createDraft(spaceId: string, title: string, parentId = ''): DocsThunkAction<Promise<Draft>> {
    return async (dispatch) => {
        const draft = await docsDataSource.createSpaceDraft(spaceId, title, parentId);
        dispatch({type: DraftTypes.RECEIVED_DRAFT, draft});
        return draft;
    };
}

// Autosave. The caller owns the debounce and the in-flight bookkeeping; this is
// just the write and the store update.
export function saveDraft(spaceId: string, pageId: string, patch: DraftPatch, signal?: AbortSignal): DocsThunkAction<Promise<Draft>> {
    return async (dispatch) => {
        const draft = await docsDataSource.updatePageDraft(spaceId, pageId, patch, signal);
        dispatch({type: DraftTypes.RECEIVED_DRAFT, draft});
        return draft;
    };
}

// Discards unpublished work. For an orphan draft this destroys the unpublished page
// outright; for an edit it leaves the published page as it was.
export function discardDraft(spaceId: string, pageId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.deletePageDraft(spaceId, pageId);
        dispatch({type: DraftTypes.DELETED_DRAFT, spaceId, pageId});
    };
}

/**
 * Publishes a draft into its page and removes the draft.
 *
 * One dispatch, not two: the tree reads pages and orphan drafts from the same
 * render, so removing the draft and adding the page separately shows a frame with
 * both — the new page appearing twice.
 *
 * Rejects with PublishConflictError when the page moved underneath the draft; the
 * caller decides whether to offer force (see PublishConflictError.isForceable).
 */
export function publishDraft(spaceId: string, pageId: string, force = false): DocsThunkAction<Promise<Page>> {
    return async (dispatch) => {
        const page = await docsDataSource.publishPageDraft(spaceId, pageId, force);
        recordOwnPageWrite(page.id, page.edit_at);
        dispatch({type: DraftTypes.PUBLISHED_DRAFT, spaceId, pageId, page});
        return page;
    };
}

// Loads a space's members (user ids) into the store, backing the member count.
export function fetchSpaceMembers(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        try {
            const members = await docsDataSource.listSpaceMembers(spaceId);
            dispatch({type: SpaceTypes.RECEIVED_SPACE_MEMBERS, spaceId, userIds: members.map((m) => m.user_id)});
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to load space members', error);
        }
    };
}

/**
 * Adds one member to a space.
 *
 * Rejects on failure: only the caller can tell the server's 403 ("not a member of
 * this team") apart from a fault that deserves a generic message.
 *
 * Not `leaveSpace`, which removes the *current* user and drops the whole space from
 * the store. This only edits the member array.
 */
export function addSpaceMember(spaceId: string, userId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.addSpaceMember(spaceId, userId);
        dispatch({type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId, userId});
    };
}

/**
 * Removes one member from a space. Rejects on failure so the caller can recognise
 * the last-member 409 (see isLastSpaceMemberError).
 *
 * Not `leaveSpace`: that removes the current user and prunes the space. This leaves
 * the space in place and only edits its member array.
 */
export function removeSpaceMember(spaceId: string, userId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.removeSpaceMember(spaceId, userId);
        dispatch({type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId, userId});
    };
}

export type FailedMemberAdd = {userId: string; error: unknown};

/**
 * Adds several members by dispatching addSpaceMember once per user, concurrently.
 *
 * Never rejects. A batch has no single outcome to reject with, so the result is the
 * users that failed and why — an empty array means every add landed. Each success has
 * already dispatched by the time this resolves, so the store is right even for a
 * batch that partly failed.
 *
 * The raw `error` is passed back rather than a message: choosing wording belongs with
 * the other message selection, in useManageSpaceMembers.
 */
export function addSpaceMembers(spaceId: string, userIds: string[]): DocsThunkAction<Promise<FailedMemberAdd[]>> {
    return async (dispatch) => {
        const settled = await Promise.allSettled(
            userIds.map((userId) => dispatch(addSpaceMember(spaceId, userId))),
        );
        return settled.flatMap((result, i) => (
            result.status === 'rejected' ? [{userId: userIds[i], error: result.reason}] : []
        ));
    };
}

// Creates a space in the current team and returns the server-assigned entity
// (rejects on failure so the form can surface it).
export function createSpace(input: CreateSpaceInput): DocsThunkAction<Promise<Space>> {
    return async (dispatch, getState) => {
        const teamId = getCurrentTeamId(getState());
        if (!teamId) {
            throw new Error('Docs: cannot create a space without a current team');
        }
        const space = await docsDataSource.createSpace(teamId, input);
        dispatch({type: SpaceTypes.CREATED_SPACE, space});
        return space;
    };
}

// Patches a space's editable fields and reconciles the store with the
// server-returned space. Reads the current update_at for optimistic concurrency
// (the server rejects a stale write). Rejects on failure so the settings form
// can surface it and stay open.
export function updateSpace(spaceId: string, patch: UpdateSpacePatch): DocsThunkAction<Promise<Space>> {
    return async (dispatch, getState) => {
        const expectedUpdateAt = getSpace(getState(), spaceId)?.update_at ?? 0;
        const updated = await docsDataSource.updateSpace(spaceId, patch, expectedUpdateAt);
        dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: [updated]});
        return updated;
    };
}

// Archives (soft-deletes) a space and prunes it from the store. Rejects on
// failure so the caller can surface it.
export function deleteSpace(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.deleteSpace(spaceId);
        dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
    };
}

// The server rejects removing a space's last authorized member with
// `app.space.remove_member.last_member.app_error` (409). The REST layer keeps
// only {message, status_code} from the AppError, so the status is all a caller
// has to recognize it by.
export function isLastSpaceMemberError(error: unknown): boolean {
    return error instanceof ClientError && error.status_code === 409;
}

// The add route answers 403 when the target isn't an active member of the space's
// team. That is the one add failure a user can act on, so it gets its own message;
// like isLastSpaceMemberError, the status is all the REST layer preserves.
export function isNotTeamMemberError(error: unknown): boolean {
    return error instanceof ClientError && error.status_code === 403;
}

// Leaving a space is removing yourself from its membership. The server rejects
// removing the last authorized member (409); the caller surfaces that.
export function leaveSpace(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const userId = getCurrentUserId(getState());
        await docsDataSource.removeSpaceMember(spaceId, userId);
        dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
    };
}
