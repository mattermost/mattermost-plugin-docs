// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeDraft, makePage, makeSpace} from 'store/test_fixtures';

import {getReadoutMessage} from 'components/readout';
import {toast} from 'components/toast';

import type {Page} from 'types/docs';
import type {Draft} from 'types/drafts';

import PageTreePanel from './page_tree_panel';

import {renderWithContext} from '../../../../tests/react_testing_utils';

const mockGoToPage = jest.fn();
const mockMovePage = jest.fn();
let mockMoveResult: Promise<void> = Promise.resolve();

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({
        goToPage: mockGoToPage,
        pageId: undefined,
        paths: {
            overview: (spaceId: string) => `/docs/${spaceId}/overview`,
            page: (spaceId: string, pageId: string) => `/docs/${spaceId}/${pageId}`,
            draft: (spaceId: string, pageId: string) => `/docs/${spaceId}/drafts/${pageId}`,
        },
    }),
}));

// movePage is a thunk that resolves or rejects, and the panel reacts to both, so
// the mock has to be a thunk too rather than a bare action.
jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    movePage: (...args: unknown[]) => {
        mockMovePage(...args as []);
        return async () => mockMoveResult;
    },
}));

// mattermost-redux/actions/preferences is untransformed ESM under jest, so the
// favorites hooks are mocked at their own boundary (as in space_item_menu.test).
jest.mock('hooks/favorites', () => ({
    useIsFavorite: () => false,
    useToggleFavorite: () => jest.fn(),
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const SPACE = makeSpace('space1', 'Engineering');

const child = (id: string, parentId: string, sortOrder = 0): Page => ({
    ...makePage(id, SPACE.id, id, sortOrder),
    parent_id: parentId,
});

// a
//   a1
//   a2
// b
const PAGES: Page[] = [
    makePage('a', SPACE.id, 'a', 0),
    child('a1', 'a', 0),
    child('a2', 'a', 1),
    makePage('b', SPACE.id, 'b', 1),
];

const renderPanel = ({drafts = []}: {drafts?: Draft[]} = {}) => renderWithContext(<PageTreePanel space={SPACE}/>, {
    state: {
        docs: {
            spaces: {[SPACE.id]: SPACE},
            pages: Object.fromEntries(PAGES.map((page) => [page.id, page])),
            pagesInSpace: {[SPACE.id]: new Set(PAGES.map((page) => page.id))},
            drafts: Object.fromEntries(drafts.map((draft) => [draft.page_id, draft])),
            draftsInSpace: {[SPACE.id]: new Set(drafts.map((draft) => draft.page_id))},
        },
    },
});

// A row's accessible name folds in its chevron and menu labels, so rows are found
// by their title text and walked up to the treeitem.
const row = (title: string) => screen.getByText(title).closest('[role="treeitem"]') as HTMLElement;

const menuTriggers = () => screen.getAllByRole('button', {name: /^Page options for /});

// Rows are anchors so a page can be opened in a new tab, copied, or middle-clicked
// like any other address; a div with an onClick supports none of that.
describe('PageTreePanel row links', () => {
    it('renders each row as a link to its page', () => {
        renderPanel();

        expect(screen.getByText('a1').closest('a')).toHaveAttribute('href', `/docs/${SPACE.id}/a1`);
    });

    // A draft has no published page, so a page address would 404 into the overview
    // redirect — which is what made draft rows look unclickable.
    it('links a draft row to its draft address', () => {
        renderPanel({drafts: [makeDraft('d1', SPACE.id, 'Unpublished')]});

        expect(screen.getByText('Unpublished').closest('a')).
            toHaveAttribute('href', `/docs/${SPACE.id}/drafts/d1`);
    });
});

describe('PageTreePanel keyboard support', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        localStorage.clear();
        mockMoveResult = Promise.resolve();
    });

    it('exposes a single tab stop for the whole tree', () => {
        renderPanel();

        const tabbable = screen.getAllByRole('treeitem').filter((item) => item.getAttribute('tabindex') === '0');
        expect(tabbable).toHaveLength(1);
        expect(tabbable[0]).toHaveTextContent('a');
    });

    // Shift+F10 and the Menu key are the platform conventions for this, but Apple
    // keyboards have neither — so Tab has to reach the menu too, or the tree's
    // per-page actions are unreachable by keyboard on a Mac.
    it('puts the menu of the tab-stop row in the tab order, and no other', () => {
        renderPanel();

        const tabbable = menuTriggers().filter((button) => button.getAttribute('tabindex') === '0');

        expect(tabbable).toHaveLength(1);
        expect(tabbable[0]).toHaveAccessibleName('Page options for a');
    });

    it('moves the tabbable menu along with the tab stop', () => {
        renderPanel();

        row('a').focus();
        fireEvent.keyDown(row('a'), {key: 'ArrowDown'});

        const tabbable = menuTriggers().filter((button) => button.getAttribute('tabindex') === '0');

        expect(tabbable).toHaveLength(1);
        expect(tabbable[0]).toHaveAccessibleName('Page options for a1');
    });

    it('nests child rows in a group so nesting is exposed', () => {
        renderPanel();

        expect(screen.getAllByRole('group')).toHaveLength(1);
        expect(row('a')).toHaveAttribute('aria-level', '1');
        expect(row('a1')).toHaveAttribute('aria-level', '2');
    });

    it('moves focus down, up, and to the ends of the visible rows', () => {
        renderPanel();

        row('a').focus();

        fireEvent.keyDown(row('a'), {key: 'ArrowDown'});
        expect(row('a1')).toHaveFocus();

        fireEvent.keyDown(row('a1'), {key: 'ArrowUp'});
        expect(row('a')).toHaveFocus();

        fireEvent.keyDown(row('a'), {key: 'End'});
        expect(row('b')).toHaveFocus();

        fireEvent.keyDown(row('b'), {key: 'Home'});
        expect(row('a')).toHaveFocus();
    });

    it('collapses with ArrowLeft and climbs to the parent when there is nothing to close', () => {
        renderPanel();

        fireEvent.keyDown(row('a1'), {key: 'ArrowLeft'});
        expect(row('a')).toHaveFocus();

        fireEvent.keyDown(row('a'), {key: 'ArrowLeft'});
        expect(screen.queryByText('a1')).not.toBeInTheDocument();

        fireEvent.keyDown(row('a'), {key: 'ArrowRight'});
        expect(screen.getByText('a1')).toBeInTheDocument();
    });

    it('opens the routed page on Enter', () => {
        const {history} = renderPanel();

        fireEvent.keyDown(row('a1'), {key: 'Enter'});
        expect(history.location.pathname).toBe(`/docs/${SPACE.id}/a1`);
    });

    it('reorders with Alt+arrow and announces the new position', () => {
        renderPanel();

        fireEvent.keyDown(row('a2'), {key: 'ArrowUp', altKey: true});

        expect(mockMovePage).toHaveBeenCalledWith(SPACE.id, 'a2', 'a', 0);
        expect(getReadoutMessage()).toBe('Moved a2 to position 1 under a.');
    });

    it('nests with Alt+ArrowRight under the preceding sibling', () => {
        renderPanel();

        fireEvent.keyDown(row('b'), {key: 'ArrowRight', altKey: true});

        expect(mockMovePage).toHaveBeenCalledWith(SPACE.id, 'b', 'a', 2);
    });

    it('announces a refused move instead of silently doing nothing', () => {
        renderPanel();

        fireEvent.keyDown(row('a1'), {key: 'ArrowUp', altKey: true});

        expect(mockMovePage).not.toHaveBeenCalled();
        expect(getReadoutMessage()).toBe('Cannot move a1 any further in that direction.');
    });

    it('surfaces a failed move instead of letting the row snap back silently', async () => {
        mockMoveResult = Promise.reject(new Error('boom'));
        renderPanel();

        fireEvent.keyDown(row('a2'), {key: 'ArrowUp', altKey: true});
        await waitFor(() => expect(toast.error).toHaveBeenCalled());
    });

    // Treeitems nest, so a key event on a child bubbles through every ancestor
    // treeitem; without a target guard one keypress moves several rows.
    it('handles a key press once, not once per ancestor level', () => {
        renderPanel();

        fireEvent.keyDown(row('a1'), {key: 'ArrowDown', altKey: true});

        expect(mockMovePage).toHaveBeenCalledTimes(1);
        expect(mockMovePage).toHaveBeenCalledWith(SPACE.id, 'a1', 'a', 1);
    });

    it('tracks the focused row itself, not its ancestors', () => {
        renderPanel();

        fireEvent.focus(row('a1'));

        const tabbable = screen.getAllByRole('treeitem').filter((item) => item.getAttribute('tabindex') === '0');
        expect(tabbable).toHaveLength(1);
        expect(tabbable[0]).toBe(row('a1'));
    });

    it('describes the tree with its keyboard instructions', () => {
        renderPanel();

        const tree = screen.getByRole('tree');
        const help = document.getElementById(tree.getAttribute('aria-describedby') ?? '');
        expect(help).toHaveTextContent(/Hold Alt with an arrow key/);
    });
});
