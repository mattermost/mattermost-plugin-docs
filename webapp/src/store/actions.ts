// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';
import {joinSpace} from 'client/space_permissions';
import {docsDataSource} from 'data';

import {ClientError} from '@mattermost/client';

import {getCurrentTeamId, getMyTeams} from 'mattermost-redux/selectors/entities/teams';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import type {CreatePageInput, CreateSpaceInput, Page, Space, UpdatePagePatch, UpdateSpacePatch} from 'types/docs';
import type {Draft, DraftPatch} from 'types/drafts';
import type {SpaceAccess} from 'types/permissions';
import {LAST_SPACE_ADMIN_ERROR_ID, LAST_SPACE_MEMBER_ERROR_ID, NOT_TEAM_MEMBER_ERROR_ID, SPACE_LOCK_TIMEOUT_ERROR_ID} from 'types/server_errors';
import type {DocsThunkAction} from 'types/store';

import {DraftTypes, PageTypes, SpaceTypes} from './action_types';
import {collectSubtreeIds} from './entities';
import {getMustJoinSpace} from './permissions';
import {getPage, getPagesById, getSpace} from './selectors';

// Orders the writers of a space's resolved access record. Every read or write whose response is
// dispatched into the `spaces` slice claims an issue slot before its request is sent; the dispatch
// is dropped when a later-issued response for the same space has already been applied, so a slower
// earlier read cannot overwrite the state a fresher response wrote. An eviction claims a slot too,
// which keeps a response already in flight from resurrecting a space evicted on a definitive
// denial. The per-member counterpart is useSpacePermissions's write-generation guard; this one
// covers the space record itself, whose writers span thunks, hooks, and WebSocket handlers.
let spaceAccessIssueCounter = 0;
const appliedSpaceAccessGeneration = new Map<string, number>();

/** Claims the next issue slot. Call before sending the request whose response will be applied. */
export function nextSpaceAccessGeneration(): number {
    return ++spaceAccessIssueCounter;
}

// Builds the received-spaces action for one record — or an empty one, which the reducer ignores,
// when a later-issued response for the same space has already been applied.
const spaceAccessAction = (space: Space, generation: number) => {
    if (generation < (appliedSpaceAccessGeneration.get(space.id) ?? 0)) {
        return {type: SpaceTypes.RECEIVED_SPACES, spaces: [] as Space[]};
    }
    appliedSpaceAccessGeneration.set(space.id, generation);
    return {type: SpaceTypes.RECEIVED_SPACES, spaces: [space]};
};

/** Drops spaceId from the store and supersedes any of its responses still in flight. */
export function evictSpace(spaceId: string) {
    appliedSpaceAccessGeneration.set(spaceId, ++spaceAccessIssueCounter);
    return {type: SpaceTypes.DELETED_SPACE, spaceId};
}

// Spaces the caller may read in the current team. The server combines membership with the eligible
// open-space fall-through. A failed load leaves the store empty rather than crashing on mount.
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
        const generation = nextSpaceAccessGeneration();
        try {
            const space = await docsDataSource.getSpace(spaceId);
            if (space) {
                dispatch(spaceAccessAction(space, generation));
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
        // On the two draft writes rather than at the affordance that leads to them, so a membership
        // records a contribution rather than an intention to make one — see ensureSpaceMembership.
        await dispatch(ensureSpaceMembership(spaceId));

        const draft = await docsDataSource.createSpaceDraft(spaceId, title, parentId);
        dispatch({type: DraftTypes.RECEIVED_DRAFT, draft});
        return draft;
    };
}

