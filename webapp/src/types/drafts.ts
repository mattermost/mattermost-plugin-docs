// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from './docs';

/**
 * A per-user autosave draft (server model.Draft, table DOCS_Draft).
 *
 * Keyed by (user_id, page_id) server-side, with no id of its own. `page_id` is the
 * page the draft belongs to — reserved at creation for a page that does not exist
 * yet, which is what makes a draft addressable before it is published.
 *
 * A draft whose `page_id` has no published page is an *orphan*: a new page nobody
 * else can see yet. One whose page does exist is unpublished edits to that page.
 * The two look identical here and are told apart by the store (see
 * getOrphanDraftsForSpace), because they read very differently in the UI.
 *
 * Note the absence of a sort order: the server has no SortOrder column for drafts,
 * so an orphan draft cannot be interleaved with its published siblings.
 */
export type Draft = {
    user_id: string;
    space_id: string;
    page_id: string;

    // The pending parent for an unpublished page; applied on publish.
    parent_id: string;
    title: string;
    body: string;
    file_ids: string[];
    props: Record<string, unknown>;
    create_at: number;
    update_at: number;

    // Last autosave heartbeat, backing the active-editors presence read.
    last_active_at: number;

    /**
     * The optimistic-lock baseline: the page `edit_at` this draft was started from,
     * 0 for a page that does not exist yet. Write-once server-side — resending it
     * on later autosaves does not re-baseline, so recovering from a conflict means
     * force-publishing or discarding, never autosaving a newer baseline.
     */
    base_edit_at: number;
};

// The projection returned by the space-wide drafts collection: metadata only, no
// content. Fetch the draft by page id when the body is needed.
export type DraftSummary = Omit<Draft, 'body' | 'props' | 'base_edit_at'>;

export type StoredDraft = Draft | DraftSummary;

export const isFullDraft = (draft: StoredDraft): draft is Draft => 'body' in draft;

// A nil field means "leave unchanged".
export type DraftPatch = {
    title?: string;
    body?: string;
    parent_id?: string;
    file_ids?: string[];
    props?: Record<string, unknown>;
    base_edit_at?: number;
};

export type PageActiveEditors = {
    active_editors: string[];
    snapshot_at: number;
    active_timeout_ms: number;
};

// Why a publish was refused. `parent_unpublished` cannot be forced — the parent
// has to be published first — while the edit conflicts can.
export const ConflictReason = {
    ConcurrentEdit: 'concurrent_edit',
    ConcurrentAutosave: 'concurrent_autosave',
    ParentUnpublished: 'parent_unpublished',
} as const;

export type ConflictReasonType = typeof ConflictReason[keyof typeof ConflictReason];

// The 409 body from publish, which carries data rather than only an error: the
// current server page, so the caller can re-baseline without another read.
export type PublishConflict = {
    error: {
        id: string;
        message: string;
        status_code: number;
    };
    current_page: Page | null;
};
