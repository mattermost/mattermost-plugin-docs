// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {CreatePageInput, CreateSpaceInput, Page, Space, UpdatePagePatch, UpdateSpacePatch} from 'types/docs';
import type {Draft, DraftPatch, DraftSummary} from 'types/drafts';
import type {Permission, SpaceAccess, SpaceMember, SpaceViewAccess} from 'types/permissions';

// The seam between the store's thunks and the Docs server REST API. The
// API-backed source implements this over the plugin's /api/v1 routes; the
// interface stays transport-agnostic so tests can substitute a fake.
//
// All methods are async: they map to network calls. Ids are the platform's
// opaque 26-char ids (no slugs) and space reads/lists are team-scoped, matching
// the server contract.
export interface DocsDataSource {

    // Spaces the caller may read in the given team.
    listSpaces(teamId: string): Promise<Space[]>;

    // One space by id. This is how a space arrives when the URL names it directly
    // and the team listing hasn't run (or ran before the space existed) — a
    // deep link must not depend on the space being in a list the client already
    // holds. Rejects with a RestError the caller interprets (403/404 = the caller
    // can't see it).
    getSpace(spaceId: string): Promise<SpaceAccess>;

    // Creates a space in the team and returns it (with its server-assigned id
    // and team_id).
    createSpace(teamId: string, input: CreateSpaceInput): Promise<SpaceAccess>;

    // Patches a space's editable fields (name/description/icon) and returns the
    // updated space. `expectedUpdateAt` is the space's current update_at for
    // optimistic concurrency (the server rejects a stale write). Only the
    // provided fields are sent.
    updateSpace(spaceId: string, patch: UpdateSpacePatch, expectedUpdateAt: number): Promise<SpaceAccess>;

    // Flips the space between open and private. This is separate from the general metadata patch
    // because it has its own administration gate and optimistic-concurrency contract.
    setSpaceViewAccess(spaceId: string, viewAccess: SpaceViewAccess, expectedUpdateAt: number): Promise<SpaceAccess>;

    // Replaces the permissions every ordinary member receives from the space's backing scheme.
    setSpaceDefaultPermissions(spaceId: string, permissions: Permission[]): Promise<SpaceAccess>;

    // Archives (soft-deletes) a space. The server rejects when the caller can't
    // manage the space; the caller surfaces that.
    deleteSpace(spaceId: string): Promise<void>;

    // Adds a user to a space. The server requires the target to be an active member
    // of the space's team (403 otherwise) and rejects an unknown user (404). There is
    // no bulk route: adding several people is several calls.
    addSpaceMember(spaceId: string, userId: string): Promise<SpaceMember>;

    // Joins the caller to an open space. Idempotent when they are already a member.
    joinSpace(spaceId: string): Promise<SpaceAccess>;

    // Removes a member from a space. Leaving a space is removing yourself; the
    // server rejects removing the last authorized member (409).
    removeSpaceMember(spaceId: string, userId: string): Promise<void>;

    // Members of a space. This general-purpose source consumes only user_id for counts and
    // avatars; the server redacts the permission fields when the caller lacks the manage tier.
    listSpaceMembers(spaceId: string): Promise<SpaceMember[]>;

    // Replaces one member's direct grants. An empty array deliberately clears them.
    setSpaceMemberPermissions(spaceId: string, userId: string, granted: Permission[]): Promise<SpaceMember>;

    // Pages in a space. The server returns page summaries (no body); the source
    // normalizes them to Page with an empty body for the store.
    listPages(spaceId: string): Promise<Page[]>;

    // One page by id, with its body. The counterpart to getSpace for deep links:
    // a routed page id is resolved against the server rather than only against
    // whatever the space listing happened to return.
    getPage(spaceId: string, pageId: string): Promise<Page>;

    // Reparents and/or reorders a page. `parentId` is the new parent id ('' =
    // space root); `siblingIndex` is the 0-based position within the new parent.
    // `expectedUpdateAt` is the moved page's current update_at for optimistic
    // concurrency. Returns the moved page.
    movePage(spaceId: string, pageId: string, parentId: string, siblingIndex: number, expectedUpdateAt: number): Promise<Page>;

    // Creates a page in a space (optionally under a parent) and returns it (with
    // its server-assigned id and sort_order).
    createPage(spaceId: string, input: CreatePageInput): Promise<Page>;

    // Patches a page's editable fields (title/body) and returns the updated page.
    // `baseEditAt` is the page's current edit_at, the optimistic-lock baseline:
    // the server rejects a stale baseline (409) and a missing one (400).
    updatePage(spaceId: string, pageId: string, patch: UpdatePagePatch, baseEditAt: number): Promise<Page>;

    // Deletes (soft-deletes) a page and, on the server, its subpages.
    deletePage(spaceId: string, pageId: string): Promise<void>;

    // Reserves a page id and creates a draft against it, for a page that does not
    // exist yet. `parentId` is where the page will land on publish ('' = root).
    createSpaceDraft(spaceId: string, title: string, parentId: string): Promise<Draft>;

    // The caller's draft for a page, or undefined when they have none. Absence is a
    // 404 on the wire and is normalized here: "no unpublished edits" is an ordinary
    // answer, not a failure the caller should have to catch.
    getPageDraft(spaceId: string, pageId: string, signal?: AbortSignal): Promise<Draft | undefined>;

    // Autosave. Only the provided fields are sent; `base_edit_at` is ignored by the
    // server after the draft's first write (it is write-once), so this cannot
    // re-baseline an existing draft.
    updatePageDraft(spaceId: string, pageId: string, patch: DraftPatch, signal?: AbortSignal): Promise<Draft>;

    // Discards the caller's draft, leaving any published page untouched.
    deletePageDraft(spaceId: string, pageId: string): Promise<void>;

    // The caller's drafts across a space, newest first. Metadata only — fetch a
    // draft by page id for its body.
    listSpaceDrafts(spaceId: string): Promise<DraftSummary[]>;

    // Publishes a draft into its page, creating that page when the draft is an
    // orphan, and discards the draft. `force` overwrites a concurrent edit.
    // Rejects with PublishConflictError on a 409 (see data/publish_conflict).
    publishPageDraft(spaceId: string, pageId: string, force: boolean): Promise<Page>;
}
