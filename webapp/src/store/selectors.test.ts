// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import type {GlobalState} from '@mattermost/types/store';

import type {Page} from 'types/docs';
import type {Draft} from 'types/drafts';

import {
    areDraftsLoadedForSpace,
    getDraftForPage,
    getDraftsForSpace,
    getOrphanDraftsForSpace,
    getPagesForSpace,
    getSpacesInTeam,
    hasUnpublishedEdits,
} from './selectors';
import {makeDraft, makePage, makeSpace} from './test_fixtures';
import type {DocsEntitiesState, DocsPluginState} from './types';

const EMPTY: DocsEntitiesState = {
    spaces: {},
    spacesInTeam: {},
    pages: {},
    pagesInSpace: {},
    spaceMembers: {},
    spaceMemberPermissionsRevision: {},
    drafts: {},
    draftsInSpace: {},
};

const makeState = (docsState: DocsPluginState): GlobalState =>
    ({['plugins-' + manifest.id]: docsState}) as unknown as GlobalState;

const stateWith = (entities: Partial<DocsEntitiesState>): GlobalState =>
    makeState({entities: {...EMPTY, ...entities}});

const indexed = <T extends Page | Draft>(items: T[], key: (item: T) => string) =>
    Object.fromEntries(items.map((item) => [key(item), item]));

describe('getSpacesInTeam', () => {
    it('resolves a team\'s ids to spaces ordered by sort_order, ignoring stale ids', () => {
        const spaceA = makeSpace('a', 'Space A', 't1', 1);
        const spaceB = makeSpace('b', 'Space B', 't1', 0);

        const state = stateWith({
            spaces: {a: spaceA, b: spaceB},
            spacesInTeam: {t1: new Set(['a', 'b', 'missing'])},
        });

        expect(getSpacesInTeam(state, 't1')).toEqual([spaceB, spaceA]);
        expect(getSpacesInTeam(state, 't2')).toEqual([]);
    });
});

describe('getPagesForSpace', () => {
    it('resolves page ids for a space, ignoring other spaces', () => {
        const page1 = makePage('p1', 'space-a', 'Page 1');
        const page2 = makePage('p2', 'space-b', 'Page 2');

        const state = stateWith({
            pages: {p1: page1, p2: page2},
            pagesInSpace: {'space-a': new Set(['p1']), 'space-b': new Set(['p2'])},
        });

        expect(getPagesForSpace(state, 'space-a')).toEqual([page1]);
        expect(getPagesForSpace(state, 'space-c')).toEqual([]);
    });
});

describe('draft selectors', () => {
    const orphan = makeDraft('new1', 'space-a', 'Unpublished page', 20);
    const edits = makeDraft('published', 'space-a', 'Edited title', 30, 7);
    const elsewhere = makeDraft('new2', 'space-b', 'Other space', 40);
    const published = makePage('published', 'space-a', 'Published');

    const state = stateWith({
        pages: indexed([published], (page) => page.id),
        pagesInSpace: {'space-a': new Set(['published'])},
        drafts: indexed([orphan, edits, elsewhere], (draft) => draft.page_id),
        draftsInSpace: {
            'space-a': new Set(['new1', 'published']),
            'space-b': new Set(['new2']),
        },
    });

    it('getDraftForPage reads a draft by the page it belongs to', () => {
        expect(getDraftForPage(state, 'published')).toEqual(edits);
        expect(getDraftForPage(state, 'nothing')).toBeUndefined();
    });

    it('getDraftForPage does not expose a metadata-only summary as a full draft', () => {
        const summary = {...orphan};
        delete (summary as Partial<typeof summary>).body;
        delete (summary as Partial<typeof summary>).props;
        delete (summary as Partial<typeof summary>).base_edit_at;
        const summaryState = stateWith({drafts: {new1: summary}});

        expect(getDraftForPage(summaryState, 'new1')).toBeUndefined();
    });

    // A draft alone doesn't mean "unpublished changes" — an orphan draft is an
    // unpublished page, which reads differently and gets its own row.
    it('hasUnpublishedEdits is true only for a draft whose page exists', () => {
        expect(hasUnpublishedEdits(state, 'published')).toBe(true);
        expect(hasUnpublishedEdits(state, 'new1')).toBe(false);
    });

    it('getDraftsForSpace scopes to the space, newest first', () => {
        expect(getDraftsForSpace(state, 'space-a')).toEqual([edits, orphan]);
        expect(getDraftsForSpace(state, 'space-c')).toEqual([]);
    });

    // The trap this selector exists to prevent: a draft whose page is already in
    // the tree must not also appear as a draft row, or the page renders twice.
    it('getOrphanDraftsForSpace excludes drafts for pages that exist', () => {
        expect(getOrphanDraftsForSpace(state, 'space-a')).toEqual([orphan]);
    });

    it('getOrphanDraftsForSpace returns a stable empty array', () => {
        const none = stateWith({draftsInSpace: {'space-a': new Set()}});

        expect(getOrphanDraftsForSpace(none, 'space-a')).
            toBe(getOrphanDraftsForSpace(none, 'space-a'));
    });

    it('areDraftsLoadedForSpace tells "none" apart from "not fetched"', () => {
        const fetchedEmpty = stateWith({draftsInSpace: {'space-a': new Set()}});

        expect(areDraftsLoadedForSpace(fetchedEmpty, 'space-a')).toBe(true);
        expect(areDraftsLoadedForSpace(fetchedEmpty, 'space-b')).toBe(false);
    });
});