// Autosave. The caller owns the debounce and the in-flight bookkeeping; this is
// just the write and the store update.
export function saveDraft(spaceId: string, pageId: string, patch: DraftPatch, signal?: AbortSignal): DocsThunkAction<Promise<Draft>> {
    return async (dispatch) => {
        // A no-op for a member, which is every caller after the first autosave of a session.
        await dispatch(ensureSpaceMembership(spaceId));

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

/**
 * Joins the caller to a space when the server has said they may join it, before a write of theirs
 * is sent.
 *
 * This is what the authoring affordances on an open space are offered on the strength of: the
 * caller holds read_page and nothing else, and the space's defaults are what they would hold as a
 * member. The write itself is gated on real membership alone — nothing else grants it — so this
 * call must run first.
 *
 * A no-op when the store says there is nothing to join, which is every case but a non-member of an
 * open space. Idempotent server-side, so a stale "yes" costs a round trip rather than an error.
 *
 * Called from the draft writes themselves (createDraft, saveDraft), never from the affordance that
 * leads to them. Joining on the write keeps membership a record of a contribution, which is also
 * what makes removing a member worth doing — a removed user rejoins by writing again, not by
 * opening an editor.
 *
 * Rejects on failure so the caller can abandon the write rather than send one that will be refused.
 */
export function ensureSpaceMembership(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        if (!getMustJoinSpace(getState(), spaceId)) {
            return;
        }
        const generation = nextSpaceAccessGeneration();
        dispatch(receivedSpaceAccess(await joinSpace(spaceId), generation));
    };
}

/**
 * Stores the server-resolved access record shared by permission-gated surfaces. generation is the
 * issue slot (nextSpaceAccessGeneration) claimed before the request that produced `space` was
 * sent; a record superseded by a later-issued response or an eviction resolves to an empty
 * received-spaces action, which the reducer ignores.
 */
export function receivedSpaceAccess(space: SpaceAccess, generation: number) {
    return spaceAccessAction(space, generation);
}

/** Invalidates the hook-local per-member grant matrix. */
export function spaceMemberPermissionsChanged(spaceId: string) {
    return {type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId};
}

/**
 * Re-reads spaceId's access record, evicting it on a definitive denial.
 *
 * A refresh and loss of access are not the same event: the record that comes back may simply carry
 * a narrowed permission set, in which case it replaces the stored one. Only a definitive denial
 * (403/404) evicts — the caller lost the space, and retaining the stale record would keep
 * rendering pre-revocation affordances indefinitely. A request that never completed is not an
 * answer about access, so a transient failure retains the stored record.
 *
 * Resolves to the refreshed record, or undefined when the space was evicted or the read failed.
 */
export function refreshSpaceAccess(spaceId: string): DocsThunkAction<Promise<Space | undefined>> {
    return async (dispatch) => {
        const generation = nextSpaceAccessGeneration();
        try {
            const space = await docsDataSource.getSpace(spaceId);
            if (space) {
                dispatch(spaceAccessAction(space, generation));
            }
            return space;
        } catch (error) {
            if (error instanceof RestError && (error.status === 403 || error.status === 404)) {
                dispatch(evictSpace(spaceId));
                return undefined;
            }
            // eslint-disable-next-line no-console
            console.error('Docs: failed to re-resolve space access', spaceId, error);
            return undefined;
        }
    };
}

/**
 * Refreshes shared access before invalidating grants. A definitive denial evicts the space
 * (refreshSpaceAccess); on any other failed refresh, retain the matrix rather than reload it under
 * stale manage authority and mistake a redacted roster for grant data.
 */
export function refreshSpaceAfterMemberPermissionsChanged(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        const space = await dispatch(refreshSpaceAccess(spaceId));
        if (!space) {
            return;
        }
        dispatch(spaceMemberPermissionsChanged(spaceId));
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

/**
 * Re-resolves a space after the caller's own membership was removed, evicting it only once the
 * server has actually refused the read.
 *
 * Removal and loss of access are not the same event. The open-space fall-through may still admit
 * the caller, in which case the returned record carries their narrowed permission set and remains
 * in the store; only a definitive denial evicts (refreshSpaceAccess).
 */
export function refreshSpaceAfterSelfRemoval(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        const space = await dispatch(refreshSpaceAccess(spaceId));
        if (space) {
            // The caller is gone from the roster the space view renders its count from, so the
            // read that survived has a stale member list behind it.
            await dispatch(fetchSpaceMembers(spaceId));
        }
    };
}

// Archives (soft-deletes) a space and prunes it from the store. Rejects on
// failure so the caller can surface it.
export function deleteSpace(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.deleteSpace(spaceId);
        dispatch(evictSpace(spaceId));
    };
}

// The removal routes answer 409 for three different rules — last member, sole admin, and a
// retryable lock timeout — so the id, not the status, tells them apart. The REST layer lifts the
// AppError id into server_error_id (see client/rest.ts).

const spaceErrorId = (error: unknown): string | undefined =>
    (error instanceof ClientError ? error.server_error_id : undefined);

// The space would be left with no member holding access.
export function isLastSpaceMemberError(error: unknown): boolean {
    return spaceErrorId(error) === LAST_SPACE_MEMBER_ERROR_ID;
}

// The space would be left with members but no administrator. Distinct from the above because the
// remedy is different: another *admin* is required, not another member.
export function isLastSpaceAdminError(error: unknown): boolean {
    return spaceErrorId(error) === LAST_SPACE_ADMIN_ERROR_ID;
}

// A space-keyed lock timeout. Retryable as-is, so it must not be reported as a rule violation.
export function isSpaceLockTimeoutError(error: unknown): boolean {
    return spaceErrorId(error) === SPACE_LOCK_TIMEOUT_ERROR_ID;
}

export function isNotTeamMemberError(error: unknown): boolean {
    return spaceErrorId(error) === NOT_TEAM_MEMBER_ERROR_ID;
}

// Leaving a space is removing yourself from its membership. The server rejects
// removing the last authorized member (409); the caller surfaces that.
//
// Losing membership is not necessarily losing the space: the open-space fall-through may still
// admit the caller. Re-resolve instead of assuming either outcome.
export function leaveSpace(spaceId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch, getState) => {
        const userId = getCurrentUserId(getState());
        await docsDataSource.removeSpaceMember(spaceId, userId);
        await dispatch(refreshSpaceAfterSelfRemoval(spaceId));
    };
}
