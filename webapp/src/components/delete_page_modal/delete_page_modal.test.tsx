// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import {createMemoryHistory} from 'history';
import React from 'react';

import {makePage, makeSpace, makeTeam} from 'store/test_fixtures';

import {toast} from 'components/toast';

import type {Page} from 'types/docs';

import DeletePageModal from './delete_page_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockDeletePage = jest.fn();
let mockDeleteResult: Promise<void> = Promise.resolve();

// deletePage is a thunk the modal awaits, so the mock has to be a thunk too.
jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    deletePage: (...args: unknown[]) => {
        mockDeletePage(...args as []);
        return async () => mockDeleteResult;
    },
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const TEAM = makeTeam('team1id', 'team1');
const SPACE = makeSpace('space1', 'Engineering', TEAM.id);

const child = (id: string, parentId: string): Page => ({
    ...makePage(id, SPACE.id, id),
    parent_id: parentId,
});

// parent
//   childOfParent
// unrelated
const PAGES: Page[] = [makePage('parent', SPACE.id, 'Parent'), child('childOfParent', 'parent'), makePage('unrelated', SPACE.id, 'Unrelated')];

const PAGE_PATH = `/team1/spaces/${SPACE.id}/childOfParent`;
const SPACE_PATH = `/team1/spaces/${SPACE.id}`;

const renderModal = (deletingPageId: string) => {
    const history = createMemoryHistory({initialEntries: [PAGE_PATH]});
    const onClose = jest.fn();

    const rendered = renderWithContext(
        <DeletePageModal
            spaceId={SPACE.id}
            pageId={deletingPageId}
            pageTitle={deletingPageId}
            onClose={onClose}
        />,
        {
            history,
            state: {
                currentTeam: TEAM,
                docs: {
                    spaces: {[SPACE.id]: SPACE},
                    pages: Object.fromEntries(PAGES.map((page) => [page.id, page])),
                    pagesInSpace: {[SPACE.id]: new Set(PAGES.map((page) => page.id))},
                },
            },
        },
    );

    return {...rendered, history, onClose};
};

const confirmDelete = () => fireEvent.click(screen.getByRole('button', {name: 'Delete'}));

beforeEach(() => {
    jest.clearAllMocks();
    mockDeleteResult = Promise.resolve();
});

describe('DeletePageModal', () => {
    it('sends the viewer to the space, not Docs home, when the routed page is deleted', async () => {
        const {history} = renderModal('childOfParent');

        confirmDelete();

        await waitFor(() => expect(history.location.pathname).toBe(SPACE_PATH));
        expect(mockDeletePage).toHaveBeenCalledWith(SPACE.id, 'childOfParent');
    });

    it('replaces the deleted page in history rather than pushing, so Back cannot return to it', async () => {
        const {history} = renderModal('childOfParent');

        confirmDelete();

        await waitFor(() => expect(history.location.pathname).toBe(SPACE_PATH));
        expect(history.length).toBe(1);
    });

    // Subpages are deleted with their parent, so viewing a descendant of the
    // deleted page loses the page too — the exact-match check missed this.
    it('sends the viewer to the space when an ancestor of the routed page is deleted', async () => {
        const {history} = renderModal('parent');

        confirmDelete();

        await waitFor(() => expect(history.location.pathname).toBe(SPACE_PATH));
    });

    it('stays put when the deleted page is not the one being viewed', async () => {
        const {history, onClose} = renderModal('unrelated');

        confirmDelete();

        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(history.location.pathname).toBe(PAGE_PATH);
    });

    it('surfaces a failure and leaves the viewer on the page', async () => {
        mockDeleteResult = Promise.reject(new Error('nope'));

        const {history, onClose} = renderModal('childOfParent');

        confirmDelete();

        await waitFor(() => expect(toast.error).toHaveBeenCalled());
        expect(history.location.pathname).toBe(PAGE_PATH);
        expect(onClose).not.toHaveBeenCalled();
        expect(screen.getByRole('heading', {name: 'Delete page'})).toBeInTheDocument();
    });
});
