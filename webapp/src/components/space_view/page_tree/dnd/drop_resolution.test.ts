// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {makePage} from 'store/test_fixtures';

import type {Page} from 'types/docs';

import {computeDropTarget} from './use_page_drag_drop';
import {resolveMove} from './use_pages_dnd';

const child = (id: string, parentId: string, sortOrder = 0): Page => ({
    ...makePage(id, 'space1', id, sortOrder),
    parent_id: parentId,
});

// a
//   a1
//   a2
// b
const pages: Page[] = [
    makePage('a', 'space1', 'a', 0),
    child('a1', 'a', 0),
    child('a2', 'a', 1),
    makePage('b', 'space1', 'b', 1),
];

describe('resolveMove', () => {
    it('drops above a sibling at that sibling\'s index', () => {
        expect(resolveMove(pages, 'a2', 'a1', {mode: 'reorder', edge: 'top'})).toEqual({
            pageId: 'a2', parentId: 'a', siblingIndex: 0,
        });
    });

    it('drops below a sibling at the next index', () => {
        expect(resolveMove(pages, 'a1', 'a2', {mode: 'reorder', edge: 'bottom'})).toEqual({
            pageId: 'a1', parentId: 'a', siblingIndex: 1,
        });
    });

    // The index the server stores is the position after the move, so the dragged
    // page must not still be counted among its own new siblings.
    it('counts siblings with the dragged page removed', () => {
        // 'a1' leaves the group, so 'b' dropped below 'a' lands at index 1 of the
        // root group rather than being pushed past a phantom entry.
        expect(resolveMove(pages, 'a1', 'a', {mode: 'reorder', edge: 'bottom'})).toEqual({
            pageId: 'a1', parentId: '', siblingIndex: 1,
        });
    });

    it('reparents onto a row as its last child', () => {
        expect(resolveMove(pages, 'b', 'a', {mode: 'reparent'})).toEqual({
            pageId: 'b', parentId: 'a', siblingIndex: 2,
        });
    });

    // Re-dropping a page onto the parent it already has must not count it twice.
    it('excludes the dragged page when reparenting onto its current parent', () => {
        expect(resolveMove(pages, 'a2', 'a', {mode: 'reparent'})).toEqual({
            pageId: 'a2', parentId: 'a', siblingIndex: 1,
        });
    });

    it('resolves to nothing for a target the tree does not hold', () => {
        expect(resolveMove(pages, 'a1', 'nope', {mode: 'reorder', edge: 'top'})).toBeNull();
    });

    it('orders siblings by sort order, not by array position', () => {
        const unordered: Page[] = [
            makePage('x', 'space1', 'x', 2),
            makePage('y', 'space1', 'y', 0),
            makePage('z', 'space1', 'z', 1),
        ];

        // Sorted, the group reads y, z, x — so dropping below 'z' is index 2.
        expect(resolveMove(unordered, 'x', 'z', {mode: 'reorder', edge: 'bottom'})).toEqual({
            pageId: 'x', parentId: '', siblingIndex: 2,
        });
    });
});

describe('computeDropTarget', () => {
    // Only getBoundingClientRect is read, so a stub rect is the whole element.
    const rowOfHeight = (height: number, top = 0): Element => ({
        getBoundingClientRect: () => ({top, height}),
    } as Element);

    const row = rowOfHeight(100);

    it('reorders above the row in the top quarter', () => {
        expect(computeDropTarget(row, 0, false)).toEqual({mode: 'reorder', edge: 'top'});
        expect(computeDropTarget(row, 25, false)).toEqual({mode: 'reorder', edge: 'top'});
    });

    it('reparents across the middle half', () => {
        expect(computeDropTarget(row, 26, false)).toEqual({mode: 'reparent'});
        expect(computeDropTarget(row, 50, false)).toEqual({mode: 'reparent'});
        expect(computeDropTarget(row, 74, false)).toEqual({mode: 'reparent'});
    });

    it('reorders below the row in the bottom quarter when it is collapsed', () => {
        expect(computeDropTarget(row, 75, false)).toEqual({mode: 'reorder', edge: 'bottom'});
        expect(computeDropTarget(row, 100, false)).toEqual({mode: 'reorder', edge: 'bottom'});
    });

    // An expanded row's children are drawn below it, so a "below" indicator there
    // would sit above the very subtree the drop lands after.
    it('gives an expanded row\'s bottom band to reparent instead', () => {
        expect(computeDropTarget(row, 75, true)).toEqual({mode: 'reparent'});
        expect(computeDropTarget(row, 100, true)).toEqual({mode: 'reparent'});

        // The top edge still reorders either way.
        expect(computeDropTarget(row, 10, true)).toEqual({mode: 'reorder', edge: 'top'});
    });

    it('measures the band against the row, not absolute pixels', () => {
        const offset = rowOfHeight(40, 500);

        expect(computeDropTarget(offset, 505, false)).toEqual({mode: 'reorder', edge: 'top'});
        expect(computeDropTarget(offset, 520, false)).toEqual({mode: 'reparent'});
        expect(computeDropTarget(offset, 535, false)).toEqual({mode: 'reorder', edge: 'bottom'});
    });
});
